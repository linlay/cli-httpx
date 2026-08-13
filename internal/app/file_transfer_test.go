package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMultipartUploadStreamsInOrderWithLengthAndReopensOnRetry(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "report.docx")
	if err := os.WriteFile(sourcePath, []byte("version-one"), 0o600); err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int32
	var uploadedFiles []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempt := attempts.Add(1)
		if request.ContentLength <= 0 {
			t.Errorf("expected fixed Content-Length, got %d", request.ContentLength)
		}
		if len(request.TransferEncoding) != 0 {
			t.Errorf("unexpected transfer encoding: %v", request.TransferEncoding)
		}
		mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Errorf("unexpected multipart content type %q: %v", request.Header.Get("Content-Type"), err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reader := multipart.NewReader(request.Body, parameters["boundary"])
		title, err := reader.NextPart()
		if err != nil {
			t.Errorf("read title part: %v", err)
			return
		}
		if title.FormName() != "title" || title.FileName() != "" {
			t.Errorf("unexpected first part: name=%q filename=%q", title.FormName(), title.FileName())
		}
		titleContent, _ := io.ReadAll(title)
		if string(titleContent) != "Review report" {
			t.Errorf("unexpected title: %q", titleContent)
		}
		filePart, err := reader.NextPart()
		if err != nil {
			t.Errorf("read file part: %v", err)
			return
		}
		if filePart.FormName() != "file" || filePart.FileName() != "report.docx" {
			t.Errorf("unexpected file part: name=%q filename=%q", filePart.FormName(), filePart.FileName())
		}
		if got := filePart.Header.Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
			t.Errorf("unexpected file content type: %q", got)
		}
		fileContent, _ := io.ReadAll(filePart)
		uploadedFiles = append(uploadedFiles, string(fileContent))
		if extra, err := reader.NextPart(); err != io.EOF || extra != nil {
			t.Errorf("expected multipart EOF, got part=%v err=%v", extra, err)
		}
		if attempt == 1 {
			if err := os.WriteFile(sourcePath, []byte("version-two"), 0o600); err != nil {
				t.Errorf("replace source before retry: %v", err)
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"doc-id"}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.upload]
description = "Upload"
method = "POST"
path = "/upload"
retries = 1
multipart = [
  { name = "title", value = { from = "param", key = "title" } },
  { name = "file", file = { from = "param", key = "file_path" }, content_type = "application/vnd.openxmlformats-officedocument.wordprocessingml.document", max_bytes = 1024 }
]
expect_status = 201
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "json", "run", "demo", "upload", "--param", "title=Review report", "--param", "file_path=" + sourcePath})
	if exitCode != ExitSuccess {
		t.Fatalf("upload failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if got, want := uploadedFiles, []string{"version-one", "version-two"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("upload retry did not reopen source: got %v want %v", got, want)
	}
}

func TestMultipartInspectDoesNotReadOrLeakDynamicFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing-secret.docx")
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.upload]
description = "Upload"
path = "/upload"
multipart = [
  { name = "file", file = { from = "param", key = "file_path" }, max_bytes = 17 }
]
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "upload", "--param", "file_path=" + missingPath})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect failed: exit=%d stderr=%s", exitCode, stderr)
	}
	if strings.Contains(stdout, missingPath) || strings.Contains(stdout, "missing-secret.docx") {
		t.Fatalf("inspect leaked dynamic path: %s", stdout)
	}
	if !strings.Contains(stdout, `"path": "***"`) || !strings.Contains(stdout, `"max_bytes": 17`) {
		t.Fatalf("inspect omitted redacted metadata: %s", stdout)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "inspect", "demo", "upload", "--reveal", "--param", "file_path=" + missingPath})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect --reveal read missing file: exit=%d stderr=%s", exitCode, stderr)
	}
	if !strings.Contains(stdout, missingPath) {
		t.Fatalf("inspect --reveal omitted path metadata: %s", stdout)
	}
}

func TestMultipartRejectsInvalidConfigurationAndFiles(t *testing.T) {
	tests := map[string]string{
		"body conflict": `body = { ok = true }
multipart = [{ name = "file", file = "/tmp/x" }]`,
		"both value and file": `multipart = [{ name = "file", value = "x", file = "/tmp/x" }]`,
		"value file metadata": `multipart = [{ name = "field", value = "x", max_bytes = 1 }]`,
		"empty multipart":     `multipart = []`,
	}
	for name, actionFields := range tests {
		t.Run(name, func(t *testing.T) {
			configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = "https://example.com"
[actions.upload]
description = "Upload"
path = "/upload"
%s
`, actionFields))
			if _, err := loadConfig(configPath); err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}

	sourcePath := filepath.Join(t.TempDir(), "too-large.docx")
	if err := os.WriteFile(sourcePath, []byte("too large"), 0o600); err != nil {
		t.Fatal(err)
	}
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo site"
base_url = "https://example.com"
[actions.upload]
description = "Upload"
path = "/upload"
multipart = [{ name = "file", file = { from = "param", key = "file_path" }, max_bytes = 2 }]
`)
	_, stderr, exitCode := runMain(t, []string{"--config", configDir, "run", "demo", "upload", "--param", "file_path=" + sourcePath})
	if exitCode != ExitExecution || !strings.Contains(stderr, "exceeds max_bytes") {
		t.Fatalf("expected pre-send max_bytes error, exit=%d stderr=%s", exitCode, stderr)
	}
}

func TestDownloadWritesHashAndSupportsTextAndJSONOutput(t *testing.T) {
	content := []byte("persisted document bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		_, _ = w.Write(content)
	}))
	t.Cleanup(server.Close)

	configDir := downloadTestConfig(t, server.URL, 1024)
	firstPath := filepath.Join(t.TempDir(), "first.docx")
	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "text", "run", "demo", "download", "--param", "output_path=" + firstPath})
	if exitCode != ExitSuccess || stdout != firstPath {
		t.Fatalf("text download failed: exit=%d stdout=%q stderr=%s", exitCode, stdout, stderr)
	}
	assertFileContent(t, firstPath, content)

	secondPath := filepath.Join(t.TempDir(), "second.docx")
	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "json", "run", "demo", "download", "--param", "output_path=" + secondPath})
	if exitCode != ExitSuccess {
		t.Fatalf("JSON download failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	var result envelope
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	wantHash := sha256.Sum256(content)
	if result.Download == nil || result.Download.Path != secondPath || result.Download.SizeBytes != int64(len(content)) || result.Download.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("unexpected download envelope: %#v", result.Download)
	}
	if result.Download.ContentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("unexpected content type: %q", result.Download.ContentType)
	}
}

func TestDownloadNoClobberOverwriteAndFailureCleanup(t *testing.T) {
	content := []byte("new-content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write(content)
	}))
	t.Cleanup(server.Close)
	configDir := downloadTestConfig(t, server.URL, 4)
	dir := t.TempDir()
	target := filepath.Join(dir, "report.docx")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "run", "demo", "download", "--param", "output_path=" + target})
	if exitCode != ExitExecution || !strings.Contains(stderr, "already exists") {
		t.Fatalf("expected no-clobber error: exit=%d stderr=%s", exitCode, stderr)
	}
	assertFileContent(t, target, []byte("original"))

	_, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "run", "demo", "download", "--param", "output_path=" + target, "--param", "overwrite=true"})
	if exitCode != ExitExecution || !strings.Contains(stderr, "exceeds max_bytes") {
		t.Fatalf("expected max_bytes error: exit=%d stderr=%s", exitCode, stderr)
	}
	assertFileContent(t, target, []byte("original"))
	assertNoDownloadTemps(t, dir)

	configDir = downloadTestConfig(t, server.URL, 1024)
	_, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "run", "demo", "download", "--param", "output_path=" + target, "--param", "overwrite=true"})
	if exitCode != ExitSuccess {
		t.Fatalf("overwrite failed: exit=%d stderr=%s", exitCode, stderr)
	}
	assertFileContent(t, target, content)
	assertNoDownloadTemps(t, dir)
}

func TestDownloadUnexpectedStatusDoesNotCreateTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"denied"}`))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	target := filepath.Join(dir, "report.docx")
	configDir := downloadTestConfig(t, server.URL, 1024)
	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "json", "run", "demo", "download", "--param", "output_path=" + target})
	if exitCode != ExitAssertion || stderr != "" {
		t.Fatalf("expected assertion response: exit=%d stderr=%q stdout=%s", exitCode, stderr, stdout)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected target after HTTP error: %v", err)
	}
	assertNoDownloadTemps(t, dir)
}

func TestDownloadConfigurationRejectsExtractorAndSave(t *testing.T) {
	for name, extra := range map[string]string{
		"extractor": `extract_type = "jq"
extract_expr = ".body"`,
		"save": `save = { token = ".body.token" }`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = "https://example.com"
[actions.download]
description = "Download"
path = "/download"
download = { path = "/tmp/output" }
%s
`, extra))
			if _, err := loadConfig(configPath); err == nil {
				t.Fatal("expected config rejection")
			}
		})
	}
}

func downloadTestConfig(t *testing.T, serverURL string, maxBytes int64) string {
	t.Helper()
	return writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
[actions.download]
description = "Download"
path = "/download"
expect_status = 200
download = { path = { from = "param", key = "output_path" }, overwrite = { from = "param", key = "overwrite", default = false }, max_bytes = %d }
`, serverURL, maxBytes))
}

func assertFileContent(t *testing.T, path string, expected []byte) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != string(expected) {
		t.Fatalf("unexpected file content: got %q want %q", content, expected)
	}
}

func assertNoDownloadTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.httpx-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary download files remain: %v", matches)
	}
}
