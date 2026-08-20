package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linlay/cli-httpx/internal/buildinfo"
)

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
bogus = true
`)

	_, err := loadConfig(configPath)
	if err == nil {
		t.Fatal("expected config error")
	}
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig, got %v", err)
	}
	if !strings.Contains(err.Error(), configPath) {
		t.Fatalf("expected config path in error, got %v", err)
	}
}

func TestLoadConfigAcceptsEnvSource(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = { from = "env", key = "HTTPX_PATH", prefix = "/" }
`)

	if _, err := loadConfig(configPath); err != nil {
		t.Fatalf("expected env source to load, got %v", err)
	}
}

func TestLoadConfigAcceptsFileDataURLSource(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.create]
description = "Upload image"
method = "POST"
path = "/images"
body = { dataUrl = { from = "file_data_url", path = { from = "env", key = "HTTPX_IMAGE_PATH", trim = true }, max_bytes = 8388608, allowed_media_types = ["image/png", "image/jpeg"] } }
`)

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("expected file_data_url source to load, got %v", err)
	}
	body, ok := cfg.Actions["create"].Body.(map[string]any)
	if !ok {
		t.Fatalf("expected object body, got %T", cfg.Actions["create"].Body)
	}
	source, ok := body["dataUrl"].(map[string]any)
	if !ok {
		t.Fatalf("expected dataUrl dynamic source, got %T", body["dataUrl"])
	}
	if _, ok, err := parseSourceSpec(source); err != nil || !ok {
		t.Fatalf("parse decoded file_data_url source: ok=%v err=%v", ok, err)
	}
}

func TestParseEnvSourceRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]any{
		"missing_key":     {"from": "env"},
		"empty_key":       {"from": "env", "key": ""},
		"scope":           {"from": "env", "key": "HTTPX_TOKEN", "scope": "global"},
		"default":         {"from": "env", "key": "HTTPX_TOKEN", "default": "fallback"},
		"prefix_type":     {"from": "env", "key": "HTTPX_TOKEN", "prefix": true},
		"suffix_type":     {"from": "env", "key": "HTTPX_TOKEN", "suffix": true},
		"pattern_type":    {"from": "env", "key": "HTTPX_TOKEN", "pattern": true},
		"pattern_empty":   {"from": "env", "key": "HTTPX_TOKEN", "pattern": ""},
		"pattern_invalid": {"from": "env", "key": "HTTPX_TOKEN", "pattern": "["},
		"unknown":         {"from": "env", "key": "HTTPX_TOKEN", "unknown": true},
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseSourceSpec(input); err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}
}

func TestParseLiteralSourceRejectsPrefix(t *testing.T) {
	t.Parallel()

	_, _, err := parseSourceSpec(map[string]any{
		"from":   "literal",
		"value":  "token",
		"prefix": "Bearer ",
	})
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoadConfigRejectsLegacyProfilesWrapper(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"

[profiles.demo]
base_url = "https://example.com"

[profiles.demo.actions.get]
path = "/"
`)

	_, err := loadConfig(configPath)
	if err == nil {
		t.Fatal("expected config error")
	}
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig, got %v", err)
	}
}

func TestLoadConfigRejectsLegacyExtractField(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract = ".body"
`)

	_, err := loadConfig(configPath)
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoadConfigRejectsNestedExtractorTable(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"

[actions.get.extractor]
type = "jq"
expr = ".body"
`)

	_, err := loadConfig(configPath)
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoadConfigIncludesPathInDecodeErrors(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.bad]
description = { text = "wrong type" }
path = "/"
`)

	_, err := loadConfig(configPath)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig, got %v", err)
	}
	if !strings.Contains(err.Error(), configPath) {
		t.Fatalf("expected config path in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "decode config") {
		t.Fatalf("expected decode config context, got %v", err)
	}
}

func TestLoadConfigAcceptsNestedBodyTable(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.create]
description = "Create issue"
method = "POST"
path = "/issues"
expect_status = 201

[actions.create.body.fields]
project = { key = "PRJ" }
title = { from = "param", key = "title" }
`)

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	actionBody, ok := cfg.Actions["create"].Body.(map[string]any)
	if !ok {
		t.Fatalf("expected body map, got %#v", cfg.Actions["create"].Body)
	}
	fields, ok := actionBody["fields"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested body.fields map, got %#v", actionBody["fields"])
	}
	project, ok := fields["project"].(map[string]any)
	if !ok || project["key"] != "PRJ" {
		t.Fatalf("unexpected nested project payload: %#v", fields["project"])
	}
}

func TestLoadConfigRejectsDuplicateActionInputSpecNames(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extracts = [
  { name = "days", type = "number" },
  { name = "days", type = "number" }
]
`)

	_, err := loadConfig(configPath)
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoadConfigRejectsLegacyLoginActionFields(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"
login_action = "login"

[actions.login]
description = "Login"
path = "/login"
`)

	_, err := loadConfig(configPath)
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoadConfigRejectsFlatExtractFieldsWithoutType(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"expr": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_expr = ".body"
`,
		"pattern": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_pattern = "id=([0-9]+)"
`,
		"group": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_group = 0
`,
		"all": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_all = false
`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath := writeConfig(t, content)
			_, err := loadConfig(configPath)
			if err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidFlatExtractorShapes(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"jq_with_group": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_type = "jq"
extract_expr = ".body"
extract_group = 1
`,
		"jq_with_all": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_type = "jq"
extract_expr = ".body"
extract_all = true
`,
		"regex_without_pattern": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_type = "regex"
`,
		"unknown_type": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
extract_type = "xml"
extract_expr = ".body"
`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath := writeConfig(t, content)
			_, err := loadConfig(configPath)
			if err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}
}

func TestLoadConfigRequiresDescriptions(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"site": `
version = 1
base_url = "https://example.com"

[actions.get]
description = "Fetch home"
path = "/"
`,
		"action": `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.get]
path = "/"
`,
	} {
		t.Run(name, func(t *testing.T) {
			configPath := writeConfig(t, content)
			_, err := loadConfig(configPath)
			if err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}
}

func TestCompileMergesDefaultsActionAndCLIOverride(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
timeout = "2s"
retries = 1

[headers]
X-Base = "base"
X-Shared = "site"

[query]
scope = "base"
region = "cn"

[actions.info]
description = "Load info"
path = "/v1/info"
timeout = "1s"
retries = 2
headers = { X-Shared = "action", X-Action = "1" }
query = { scope = "action" }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	state := &profileState{Values: map[string]string{}}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})

	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "info",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Timeout:  3 * time.Second,
			Format:   formatJSON,
		},
	}

	compiled, _, _, err := rt.compile(req, cfg, state)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if compiled.TimeoutMS != 3000 {
		t.Fatalf("expected timeout override to win, got %d", compiled.TimeoutMS)
	}
	if compiled.Retries != 2 {
		t.Fatalf("expected action retries, got %d", compiled.Retries)
	}
	if compiled.Headers["X-Base"] != "base" || compiled.Headers["X-Shared"] != "action" || compiled.Headers["X-Action"] != "1" {
		t.Fatalf("unexpected merged headers: %#v", compiled.Headers)
	}
	if !strings.Contains(compiled.URL, "scope=action") || !strings.Contains(compiled.URL, "region=cn") {
		t.Fatalf("unexpected merged query in URL: %s", compiled.URL)
	}
}

func TestResolverSupportsEnvFileSecretShellAndState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HTTPX_TEST_RESOLVER_ENV", " env-value \n")
	filePath := filepath.Join(tmpDir, "token.txt")
	if err := os.WriteFile(filePath, []byte(" file-value \n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	secretDir := filepath.Join(tmpDir, "secret")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatalf("mkdir secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "demo.json"), []byte(`{"cookie":" secret-value \n"}`), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	r := resolver{
		state:     &profileState{Values: map[string]string{"saved.token": " state-value \n"}},
		reveal:    true,
		site:      "demo",
		secretDir: secretDir,
	}

	cases := map[string]map[string]any{
		"env": {
			"from": "env",
			"key":  "HTTPX_TEST_RESOLVER_ENV",
			"trim": true,
		},
		"file": {
			"from": "file",
			"path": filePath,
			"trim": true,
		},
		"secret": {
			"from": "secret",
			"key":  "cookie",
			"trim": true,
		},
		"shell": {
			"from":       "shell",
			"cmd":        "printf 'shell-value\\n'",
			"timeout_ms": int64(1000),
			"trim":       true,
		},
		"state": {
			"from": "state",
			"key":  "saved.token",
			"trim": true,
		},
	}

	expected := map[string]string{
		"env":    "env-value",
		"file":   "file-value",
		"secret": "secret-value",
		"shell":  "shell-value",
		"state":  "state-value",
	}

	for name, input := range cases {
		value, err := r.resolveAny(context.Background(), input)
		if err != nil {
			t.Fatalf("%s resolve failed: %v", name, err)
		}
		if value != expected[name] {
			t.Fatalf("%s resolve mismatch: got %v want %q", name, value, expected[name])
		}
	}
}

func TestResolverAppliesPatternAndAffixesToStringSourcesAfterTrim(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HTTPX_TEST_PREFIX_ENV", " token-value \n")
	filePath := filepath.Join(tmpDir, "token.txt")
	if err := os.WriteFile(filePath, []byte(" token-value \n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	secretDir := filepath.Join(tmpDir, "secret")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatalf("mkdir secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "demo.json"), []byte(`{"token":" token-value \n"}`), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	r := resolver{
		state:     &profileState{Values: map[string]string{"token": " token-value \n"}},
		reveal:    true,
		params:    map[string]any{"token": " token-value \n"},
		site:      "demo",
		secretDir: secretDir,
	}
	cases := map[string]map[string]any{
		"param":  {"from": "param", "key": "token", "trim": true, "pattern": "[a-z-]+", "prefix": "Bearer ", "suffix": "!"},
		"env":    {"from": "env", "key": "HTTPX_TEST_PREFIX_ENV", "trim": true, "pattern": "[a-z-]+", "prefix": "Bearer ", "suffix": "!"},
		"file":   {"from": "file", "path": filePath, "trim": true, "pattern": "[a-z-]+", "prefix": "Bearer ", "suffix": "!"},
		"secret": {"from": "secret", "key": "token", "trim": true, "pattern": "[a-z-]+", "prefix": "Bearer ", "suffix": "!"},
		"shell":  {"from": "shell", "cmd": "printf ' token-value \\n'", "trim": true, "pattern": "[a-z-]+", "prefix": "Bearer ", "suffix": "!"},
		"state":  {"from": "state", "key": "token", "trim": true, "pattern": "[a-z-]+", "prefix": "Bearer ", "suffix": "!"},
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			value, err := r.resolveAny(context.Background(), input)
			if err != nil {
				t.Fatalf("resolve failed: %v", err)
			}
			if value != "Bearer token-value!" {
				t.Fatalf("unexpected transformed value: %#v", value)
			}
		})
	}
}

func TestResolverPrefixRequiresStringResult(t *testing.T) {
	r := resolver{
		reveal: true,
		params: map[string]any{"count": "42"},
	}
	_, err := r.resolveAny(context.Background(), map[string]any{
		"from":    "param",
		"key":     "count",
		"default": 0,
		"prefix":  "items-",
	})
	if err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("expected execution error, got %v", err)
	}
	if !strings.Contains(err.Error(), "prefix/suffix requires a string result") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolverPatternRequiresFullMatchWithoutLeakingValue(t *testing.T) {
	const invalidValue = "prefix-550e8400-e29b-41d4-a716-446655440000-suffix"
	t.Setenv("HTTPX_TEST_PARTIAL_DOCUMENT_ID", invalidValue)

	_, err := (resolver{reveal: true}).resolveAny(context.Background(), map[string]any{
		"from": "env", "key": "HTTPX_TEST_PARTIAL_DOCUMENT_ID", "pattern": "[0-9a-f-]{36}",
	})
	if err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("partial regex match must be rejected, got %v", err)
	}
	if strings.Contains(err.Error(), invalidValue) {
		t.Fatalf("pattern error leaked dynamic value: %v", err)
	}
}

func TestResolverAppliesPatternBeforeOutputTemplate(t *testing.T) {
	t.Setenv("HTTPX_TEST_DOCUMENT_ID", " 550e8400-e29b-41d4-a716-446655440000 \n")
	r := resolver{reveal: true}

	value, err := r.resolveAny(context.Background(), map[string]any{
		"from":            "env",
		"key":             "HTTPX_TEST_DOCUMENT_ID",
		"trim":            true,
		"pattern":         `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
		"output_template": "/api/v1/documents/{{value}}/ai/session",
	})
	if err != nil {
		t.Fatalf("resolve templated document path: %v", err)
	}
	if value != "/api/v1/documents/550e8400-e29b-41d4-a716-446655440000/ai/session" {
		t.Fatalf("unexpected templated value: %#v", value)
	}
}

func TestResolverPatternRejectsValueWithoutLeakingIt(t *testing.T) {
	const invalidValue = "NOT-A-DOCUMENT-ID"
	t.Setenv("HTTPX_TEST_INVALID_DOCUMENT_ID", invalidValue)
	r := resolver{reveal: true}

	_, err := r.resolveAny(context.Background(), map[string]any{
		"from":            "env",
		"key":             "HTTPX_TEST_INVALID_DOCUMENT_ID",
		"pattern":         `^[0-9a-f-]+$`,
		"output_template": "/documents/{{value}}",
	})
	if err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("expected pattern execution error, got %v", err)
	}
	if strings.Contains(err.Error(), invalidValue) {
		t.Fatalf("pattern error leaked dynamic value: %v", err)
	}
}

func TestParseSourceRejectsInvalidPatternAndOutputTemplate(t *testing.T) {
	t.Parallel()

	cases := map[string]map[string]any{
		"invalid_pattern": {
			"from": "env", "key": "HTTPX_VALUE", "pattern": "[",
		},
		"missing_placeholder": {
			"from": "env", "key": "HTTPX_VALUE", "output_template": "/fixed",
		},
		"unknown_placeholder": {
			"from": "env", "key": "HTTPX_VALUE", "output_template": "/{{value}}/{{other}}",
		},
		"prefix_and_template": {
			"from": "env", "key": "HTTPX_VALUE", "prefix": "/", "output_template": "/{{value}}",
		},
		"suffix_and_template": {
			"from": "env", "key": "HTTPX_VALUE", "suffix": "/", "output_template": "/{{value}}",
		},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseSourceSpec(input); err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}
}

func TestResolverFileDataURLFromEnvironmentPath(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "image.png")
	content := []byte("not-a-real-png-but-binary-safe")
	if err := os.WriteFile(imagePath, content, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	t.Setenv("HTTPX_TEST_IMAGE_PATH", " "+imagePath+" \n")
	r := resolver{reveal: true}

	value, err := r.resolveAny(context.Background(), map[string]any{
		"from": "file_data_url",
		"path": map[string]any{
			"from": "env", "key": "HTTPX_TEST_IMAGE_PATH", "trim": true,
		},
		"max_bytes":           1024,
		"allowed_media_types": []any{"image/png"},
	})
	if err != nil {
		t.Fatalf("resolve file_data_url: %v", err)
	}
	expected := "data:image/png;base64," + base64.StdEncoding.EncodeToString(content)
	if value != expected {
		t.Fatalf("unexpected file_data_url value: %#v", value)
	}
}

func TestResolverFileDataURLRejectsUnsafeInputs(t *testing.T) {
	t.Parallel()

	t.Run("relative path", func(t *testing.T) {
		_, err := encodeFileDataURL("image.png", 1024, []string{"image/png"})
		if err == nil || !errors.Is(err, ErrExecution) {
			t.Fatalf("expected execution error, got %v", err)
		}
	})

	t.Run("disallowed media type", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "image.svg")
		if err := os.WriteFile(path, []byte("<svg/>"), 0o600); err != nil {
			t.Fatalf("write image: %v", err)
		}
		_, err := encodeFileDataURL(path, 1024, []string{"image/png"})
		if err == nil || !errors.Is(err, ErrExecution) {
			t.Fatalf("expected execution error, got %v", err)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "image.png")
		if err := os.WriteFile(path, []byte("too-large"), 0o600); err != nil {
			t.Fatalf("write image: %v", err)
		}
		_, err := encodeFileDataURL(path, 4, []string{"image/png"})
		if err == nil || !errors.Is(err, ErrExecution) {
			t.Fatalf("expected execution error, got %v", err)
		}
	})
}

func TestResolverEnvironmentVariableSemantics(t *testing.T) {
	r := resolver{reveal: true}
	t.Setenv("HTTPX_TEST_EMPTY_ENV", "")

	value, err := r.resolveAny(context.Background(), map[string]any{
		"from": "env",
		"key":  "HTTPX_TEST_EMPTY_ENV",
	})
	if err != nil {
		t.Fatalf("resolve explicitly empty environment variable: %v", err)
	}
	if value != "" {
		t.Fatalf("expected explicitly empty environment variable, got %#v", value)
	}
	value, err = r.resolveAny(context.Background(), map[string]any{
		"from":   "env",
		"key":    "HTTPX_TEST_EMPTY_ENV",
		"prefix": "Bearer ",
	})
	if err != nil {
		t.Fatalf("resolve prefixed empty environment variable: %v", err)
	}
	if value != "Bearer " {
		t.Fatalf("expected prefix-only value, got %#v", value)
	}

	const missingKey = "HTTPX_TEST_ENV_DEFINITELY_MISSING"
	original, existed := os.LookupEnv(missingKey)
	if err := os.Unsetenv(missingKey); err != nil {
		t.Fatalf("unset test environment variable: %v", err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(missingKey, original)
		} else {
			_ = os.Unsetenv(missingKey)
		}
	})

	_, err = r.resolveAny(context.Background(), map[string]any{
		"from":   "env",
		"key":    missingKey,
		"prefix": "Bearer ",
	})
	if err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("expected missing environment variable execution error, got %v", err)
	}
	if !strings.Contains(err.Error(), `environment variable "`+missingKey+`" is not set`) {
		t.Fatalf("unexpected missing environment variable error: %v", err)
	}
}

func TestCompileUsesEnvironmentAcrossRequestFields(t *testing.T) {
	t.Setenv("HTTPX_TEST_ENV_PATH", " /secure ")
	t.Setenv("HTTPX_TEST_ENV_PROXY", "http://proxy.example:8001")
	t.Setenv("HTTPX_TEST_ENV_AUTHORIZATION", " Bearer env-token \n")
	t.Setenv("HTTPX_TEST_ENV_SESSION", "session-env")
	t.Setenv("HTTPX_TEST_ENV_QUERY", "query-env")
	t.Setenv("HTTPX_TEST_ENV_BODY", "body-env")

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.secure]
description = "Use environment values"
method = "POST"
path = { from = "env", key = "HTTPX_TEST_ENV_PATH", trim = true }
proxy = { from = "env", key = "HTTPX_TEST_ENV_PROXY" }
headers = { Authorization = { from = "env", key = "HTTPX_TEST_ENV_AUTHORIZATION", trim = true } }
cookies = { session = { from = "env", key = "HTTPX_TEST_ENV_SESSION" } }
query = { token = { from = "env", key = "HTTPX_TEST_ENV_QUERY" } }
body = { value = { from = "env", key = "HTTPX_TEST_ENV_BODY" } }
`)

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	compiled, _, _, err := NewRuntime(ioDiscard{}, ioDiscard{}).compile(
		commandRequest{
			Command: commandRun,
			Site:    "demo",
			Action:  "secure",
			Options: globalOptions{
				Format:   formatJSON,
				StateDir: t.TempDir(),
			},
		},
		cfg,
		&profileState{Values: map[string]string{}},
	)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if compiled.URL != "https://example.com/secure?token=query-env" {
		t.Fatalf("unexpected URL: %s", compiled.URL)
	}
	if compiled.Proxy != "http://proxy.example:8001" {
		t.Fatalf("unexpected proxy: %q", compiled.Proxy)
	}
	if compiled.Headers["Authorization"] != "Bearer env-token" {
		t.Fatalf("unexpected Authorization header: %#v", compiled.Headers)
	}
	if compiled.Cookies["session"] != "session-env" {
		t.Fatalf("unexpected cookies: %#v", compiled.Cookies)
	}
	body, ok := compiled.Body.(map[string]any)
	if !ok || body["value"] != "body-env" {
		t.Fatalf("unexpected body: %#v", compiled.Body)
	}
}

func TestResolverReportsMissingSecretKey(t *testing.T) {
	t.Parallel()

	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "demo.json"), []byte(`{"cookie":"value"}`), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	r := resolver{
		state:     &profileState{Values: map[string]string{}},
		reveal:    true,
		site:      "demo",
		secretDir: secretDir,
	}

	_, err := r.resolveAny(context.Background(), map[string]any{"from": "secret", "key": "missing"})
	if err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("expected missing secret key execution error, got %v", err)
	}
	if !strings.Contains(err.Error(), `secret key "missing" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompileUsesSiteSecretAcrossRequestFields(t *testing.T) {
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "demo.json"), []byte(`{
		"authorization": "Bearer secret-token",
		"session": "session-cookie",
		"query_token": "query-secret",
		"payload": "body-secret"
	}`), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	configPath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.secure]
description = "Use site secret"
method = "POST"
path = "/secure"
headers = { Authorization = { from = "secret", key = "authorization" } }
cookies = { session = { from = "secret", key = "session" } }
query = { token = { from = "secret", key = "query_token" } }
body = { value = { from = "secret", key = "payload" } }
`)

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "secure",
		Options: globalOptions{
			Format:    formatJSON,
			SecretDir: secretDir,
			StateDir:  t.TempDir(),
		},
	}
	compiled, _, _, err := NewRuntime(ioDiscard{}, ioDiscard{}).compile(
		req,
		cfg,
		&profileState{Values: map[string]string{}},
	)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if compiled.Headers["Authorization"] != "Bearer secret-token" {
		t.Fatalf("unexpected Authorization header: %#v", compiled.Headers)
	}
	if compiled.Cookies["session"] != "session-cookie" {
		t.Fatalf("unexpected cookies: %#v", compiled.Cookies)
	}
	if !strings.Contains(compiled.URL, "token=query-secret") {
		t.Fatalf("unexpected URL: %s", compiled.URL)
	}
	body, ok := compiled.Body.(map[string]any)
	if !ok || body["value"] != "body-secret" {
		t.Fatalf("unexpected body: %#v", compiled.Body)
	}
}

func TestParseArgsSupportsRepeatableParams(t *testing.T) {
	t.Parallel()

	req, err := parseArgs([]string{
		"run", "demo", "list",
		"--param", "user=alice",
		"--param", "page=2",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if req.Options.Params["user"] != "alice" || req.Options.Params["page"] != "2" {
		t.Fatalf("unexpected params: %#v", req.Options.Params)
	}
}

func TestParseArgsSupportsExtractJSON(t *testing.T) {
	t.Parallel()

	req, err := parseArgs([]string{
		"run", "demo", "list",
		"--extract", `{"days":7,"group":["GROUP_A","GROUP_B"]}`,
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if value, ok := req.Options.ExtractInput["days"].(float64); !ok || value != 7 {
		t.Fatalf("unexpected extract input: %#v", req.Options.ExtractInput)
	}
	groups, ok := req.Options.ExtractInput["group"].([]any)
	if !ok || len(groups) != 2 || groups[0] != "GROUP_A" || groups[1] != "GROUP_B" {
		t.Fatalf("unexpected extract input groups: %#v", req.Options.ExtractInput)
	}
}

func TestParseArgsDefaultsByCommand(t *testing.T) {
	t.Parallel()

	runReq, err := parseArgs([]string{"run", "demo", "list"})
	if err != nil {
		t.Fatalf("parseArgs run failed: %v", err)
	}
	if runReq.Options.Format != formatText {
		t.Fatalf("expected run default format text, got %q", runReq.Options.Format)
	}

	inspectReq, err := parseArgs([]string{"inspect", "demo", "list"})
	if err != nil {
		t.Fatalf("parseArgs inspect failed: %v", err)
	}
	if inspectReq.Options.Format != formatJSON {
		t.Fatalf("expected inspect default format json, got %q", inspectReq.Options.Format)
	}

	sitesReq, err := parseArgs([]string{"sites"})
	if err != nil {
		t.Fatalf("parseArgs sites failed: %v", err)
	}
	if sitesReq.Options.Format != formatText {
		t.Fatalf("expected sites default format text, got %q", sitesReq.Options.Format)
	}
}

func TestParseArgsRejectsSiteNameWithPathSeparator(t *testing.T) {
	t.Parallel()

	if _, err := parseArgs([]string{"run", "team/demo", "list"}); err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected site name with path separator to be rejected, got %v", err)
	}
}

func TestDefaultStateDirUsesLocalHTTPXStateWithoutXDG(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_STATE_HOME", "")

	got := defaultStateDir()
	want := filepath.Join(homeDir, ".local", "state", "httpx")
	if got != want {
		t.Fatalf("defaultStateDir mismatch: got %q want %q", got, want)
	}
}

func TestDefaultStateDirUsesXDGStateHomeWhenSet(t *testing.T) {
	xdgStateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdgStateHome)

	got := defaultStateDir()
	want := filepath.Join(xdgStateHome, "httpx")
	if got != want {
		t.Fatalf("defaultStateDir mismatch: got %q want %q", got, want)
	}
}

func TestDefaultSecretDirUsesLocalSecretPathWithoutXDGData(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv(secretHomeEnv, "")
	t.Setenv("XDG_DATA_HOME", "")

	got := defaultSecretDir()
	want := filepath.Join(homeDir, ".local", "secret", "httpx")
	if got != want {
		t.Fatalf("defaultSecretDir mismatch: got %q want %q", got, want)
	}
}

func TestDefaultSecretDirUsesXDGDataHomeWhenSet(t *testing.T) {
	xdgDataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdgDataHome)
	t.Setenv(secretHomeEnv, "")

	got := defaultSecretDir()
	want := filepath.Join(xdgDataHome, "secret", "httpx")
	if got != want {
		t.Fatalf("defaultSecretDir mismatch: got %q want %q", got, want)
	}
}

func TestDefaultSecretDirPrefersXDGSecretHome(t *testing.T) {
	secretHome := t.TempDir()
	t.Setenv(secretHomeEnv, secretHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	got := defaultSecretDir()
	want := filepath.Join(secretHome, "httpx")
	if got != want {
		t.Fatalf("defaultSecretDir mismatch: got %q want %q", got, want)
	}
}

func TestStorageScopeDefaultsToGlobalAndAcceptsChat(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]storageScope{
		"":       scopeGlobal,
		"global": scopeGlobal,
		"chat":   scopeChat,
	} {
		got, err := normalizeStorageScope(raw)
		if err != nil {
			t.Fatalf("normalizeStorageScope(%q) failed: %v", raw, err)
		}
		if got != want {
			t.Fatalf("normalizeStorageScope(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, err := normalizeStorageScope("auto"); err == nil {
		t.Fatal("expected auto scope to be rejected")
	}
	if _, err := normalizeStorageScope("system"); err == nil {
		t.Fatal("expected system scope to be rejected")
	}
}

func TestDynamicSourceScopeDefaultsToGlobal(t *testing.T) {
	t.Parallel()

	globalSpec, ok, err := parseSourceSpec(map[string]any{
		"from": "state",
		"key":  "auth.token",
	})
	if err != nil || !ok {
		t.Fatalf("parse global source failed: ok=%t err=%v", ok, err)
	}
	if globalSpec.Scope != scopeGlobal {
		t.Fatalf("default source scope = %q, want global", globalSpec.Scope)
	}

	chatSpec, ok, err := parseSourceSpec(map[string]any{
		"from":  "secret",
		"scope": "chat",
		"key":   "authorization",
	})
	if err != nil || !ok {
		t.Fatalf("parse chat source failed: ok=%t err=%v", ok, err)
	}
	if chatSpec.Scope != scopeChat {
		t.Fatalf("chat source scope = %q, want chat", chatSpec.Scope)
	}

	if _, _, err := parseSourceSpec(map[string]any{
		"from":  "param",
		"scope": "chat",
		"key":   "user",
	}); err == nil {
		t.Fatal("expected scope on param source to be rejected")
	}
}

func TestChatStorageDirectoriesUseAPChatDir(t *testing.T) {
	chatDir := t.TempDir()
	options := globalOptions{
		SecretDir:  filepath.Join(t.TempDir(), "global-secret"),
		StateDir:   filepath.Join(t.TempDir(), "global-state"),
		ChatDir:    chatDir,
		ChatDirSet: true,
	}

	gotSecret, err := secretDirForScope(options, scopeChat)
	if err != nil {
		t.Fatalf("resolve chat secret dir: %v", err)
	}
	if want := filepath.Join(chatDir, ".secret", "httpx"); gotSecret != want {
		t.Fatalf("chat secret dir = %q, want %q", gotSecret, want)
	}
	gotState, err := stateDirForScope(options, scopeChat)
	if err != nil {
		t.Fatalf("resolve chat state dir: %v", err)
	}
	if want := filepath.Join(chatDir, ".state", "httpx"); gotState != want {
		t.Fatalf("chat state dir = %q, want %q", gotState, want)
	}
}

func TestChatStorageValidatesAPChatDir(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "private-chat-id", "missing")
	filePath := filepath.Join(t.TempDir(), "private-chat-id.txt")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "missing", set: false},
		{name: "empty", value: "", set: true},
		{name: "relative", value: filepath.Join("runtime", "chats", "private-chat-id"), set: true},
		{name: "root", value: string(filepath.Separator), set: true},
		{name: "not found", value: missingPath, set: true},
		{name: "file", value: filePath, set: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := globalOptions{
				StateDir:   t.TempDir(),
				ChatDir:    tt.value,
				ChatDirSet: tt.set,
			}
			_, err := stateDirForScope(options, scopeChat)
			if err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected AP_CHAT_DIR config error, got %v", err)
			}
			if tt.value != "" && filepath.IsAbs(tt.value) && strings.Contains(err.Error(), tt.value) {
				t.Fatalf("chat directory leaked in error: %v", err)
			}
		})
	}
}

func TestConfigRejectsUnsupportedStorageScopes(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"state": `
version = 1
description = "Demo"
base_url = "https://example.com"
state_scope = "auto"

[actions.get]
description = "Get"
path = "/"
`,
		"login_secret": `
version = 1
description = "Demo"
base_url = "https://example.com"

[login]
path = "/login"
secret_scope = "system"
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(writeConfig(t, content))
			if err == nil || !errors.Is(err, ErrConfig) {
				t.Fatalf("expected config error, got %v", err)
			}
		})
	}
}

func TestParseArgsSupportsGlobalFlagsAnywhere(t *testing.T) {
	t.Parallel()

	req, err := parseArgs([]string{
		"inspect",
		"demo",
		"--format", "json",
		"--param", "user=alice",
		"--extract", `{"days":7}`,
		"list",
		"--timeout=5s",
		"--state", "/tmp/httpx-state",
		"--config", "/tmp/httpx-config",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if req.Command != commandInspect || req.Site != "demo" || req.Action != "list" {
		t.Fatalf("unexpected request: %#v", req)
	}
	if req.Options.Format != formatJSON {
		t.Fatalf("expected json format, got %q", req.Options.Format)
	}
	if req.Options.Timeout != 5*time.Second {
		t.Fatalf("expected timeout 5s, got %v", req.Options.Timeout)
	}
	if req.Options.StateDir != "/tmp/httpx-state" {
		t.Fatalf("unexpected state dir: %q", req.Options.StateDir)
	}
	if req.Options.ConfigDir != "/tmp/httpx-config" {
		t.Fatalf("unexpected config dir: %q", req.Options.ConfigDir)
	}
	if !req.Options.ConfigExplicit {
		t.Fatal("expected explicit config flag")
	}
	if req.Options.Params["user"] != "alice" {
		t.Fatalf("unexpected params: %#v", req.Options.Params)
	}
	if req.Options.ExtractInput["days"] != float64(7) {
		t.Fatalf("unexpected extract input: %#v", req.Options.ExtractInput)
	}

	req, err = parseArgs([]string{"action", "demo", "list"})
	if err != nil {
		t.Fatalf("parseArgs action failed: %v", err)
	}
	if req.Command != commandAction || req.Options.Format != formatText {
		t.Fatalf("unexpected action request: %#v", req)
	}
}

func TestParseArgsRejectsInvalidCombinations(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"inspect", "--format", "body", "demo", "list"},
		{"run", "--format", "body", "demo", "list"},
		{"login", "--format", "body", "demo"},
		{"run", "demo", "list", "--extract", `[]`},
		{"run", "demo", "list", "--extract", `{"days":`},
		{"run", "demo", "list", "--extract", `{"days":7}`, "--extract", `{"group":"GROUP_A"}`},
		{"sites", "--format", "body"},
		{"sites", "--param", "user=alice"},
		{"sites", "--extract", `{"days":7}`},
		{"action", "demo", "list", "--extract", `{"days":7}`},
		{"login", "demo", "--extract", `{"days":7}`},
		{"state", "--timeout", "1s", "demo"},
		{"--state-dir", "/tmp/httpx-state", "sites"},
		{"demo", "list"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("expected parse error for args %#v", args)
		}
	}
}

func TestParseArgsRejectsBodyFormatWithTextHint(t *testing.T) {
	t.Parallel()

	_, err := parseArgs([]string{"run", "--format", "body", "demo", "list"})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), `use "text" instead`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsReservedSiteName(t *testing.T) {
	t.Parallel()

	if _, err := parseArgs([]string{"run", "version", "list"}); err == nil {
		t.Fatal("expected reserved site parse error")
	}
}

func TestResolveConfigPathRejectsFile(t *testing.T) {
	t.Parallel()

	filePath := writeConfig(t, `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.me]
description = "Profile"
path = "/me"
`)
	_, err := resolveConfigPath(filePath, "demo")
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config dir error, got %v", err)
	}
}

func TestDefaultConfigDirUsesHomeConfigPath(t *testing.T) {
	t.Setenv("HOME", "/root")

	if got := defaultConfigDir(); got != "/root/.config/httpx" {
		t.Fatalf("unexpected default config dir: %q", got)
	}
}

func TestConfigSearchUsesSystemConfigWithoutAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dirs, err := configSearchDirs(defaultConfigDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, ".config", "httpx")}
	if !reflect.DeepEqual(dirs, want) {
		t.Fatalf("config directories = %#v, want %#v", dirs, want)
	}
}

func TestAgentConfigOverlayUsesAgentSiteThenSystemFallback(t *testing.T) {
	home := t.TempDir()
	agentConfigHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, agentConfigHome)

	writeSite := func(configHome, site, description string) string {
		t.Helper()
		dir := filepath.Join(configHome, "httpx")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, site+".toml")
		raw := "version = 1\ndescription = \"" + description + "\"\nbase_url = \"https://example.com\"\n\n[actions.get]\ndescription = \"Get\"\npath = \"/\"\n"
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	systemConfigHome := filepath.Join(home, ".config")
	systemOnlyPath := writeSite(systemConfigHome, "system-only", "system")
	writeSite(systemConfigHome, "shared", "system-shared")
	agentSharedPath := writeSite(agentConfigHome, "shared", "agent-shared")

	configDir := defaultConfigDir()
	path, err := resolveConfigPathWithFallback(configDir, true, "system-only")
	if err != nil {
		t.Fatal(err)
	}
	if path != systemOnlyPath {
		t.Fatalf("system fallback path = %q, want %q", path, systemOnlyPath)
	}
	path, err = resolveConfigPathWithFallback(configDir, true, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if path != agentSharedPath {
		t.Fatalf("agent override path = %q, want %q", path, agentSharedPath)
	}

	sites, err := listConfigSitesWithFallback(configDir, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(sites, ","), "shared,system-only"; got != want {
		t.Fatalf("sites = %q, want %q", got, want)
	}
	cfg, loadedPath, err := loadSiteConfigWithFallback(configDir, true, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if loadedPath != agentSharedPath || cfg.Description != "agent-shared" {
		t.Fatalf("unexpected agent-loaded config path=%q config=%#v", loadedPath, cfg)
	}
}

func TestAgentConfigOverlayDoesNotFallbackAfterAgentConfigIsSelected(t *testing.T) {
	home := t.TempDir()
	agentConfigHome := t.TempDir()
	systemConfigHome := filepath.Join(home, ".config")
	t.Setenv("HOME", home)
	t.Setenv(agentConfigHomeEnv, agentConfigHome)
	for _, configHome := range []string{agentConfigHome, systemConfigHome} {
		if err := os.MkdirAll(filepath.Join(configHome, "httpx"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentConfigHome, "httpx", "shared.toml"), []byte("version = 1\n[actions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemConfigHome, "httpx", "shared.toml"), []byte("version = 1\nbase_url = \"https://example.com\"\n[actions.get]\npath = \"/\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSiteConfigWithFallback(defaultConfigDir(), true, "shared"); err == nil || !strings.Contains(err.Error(), filepath.Join(agentConfigHome, "httpx", "shared.toml")) {
		t.Fatalf("expected agent config parse error, got %v", err)
	}
}

func TestExplicitConfigDisablesAgentConfigFallback(t *testing.T) {
	agentConfigHome := t.TempDir()
	explicitDir := t.TempDir()
	t.Setenv(agentConfigHomeEnv, agentConfigHome)
	if err := os.MkdirAll(filepath.Join(agentConfigHome, "httpx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentConfigHome, "httpx", "shared.toml"), []byte("version = 1\nbase_url = \"https://example.com\"\n[actions.get]\npath = \"/\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := resolveConfigPathWithFallback(explicitDir, false, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := path, filepath.Join(explicitDir, "shared.toml"); got != want {
		t.Fatalf("explicit config path = %q, want %q", got, want)
	}
}

func TestDefaultStateDirUsesHomeLocalHTTPXState(t *testing.T) {
	t.Setenv("HOME", "/root")
	t.Setenv("XDG_STATE_HOME", "")

	if got := defaultStateDir(); got != "/root/.local/state/httpx" {
		t.Fatalf("unexpected default state dir: %q", got)
	}
}

func TestDefaultSecretDirUsesHomeLocalSecretHTTPXPath(t *testing.T) {
	t.Setenv("HOME", "/root")
	t.Setenv(secretHomeEnv, "")
	t.Setenv("XDG_DATA_HOME", "")

	if got := defaultSecretDir(); got != "/root/.local/secret/httpx" {
		t.Fatalf("unexpected default secret dir: %q", got)
	}
}

func TestCompileSupportsParamAndLiteralSources(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.search]
description = "Search site"
path = "/search"
headers = { X-Mode = { from = "literal", value = "agent" } }
query = { q = { from = "param", key = "query" }, page = { from = "param", key = "page", default = 1 } }
body = { keyword = { from = "param", key = "query" }, source = { from = "literal", value = "cli" }, id = { from = "param", key = "id", default = 9062 } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "search",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
			Params: map[string]any{
				"query": "golang",
				"id":    "8001",
			},
		},
	}

	compiled, _, _, err := rt.compile(req, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if compiled.Headers["X-Mode"] != "agent" {
		t.Fatalf("unexpected literal header: %#v", compiled.Headers)
	}
	if !strings.Contains(compiled.URL, "q=golang") || !strings.Contains(compiled.URL, "page=1") {
		t.Fatalf("unexpected param URL: %s", compiled.URL)
	}
	body, ok := compiled.Body.(map[string]any)
	if !ok {
		t.Fatalf("expected map body, got %#v", compiled.Body)
	}
	if body["keyword"] != "golang" || body["source"] != "cli" {
		t.Fatalf("unexpected body: %#v", compiled.Body)
	}
	if body["id"] != int64(8001) {
		t.Fatalf("expected numeric id, got %#v", compiled.Body)
	}
}

func TestCompileRejectsInvalidParamTypeForDefaultSample(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.search]
description = "Search site"
path = "/search"
body = { id = { from = "param", key = "id", default = 9062 } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "search",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
			Params: map[string]any{
				"id": "not-a-number",
			},
		},
	}

	_, _, _, err = rt.compile(req, cfg, &profileState{Values: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), `parameter "id"`) {
		t.Fatalf("expected typed param error, got %v", err)
	}
}

func TestCompileSupportsCookies(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
cookies = { locale = "zh-CN" }

[actions.me]
description = "Profile"
path = "/me"
cookies = { session = { from = "state", key = "auth.session" }, mode = { from = "literal", value = "agent" } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "me",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
		},
	}

	compiled, _, _, err := rt.compile(req, cfg, &profileState{Values: map[string]string{"auth.session": "abc123"}})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if compiled.Cookies["locale"] != "zh-CN" || compiled.Cookies["session"] != "abc123" || compiled.Cookies["mode"] != "agent" {
		t.Fatalf("unexpected compiled cookies: %#v", compiled.Cookies)
	}
}

func TestCompileSupportsSiteAndActionProxy(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
proxy = "http://127.0.0.1:8001"

[actions.default]
description = "Default path"
path = "/default"

[actions.override]
description = "Override path"
path = "/override"
proxy = "http://127.0.0.1:8002"
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})

	defaultReq := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "default",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
		},
	}
	defaultCompiled, _, _, err := rt.compile(defaultReq, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile default failed: %v", err)
	}
	if defaultCompiled.Proxy != "http://127.0.0.1:8001" {
		t.Fatalf("unexpected default proxy: %#v", defaultCompiled.Proxy)
	}

	overrideReq := defaultReq
	overrideReq.Action = "override"
	overrideCompiled, _, _, err := rt.compile(overrideReq, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile override failed: %v", err)
	}
	if overrideCompiled.Proxy != "http://127.0.0.1:8002" {
		t.Fatalf("unexpected override proxy: %#v", overrideCompiled.Proxy)
	}
}

func TestCompileSupportsDynamicProxySource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
proxy = { from = "param", key = "proxy" }

[actions.default]
description = "Default path"
path = "/default"
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "default",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
			Params: map[string]any{
				"proxy": "http://127.0.0.1:8001",
			},
		},
	}

	compiled, _, _, err := rt.compile(req, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if compiled.Proxy != "http://127.0.0.1:8001" {
		t.Fatalf("unexpected dynamic proxy: %#v", compiled.Proxy)
	}
}

func TestCompileRejectsInvalidProxyURL(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
proxy = "127.0.0.1:8001"

[actions.default]
description = "Default path"
path = "/default"
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "default",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
		},
	}

	_, _, _, err = rt.compile(req, cfg, &profileState{Values: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("expected proxy error, got %v", err)
	}
}

func TestCompileFormEncodesNestedObjectsAsJSONString(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.submit]
description = "Submit login"
method = "POST"
path = "/login"
form = { data = { user = "alice", secret = "secret", kind = "100", mode = "2" } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Action:  "submit",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
		},
	}

	compiled, _, _, err := rt.compile(req, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	requestBody, contentLength, err := compiled.OpenBody()
	if err != nil {
		t.Fatalf("open compiled body failed: %v", err)
	}
	defer requestBody.Close()
	encodedBytes, err := io.ReadAll(requestBody)
	if err != nil {
		t.Fatalf("read compiled body failed: %v", err)
	}
	if int64(len(encodedBytes)) != contentLength {
		t.Fatalf("compiled body length mismatch: got %d, want %d", len(encodedBytes), contentLength)
	}
	encodedBody := string(encodedBytes)
	if !strings.HasPrefix(encodedBody, "data=") {
		t.Fatalf("expected form field named data, got %q", encodedBody)
	}
	if !strings.Contains(encodedBody, "%22user%22%3A%22alice%22") {
		t.Fatalf("expected nested object to be JSON-encoded in form body, got %q", encodedBody)
	}

	body, ok := compiled.Body.(map[string]string)
	if !ok {
		t.Fatalf("expected inspect body to be string form map, got %#v", compiled.Body)
	}
	if !strings.Contains(body["data"], `"user":"alice"`) {
		t.Fatalf("expected nested object stringified in inspect body, got %#v", body)
	}
}

func TestResolverReportsMissingParamAndShellTimeout(t *testing.T) {
	t.Parallel()

	r := resolver{
		state:  &profileState{Values: map[string]string{}},
		reveal: true,
	}

	if _, err := r.resolveAny(context.Background(), map[string]any{"from": "param", "key": "missing"}); err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("expected missing param execution error, got %v", err)
	}

	_, err := r.resolveAny(context.Background(), map[string]any{
		"from":       "shell",
		"cmd":        "sleep 1",
		"timeout_ms": int64(10),
	})
	if err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("expected shell timeout execution error, got %v", err)
	}
}

func TestResolverReportsMissingParamBeforeInspectRedaction(t *testing.T) {
	t.Parallel()

	r := resolver{
		state:  &profileState{Values: map[string]string{}},
		reveal: false,
	}
	if _, err := r.resolveAny(context.Background(), map[string]any{
		"from": "param",
		"key":  "request_id",
	}); err == nil || !errors.Is(err, ErrExecution) || !strings.Contains(err.Error(), `parameter "request_id" not provided`) {
		t.Fatalf("expected actionable missing param error during inspect, got %v", err)
	}
	value, err := r.resolveAny(context.Background(), map[string]any{
		"from":    "param",
		"key":     "arguments_json",
		"default": "{}",
	})
	if err != nil || value != "{}" {
		t.Fatalf("expected safe param default during inspect, got value=%#v err=%v", value, err)
	}
}

func TestActionExamplesStartWithAllRequiredParams(t *testing.T) {
	t.Parallel()

	examples := buildActionExamples(actionDetail{
		Site: "online-pptx-bridge",
		Name: "inspect",
		Params: []actionInputSpec{
			{Name: "arguments_json", Required: false, Example: "{}"},
			{Name: "request_id", Required: true, Example: "tool-inspect-1"},
		},
	})
	if len(examples) < 2 {
		t.Fatalf("expected required-only and optional examples, got %#v", examples)
	}
	expected := "httpx run online-pptx-bridge inspect --param request_id=tool-inspect-1"
	if examples[0] != expected {
		t.Fatalf("expected first runnable example %q, got %#v", expected, examples)
	}
	for _, example := range examples {
		if example == "httpx run online-pptx-bridge inspect" {
			t.Fatalf("examples must not contain a command missing request_id: %#v", examples)
		}
	}
}

func TestRunUsesDefaultSiteConfigPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(server.Close)

	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".config", "httpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "demo.toml"), []byte(strings.TrimSpace(fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ping]
description = "Ping site"
path = "/ping"
`, server.URL))+"\n"), 0o600); err != nil {
		t.Fatalf("write site config: %v", err)
	}

	stdout, stderr, exitCode := runMain(t, []string{"run", "demo", "ping"})
	if exitCode != ExitSuccess {
		t.Fatalf("run failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != "pong" {
		t.Fatalf("unexpected body output: %q", stdout)
	}
}

func TestRunSendsPrefixedEnvironmentAuthorization(t *testing.T) {
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)
	t.Setenv("HTTPX_TEST_ACCESS_TOKEN", " fake-jwt \n")

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[headers]
Authorization = { from = "env", key = "HTTPX_TEST_ACCESS_TOKEN", trim = true, prefix = "Bearer " }

[actions.secure]
description = "Call authenticated endpoint"
path = "/secure"
expect_status = 200
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "run", "demo", "secure"})
	if exitCode != ExitSuccess {
		t.Fatalf("run failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if gotAuthorization != "Bearer fake-jwt" {
		t.Fatalf("unexpected Authorization header: %q", gotAuthorization)
	}
}

func TestCompileRejectsRegexExtractorGroupOutOfRange(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.page]
description = "Read page"
path = "/page"
extract_type = "regex"
extract_pattern = "token=([A-Za-z0-9_-]+)"
extract_group = 2
`, server.URL))

	_, err := loadConfig(configPath)
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestLoginPersistsStateAndRunReusesCookieAndToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse form: %v", err)
			}
			if r.FormValue("username") != "alice" || r.FormValue("password") != "secret" {
				http.Error(w, "bad credentials", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"token-123"}`))
		case "/data":
			if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
				http.Error(w, "missing auth header", http.StatusUnauthorized)
				return
			}
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "abc123" {
				http.Error(w, "missing session cookie", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":42}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	writeSecret(t, "demo", "alice", "secret")
	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
[login]
path = "/login"
save = { "session.auth_header" = "\"Bearer \" + .body.token" }

[actions.data]
description = "Load data"
path = "/data"
headers = { Authorization = { from = "state", key = "session.auth_header" } }
extract_type = "jq"
extract_expr = ".body.value"
`, server.URL))

	stateDir := t.TempDir()
	loginStdout, loginStderr, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "login", "demo"})
	if exitCode != ExitSuccess {
		t.Fatalf("login failed: exit=%d stderr=%s stdout=%s", exitCode, loginStderr, loginStdout)
	}

	state, err := loadState(stateDir, "demo")
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.Values["session.auth_header"] != "Bearer token-123" {
		t.Fatalf("expected saved token, got %#v", state.Values)
	}
	if len(state.Cookies) == 0 {
		t.Fatalf("expected persisted cookies")
	}
	if state.LastLogin == "" {
		t.Fatalf("expected last_login to be set")
	}

	runStdout, runStderr, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "run", "demo", "data"})
	if exitCode != ExitSuccess {
		t.Fatalf("run failed: exit=%d stderr=%s stdout=%s", exitCode, runStderr, runStdout)
	}

	var runOutput map[string]any
	if err := json.Unmarshal([]byte(runStdout), &runOutput); err != nil {
		t.Fatalf("unmarshal run output map: %v", err)
	}

	var env envelope
	if err := json.Unmarshal([]byte(runStdout), &env); err != nil {
		t.Fatalf("unmarshal run output: %v", err)
	}
	if !env.OK || env.Status != 200 {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if env.Action != "data" {
		t.Fatalf("unexpected action: %#v", env.Action)
	}
	if env.Site != "demo" {
		t.Fatalf("unexpected site: %#v", env.Site)
	}
	if value, ok := env.Body.(float64); !ok || value != 42 {
		t.Fatalf("unexpected body: %#v", env.Body)
	}
	if _, exists := runOutput["extract"]; exists {
		t.Fatalf("expected no extract field in runtime output, got %#v", runOutput)
	}
}

func TestLoginSupportsJSONBodyAndBasicAuth(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"token-456"}`))
	}))
	t.Cleanup(server.Close)

	writeSecret(t, "demo", "alice", "secret")
	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[login]
path = "/login"
body_format = "json"
basic_auth = true
save = { "session.auth_header" = "\"Bearer \" + .body.token" }
`, server.URL))

	stateDir := t.TempDir()
	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "login", "demo"})
	if exitCode != ExitSuccess {
		t.Fatalf("login failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	if gotAuth != "Basic YWxpY2U6c2VjcmV0" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
	if gotBody["username"] != "alice" || gotBody["password"] != "secret" {
		t.Fatalf("unexpected login body: %#v", gotBody)
	}

	state, err := loadState(stateDir, "demo")
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.Values["session.auth_header"] != "Bearer token-456" {
		t.Fatalf("unexpected saved state: %#v", state.Values)
	}
}

func TestLoginFailsClearlyWithoutBuiltInLoginConfig(t *testing.T) {
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.data]
description = "Load data"
path = "/data"
`)
	writeSecret(t, "demo", "alice", "secret")

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--format", "json", "login", "demo"})
	if exitCode != ExitConfig {
		t.Fatalf("expected config failure, got exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stdout, "external Python script") {
		t.Fatalf("expected guidance for external Python script, stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestLoginDoesNotPersistStateOnFailure(t *testing.T) {
	stateDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	writeSecret(t, "demo", "alice", "secret")
	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
[login]
path = "/login"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "login", "demo"})
	if exitCode == ExitSuccess {
		t.Fatalf("expected login failure, got stdout=%s", stdout)
	}
	if !strings.Contains(stderr, "unexpected status 403") && !strings.Contains(stdout, "unexpected status 403") {
		t.Fatalf("expected status failure, stderr=%q stdout=%q", stderr, stdout)
	}

	summary, err := summarizeState(stateDir, "demo")
	if err != nil {
		t.Fatalf("summarizeState failed: %v", err)
	}
	if summary.Exists {
		t.Fatalf("expected no persisted state, got %#v", summary)
	}
}

func TestRunSupportsExplicitCookiesFromState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"session_id":"session-123"}`))
		case "/me":
			cookie, err := r.Cookie("session.id")
			if err != nil || cookie.Value != "session-123" {
				http.Error(w, "missing session.id", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	writeSecret(t, "demo", "alice", "secret")
	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
[login]
path = "/login"
save = { "session.id" = ".body.session_id" }

[actions.me]
description = "Load profile"
path = "/me"
cookies = { "session.id" = { from = "state", key = "session.id" } }
extract_type = "jq"
extract_expr = ".body.ok"
`, server.URL))

	stateDir := t.TempDir()
	_, _, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "login", "demo"})
	if exitCode != ExitSuccess {
		t.Fatalf("login failed with exit=%d", exitCode)
	}

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "run", "demo", "me"})
	if exitCode != ExitSuccess {
		t.Fatalf("run failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var runOutput map[string]any
	if err := json.Unmarshal([]byte(stdout), &runOutput); err != nil {
		t.Fatalf("unmarshal run output map: %v", err)
	}

	var env envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("unmarshal run output: %v", err)
	}
	if ok, _ := env.Body.(bool); !ok {
		t.Fatalf("unexpected body: %#v", env.Body)
	}
	if _, exists := runOutput["extract"]; exists {
		t.Fatalf("expected no extract field in runtime output, got %#v", runOutput)
	}
}

func TestInspectRedactsDynamicValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.secret]
description = "Secret request"
method = "POST"
path = "/secret"
proxy = "http://alice:secret@proxy.example:8001"
headers = { Authorization = { from = "secret", key = "authorization" } }
query = { token = { from = "secret", key = "token" } }
body = { nested = { from = "secret", key = "payload" } }
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "secret"})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var compiled compiledRequest
	if err := json.Unmarshal([]byte(stdout), &compiled); err != nil {
		t.Fatalf("unmarshal inspect output: %v", err)
	}
	if compiled.Headers["Authorization"] != redactedValue {
		t.Fatalf("expected redacted auth header, got %#v", compiled.Headers)
	}
	if compiled.Proxy != "http://%2A%2A%2A:%2A%2A%2A@proxy.example:8001" {
		t.Fatalf("expected redacted proxy, got %#v", compiled.Proxy)
	}
	if !strings.Contains(compiled.URL, "token=%2A%2A%2A") {
		t.Fatalf("expected redacted query token in URL, got %s", compiled.URL)
	}
	body, ok := compiled.Body.(map[string]any)
	if !ok || body["nested"] != redactedValue {
		t.Fatalf("expected redacted body, got %#v", compiled.Body)
	}
}

func TestInspectRedactsEnvironmentWithoutReadingIt(t *testing.T) {
	const envKey = "HTTPX_TEST_INSPECT_AUTHORIZATION"
	original, existed := os.LookupEnv(envKey)
	if err := os.Unsetenv(envKey); err != nil {
		t.Fatalf("unset test environment variable: %v", err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(envKey, original)
		} else {
			_ = os.Unsetenv(envKey)
		}
	})

	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.secret]
description = "Environment-backed request"
path = "/secret"
headers = { Authorization = { from = "env", key = "HTTPX_TEST_INSPECT_AUTHORIZATION", trim = true, prefix = "Bearer " } }
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "secret"})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect failed without environment variable: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	var redacted compiledRequest
	if err := json.Unmarshal([]byte(stdout), &redacted); err != nil {
		t.Fatalf("unmarshal redacted inspect output: %v", err)
	}
	if redacted.Headers["Authorization"] != redactedValue {
		t.Fatalf("expected redacted environment header, got %#v", redacted.Headers)
	}

	t.Setenv(envKey, " fake-jwt \n")
	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "inspect", "--reveal", "demo", "secret"})
	if exitCode != ExitSuccess {
		t.Fatalf("revealed inspect failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	var revealed compiledRequest
	if err := json.Unmarshal([]byte(stdout), &revealed); err != nil {
		t.Fatalf("unmarshal revealed inspect output: %v", err)
	}
	if revealed.Headers["Authorization"] != "Bearer fake-jwt" {
		t.Fatalf("unexpected revealed environment header: %#v", revealed.Headers)
	}
}

func TestInspectRedactsDynamicPath(t *testing.T) {
	stateDir := t.TempDir()
	if err := saveState(stateDir, "demo", &profileState{
		Values: map[string]string{
			"login.next_url": "https://example.com/finish?ticket=abc",
		},
	}); err != nil {
		t.Fatalf("saveState failed: %v", err)
	}

	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo site"
base_url = "https://example.com"

[actions.finish]
description = "Finish login"
path = { from = "state", key = "login.next_url" }
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "inspect", "demo", "finish"})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var compiled compiledRequest
	if err := json.Unmarshal([]byte(stdout), &compiled); err != nil {
		t.Fatalf("unmarshal inspect output: %v", err)
	}
	if compiled.URL != redactedValue {
		t.Fatalf("expected redacted URL, got %#v", compiled.URL)
	}
}

func TestInspectRevealShowsProxyCredentials(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.secret]
description = "Secret request"
path = "/secret"
proxy = "http://alice:secret@proxy.example:8001"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "--reveal", "demo", "secret"})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var compiled compiledRequest
	if err := json.Unmarshal([]byte(stdout), &compiled); err != nil {
		t.Fatalf("unmarshal inspect output: %v", err)
	}
	if compiled.Proxy != "http://alice:secret@proxy.example:8001" {
		t.Fatalf("unexpected revealed proxy: %#v", compiled.Proxy)
	}
}

func TestBuildTransportDoesNotUseEnvironmentProxyByDefault(t *testing.T) {
	transport, err := buildTransport("")
	if err != nil {
		t.Fatalf("buildTransport failed: %v", err)
	}
	if transport.Proxy != nil {
		t.Fatal("expected buildTransport to use direct transport by default")
	}
}

func TestRuntimeUsesExplicitProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(origin.Close)

	var proxiedURL string
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	baseTransport.Proxy = nil
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxiedURL = r.URL.String()

		outbound := r.Clone(r.Context())
		outbound.RequestURI = ""
		resp, err := baseTransport.RoundTrip(outbound)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
proxy = %q

[actions.ping]
description = "Ping site"
path = "/ping"
`, origin.URL, proxy.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "text", "run", "demo", "ping"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != "pong" {
		t.Fatalf("unexpected body output: %q", stdout)
	}
	if proxiedURL != origin.URL+"/ping" {
		t.Fatalf("expected request to traverse proxy, got %q", proxiedURL)
	}
}

func TestAssertionFailureReturnsStructuredEnvelope(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.fail]
description = "Fail action"
path = "/fail"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "json", "run", "demo", "fail"})
	if exitCode != ExitAssertion {
		t.Fatalf("expected assertion exit code, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var env envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if env.Status != http.StatusTeapot {
		t.Fatalf("expected status in envelope, got %#v", env)
	}
	if env.Error == nil || env.Error.Code != "assertion_error" {
		t.Fatalf("expected assertion error envelope, got %#v", env.Error)
	}
	if env.Site != "demo" {
		t.Fatalf("expected site in envelope, got %#v", env.Site)
	}
}

func TestBodyFormatOutputsRawBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ping]
description = "Ping site"
path = "/ping"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "text", "run", "demo", "ping"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != "pong" {
		t.Fatalf("unexpected body output: %q", stdout)
	}
}

func TestJSONFormatOutputsRawBodyWithoutExtractor(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"pong"}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ping]
description = "Ping site"
path = "/ping"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "json", "run", "demo", "ping"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var runOutput map[string]any
	if err := json.Unmarshal([]byte(stdout), &runOutput); err != nil {
		t.Fatalf("unmarshal run output map: %v", err)
	}
	body, ok := runOutput["body"].(map[string]any)
	if !ok || body["message"] != "pong" {
		t.Fatalf("unexpected body output: %#v", runOutput)
	}
	if _, exists := runOutput["extract"]; exists {
		t.Fatalf("expected no extract field in runtime output, got %#v", runOutput)
	}
}

func TestBodyFormatOutputsJQExtractorResult(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"title":"demo","owner":"alice","noise":"skip"}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.repo]
description = "Load repo"
path = "/repo"
extract_type = "jq"
extract_expr = ".body | {id, title, owner}"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "text", "run", "demo", "repo"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != `{"id":7,"owner":"alice","title":"demo"}` {
		t.Fatalf("unexpected extractor output: %q", stdout)
	}
}

func TestBodyFormatOutputsJQExtractorUsingExtractInput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":1,"age_days":2},{"id":2,"age_days":9},{"id":3,"age_days":5}]}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.summary]
description = "Load summary"
path = "/summary"
extract_type = "jq"
extract_expr = ".extract as $extract | .body.items | map(select(.age_days <= ($extract.days // 0))) | map(.id)"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", t.TempDir(),
		"--format", "text",
		"run", "demo", "summary",
		"--extract", `{"days":7}`,
	})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != `[1,3]` {
		t.Fatalf("unexpected extractor output: %q", stdout)
	}
}

func TestBodyFormatJQExtractorTreatsNullDataAsEmptyWhenCodeIsZero(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":null,"msg":"ok"}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.summary]
description = "Load summary"
path = "/summary"
extract_type = "jq"
extract_expr = '''
def fail_payload($payload; $reason):
  (
    $reason
    + "; code="
    + (($payload.code // "null") | tostring)
    + "; msg="
    + (($payload.msg // "<none>") | tostring)
    + "; data_type="
    + (
        if ($payload | type) == "object" and ($payload | has("data")) then
          ($payload.data | type)
        else
          "<missing>"
        end
      )
  ) | halt_error(1);
.body as $payload
| (
    if ($payload | type) != "object" then
      fail_payload($payload; "payload body is not an object")
    elif (($payload.code // 0) != 0) then
      fail_payload($payload; "upstream returned non-zero code")
    elif ($payload.data == null) then
      []
    elif (($payload.data | type) == "array") then
      $payload.data
    else
      fail_payload($payload; "payload data is not an array")
    end
  ) as $source_items
| .extract as $extract
| $source_items
| map(select(($extract.days // null) == null or .age_days <= ($extract.days | tonumber)))
| map(.id)
'''
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", t.TempDir(),
		"--format", "text",
		"run", "demo", "summary",
		"--extract", `{"days":30}`,
	})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != `[]` {
		t.Fatalf("unexpected extractor output: %q", stdout)
	}
}

func TestBodyFormatJQExtractorFailsClearlyWhenCodeIsNonZero(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":123,"data":null,"msg":"upstream error"}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.summary]
description = "Load summary"
path = "/summary"
extract_type = "jq"
extract_expr = '''
def fail_payload($payload; $reason):
  (
    $reason
    + "; code="
    + (($payload.code // "null") | tostring)
    + "; msg="
    + (($payload.msg // "<none>") | tostring)
    + "; data_type="
    + (
        if ($payload | type) == "object" and ($payload | has("data")) then
          ($payload.data | type)
        else
          "<missing>"
        end
      )
  ) | halt_error(1);
.body as $payload
| (
    if ($payload | type) != "object" then
      fail_payload($payload; "payload body is not an object")
    elif (($payload.code // 0) != 0) then
      fail_payload($payload; "upstream returned non-zero code")
    elif ($payload.data == null) then
      []
    elif (($payload.data | type) == "array") then
      $payload.data
    else
      fail_payload($payload; "payload data is not an array")
    end
  ) as $source_items
| $source_items
'''
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", t.TempDir(),
		"--format", "text",
		"run", "demo", "summary",
	})
	if exitCode != ExitAssertion {
		t.Fatalf("expected assertion failure, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stderr, `upstream returned non-zero code; code=123; msg=upstream error; data_type=null`) {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestBodyFormatOutputsRegexExtractorMatches(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("id=12 id=34 id=56"))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ids]
description = "Load ids"
path = "/ids"
extract_type = "regex"
extract_pattern = "id=([0-9]+)"
extract_group = 1
extract_all = true
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "text", "run", "demo", "ids"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != `["12","34","56"]` {
		t.Fatalf("unexpected regex extractor output: %q", stdout)
	}
}

func TestBodyFormatOutputsRegexExtractorUsingExtractInput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("group=GROUP_A group=GROUP_B"))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ids]
description = "Load ids"
path = "/ids"
extract_type = "regex"
extract_pattern = "group=({{extract.group}})"
extract_group = 1
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", t.TempDir(),
		"--format", "text",
		"run", "demo", "ids",
		"--extract", `{"group":"GROUP_B"}`,
	})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != `GROUP_B` {
		t.Fatalf("unexpected regex extractor output: %q", stdout)
	}
}

func TestJSONFormatReplacesBodyWithRegexExtractorResult(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("group=GROUP_A group=GROUP_B"))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ids]
description = "Load ids"
path = "/ids"
extract_type = "regex"
extract_pattern = "group=({{extract.group}})"
extract_group = 1
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", t.TempDir(),
		"--format", "json",
		"run", "demo", "ids",
		"--extract", `{"group":"GROUP_B"}`,
	})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var runOutput map[string]any
	if err := json.Unmarshal([]byte(stdout), &runOutput); err != nil {
		t.Fatalf("unmarshal run output map: %v", err)
	}
	if runOutput["body"] != "GROUP_B" {
		t.Fatalf("unexpected body output: %#v", runOutput)
	}
	if _, exists := runOutput["extract"]; exists {
		t.Fatalf("expected no extract field in runtime output, got %#v", runOutput)
	}
}

func TestBodyFormatRegexExtractorFailsWhenExtractInputMissing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("group=GROUP_A"))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ids]
description = "Load ids"
path = "/ids"
extract_type = "regex"
extract_pattern = "group=({{extract.group}})"
extract_group = 1
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", t.TempDir(),
		"--format", "text",
		"run", "demo", "ids",
	})
	if exitCode != ExitAssertion {
		t.Fatalf("expected assertion failure, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stderr, `extract input "group" not provided`) {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestBodyFormatOutputsEmptyWhenExtractorFindsNoMatches(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.ids]
description = "Load ids"
path = "/ids"
extract_type = "jq"
extract_expr = ".body.items[]?.id"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "text", "run", "demo", "ids"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != "" {
		t.Fatalf("expected empty extractor output, got %q", stdout)
	}
}

func TestInspectOutputsStructuredExtractor(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q

[actions.secret]
description = "Secret request"
path = "/secret"
params = [{ name = "id", type = "string", required = true, description = "Lookup id", example = "42" }]
extracts = [{ name = "group", type = "string", description = "Exact group match", example = "GROUP_A" }]
extract_type = "regex"
extract_pattern = "token=([A-Za-z0-9_-]+)"
extract_group = 1
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "secret", "--extract", `{"group":"GROUP_A"}`})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var compiled compiledRequest
	if err := json.Unmarshal([]byte(stdout), &compiled); err != nil {
		t.Fatalf("unmarshal inspect output: %v", err)
	}
	if compiled.Extractor == nil {
		t.Fatalf("expected extractor in inspect output")
	}
	if compiled.Extractor.Type != "regex" || compiled.Extractor.Pattern != "token=([A-Za-z0-9_-]+)" || compiled.Extractor.Group != 1 {
		t.Fatalf("unexpected extractor: %#v", compiled.Extractor)
	}
	if compiled.ExtractInput["group"] != "GROUP_A" {
		t.Fatalf("unexpected extract input: %#v", compiled.ExtractInput)
	}
	if len(compiled.Params) != 1 || compiled.Params[0].Name != "id" {
		t.Fatalf("unexpected params metadata: %#v", compiled.Params)
	}
	if len(compiled.Extracts) != 1 || compiled.Extracts[0].Name != "group" {
		t.Fatalf("unexpected extracts metadata: %#v", compiled.Extracts)
	}
}

func TestDiscoveryCommandsExposeUsableActionMetadata(t *testing.T) {
	configDir := t.TempDir()
	stateDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(configDir, "alpha.toml"), []byte(strings.TrimSpace(`
version = 1
description = "Sample site A"
base_url = "https://alpha.example.com"
[login]
path = "/login"

[actions.profile]
description = "Load profile"
path = "/me"
params = [{ name = "user_id", type = "string", required = true, description = "Profile id", example = "42" }]
extracts = [{ name = "group", type = "string", description = "Filter group", example = "GROUP_A" }]
`)+"\n"), 0o600); err != nil {
		t.Fatalf("write alpha config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "beta.toml"), []byte(strings.TrimSpace(`
version = 1
description = "Sample site B"
base_url = "https://beta.example.com"

[actions.search]
description = "Search beta"
path = "/search"
`)+"\n"), 0o600); err != nil {
		t.Fatalf("write beta config: %v", err)
	}

	alphaState := &profileState{
		Values:    map[string]string{"auth.token": "secret-value"},
		Cookies:   []storedCookie{{Name: "session", Value: "abc"}},
		LastLogin: "2026-03-28T10:00:00Z",
	}
	if err := saveState(stateDir, "alpha", alphaState); err != nil {
		t.Fatalf("save state: %v", err)
	}

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", stateDir, "sites"})
	if exitCode != ExitSuccess {
		t.Fatalf("sites failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stdout, "alpha") || !strings.Contains(stdout, "Sample site A") || !strings.Contains(stdout, "yes") {
		t.Fatalf("unexpected sites output: %q", stdout)
	}
	if !strings.Contains(stdout, "beta") || !strings.Contains(stdout, "no") {
		t.Fatalf("unexpected sites output: %q", stdout)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "site", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("site failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	var siteResp siteResponse
	if err := json.Unmarshal([]byte(stdout), &siteResp); err != nil {
		t.Fatalf("unmarshal site output: %v", err)
	}
	if siteResp.Site.Name != "alpha" || siteResp.Site.State.SavedValues != 1 || !siteResp.Site.State.Exists {
		t.Fatalf("unexpected site response: %#v", siteResp)
	}
	if siteResp.Site.Login == nil || !siteResp.Site.Login.Enabled || siteResp.Site.Login.Type != "basic" || siteResp.Site.Login.Path != "/login" {
		t.Fatalf("unexpected login summary in site response: %#v", siteResp)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "actions", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("actions failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stdout, "site: alpha") {
		t.Fatalf("expected site header in actions output: %q", stdout)
	}
	if !strings.Contains(stdout, "ACTION") || !strings.Contains(stdout, "DESCRIPTION") {
		t.Fatalf("expected actions table header: %q", stdout)
	}
	if !strings.Contains(stdout, "profile") {
		t.Fatalf("unexpected actions output: %q", stdout)
	}
	if !strings.Contains(stdout, "Load profile") {
		t.Fatalf("expected action descriptions in actions output: %q", stdout)
	}
	if strings.Contains(stdout, "action:") || strings.Contains(stdout, "description:") || strings.Contains(stdout, "method:") || strings.Contains(stdout, "path:") || strings.Contains(stdout, "params:") || strings.Contains(stdout, "extracts:") {
		t.Fatalf("expected tabular actions output: %q", stdout)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "actions", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("actions json failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	var actionsResp actionsResponse
	if err := json.Unmarshal([]byte(stdout), &actionsResp); err != nil {
		t.Fatalf("unmarshal actions output: %v", err)
	}
	if len(actionsResp.Actions) != 1 {
		t.Fatalf("unexpected actions response: %#v", actionsResp)
	}
	foundProfile := false
	for _, action := range actionsResp.Actions {
		if action.Name == "profile" {
			foundProfile = true
			if action.Method != "GET" || action.Path != "/me" {
				t.Fatalf("expected method/path details for profile action: %#v", action)
			}
			if len(action.Params) != 1 {
				t.Fatalf("expected param count for profile action: %#v", action)
			}
			if len(action.Extracts) != 1 {
				t.Fatalf("expected extract count for profile action: %#v", action)
			}
			if action.Params[0].Name != "user_id" || action.Params[0].Type != "string" || action.Params[0].Description != "Profile id" || !action.Params[0].Required {
				t.Fatalf("unexpected param spec for profile action: %#v", action)
			}
			if action.Extracts[0].Name != "group" || action.Extracts[0].Type != "string" || action.Extracts[0].Description != "Filter group" {
				t.Fatalf("unexpected extract spec for profile action: %#v", action)
			}
			if example, ok := action.Extracts[0].Example.(string); !ok || example != "GROUP_A" {
				t.Fatalf("unexpected extract example for profile action: %#v", action)
			}
		}
	}
	if !foundProfile {
		t.Fatalf("expected profile action in actions response: %#v", actionsResp)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "action", "alpha", "profile"})
	if exitCode != ExitSuccess {
		t.Fatalf("action json failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	var actionResp actionResponse
	if err := json.Unmarshal([]byte(stdout), &actionResp); err != nil {
		t.Fatalf("unmarshal action output: %v", err)
	}
	if actionResp.Action.Name != "profile" || len(actionResp.Action.Params) != 1 || actionResp.Action.Params[0].Name != "user_id" || len(actionResp.Action.Extracts) != 1 || actionResp.Action.Extracts[0].Name != "group" {
		t.Fatalf("unexpected action response: %#v", actionResp)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "action", "alpha", "profile"})
	if exitCode != ExitSuccess {
		t.Fatalf("action text failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stdout, "Usage:\n  httpx run alpha profile [flags]") {
		t.Fatalf("expected usage section in action text: %q", stdout)
	}
	if !strings.Contains(stdout, "\nDescription:\n  Load profile\n") {
		t.Fatalf("expected description section in action text: %q", stdout)
	}
	if !strings.Contains(stdout, "\nFlags:\n") || !strings.Contains(stdout, "--param key=value") || !strings.Contains(stdout, "--param-json-file <path|->") || !strings.Contains(stdout, "--extract <json-object>") || !strings.Contains(stdout, "--extract-json-file <path|->") || !strings.Contains(stdout, "-h, --help") {
		t.Fatalf("expected flags section in action text: %q", stdout)
	}
	if !strings.Contains(stdout, "\nParams fields:\n") || !strings.Contains(stdout, "name     type    required  default") || !strings.Contains(stdout, `user_id  string  yes       -        Profile id   "42"`) {
		t.Fatalf("expected params table in action text: %q", stdout)
	}
	if !strings.Contains(stdout, "\nExtracts fields:\n") || !strings.Contains(stdout, "name   type    required  default") {
		t.Fatalf("expected extracts table in action text: %q", stdout)
	}
	if !strings.Contains(stdout, `group  string  no        -        Filter group  "GROUP_A"`) {
		t.Fatalf("expected extract row in action text: %q", stdout)
	}
	if !strings.Contains(stdout, "\nExamples:\n") || !strings.Contains(stdout, `httpx run alpha profile --param user_id=42`) || !strings.Contains(stdout, `extract.json: {"group":"GROUP_A"}`) || !strings.Contains(stdout, `httpx run alpha profile --param user_id=42 --extract-json-file /absolute/path/extract.json`) {
		t.Fatalf("expected examples section in action text: %q", stdout)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "state", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("state failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if strings.Contains(stdout, "secret-value") || strings.Contains(stdout, "abc") {
		t.Fatalf("state output leaked secret values: %q", stdout)
	}
	var stateResp stateResponse
	if err := json.Unmarshal([]byte(stdout), &stateResp); err != nil {
		t.Fatalf("unmarshal state output: %v", err)
	}
	if !stateResp.State.Exists || stateResp.State.Cookies != 1 || stateResp.State.SavedValues != 1 {
		t.Fatalf("unexpected state response: %#v", stateResp)
	}
}

func TestDiscoveryExposesLoginSummaryAndDynamicPath(t *testing.T) {
	configDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(configDir, "alpha.toml"), []byte(strings.TrimSpace(`
version = 1
description = "Sample site A"
base_url = "https://alpha.example.com"
[login]
path = "/login"

[actions.login_finish]
description = "Finish login"
path = { from = "state", key = "login.next_url" }
`)+"\n"), 0o600); err != nil {
		t.Fatalf("write alpha config: %v", err)
	}

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--format", "json", "site", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("site failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var siteResp siteResponse
	if err := json.Unmarshal([]byte(stdout), &siteResp); err != nil {
		t.Fatalf("unmarshal site output: %v", err)
	}
	if siteResp.Site.Login == nil || !siteResp.Site.Login.Enabled || siteResp.Site.Login.Path != "/login" {
		t.Fatalf("unexpected login summary: %#v", siteResp.Site)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--format", "json", "actions", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("actions failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var actionsResp actionsResponse
	if err := json.Unmarshal([]byte(stdout), &actionsResp); err != nil {
		t.Fatalf("unmarshal actions output: %v", err)
	}
	for _, action := range actionsResp.Actions {
		if action.Name == "login_finish" && action.Path != `{"from":"state","key":"login.next_url"}` {
			t.Fatalf("unexpected dynamic path rendering: %#v", action)
		}
	}
}

func TestCompileReadsGlobalAndChatSecretsByScope(t *testing.T) {
	globalSecretDir := t.TempDir()
	chatDir := t.TempDir()
	chatSecretDir := filepath.Join(chatDir, ".secret", "httpx")
	if err := os.MkdirAll(chatSecretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalSecretDir, "demo.json"), []byte(`{"token":"global-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatSecretDir, "demo.json"), []byte(`{"token":"chat-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(writeConfig(t, `
version = 1
description = "Demo"
base_url = "https://example.com"

[actions.global]
description = "Global"
path = "/"
headers = { Authorization = { from = "secret", key = "token" } }

[actions.chat]
description = "Chat"
path = "/"
headers = { Authorization = { from = "secret", scope = "chat", key = "token" } }
`))
	if err != nil {
		t.Fatal(err)
	}

	baseReq := commandRequest{
		Command: commandRun,
		Site:    "demo",
		Options: globalOptions{
			Format:     formatJSON,
			SecretDir:  globalSecretDir,
			StateDir:   t.TempDir(),
			ChatDir:    chatDir,
			ChatDirSet: true,
		},
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	state := &profileState{Values: map[string]string{}}

	globalReq := baseReq
	globalReq.Action = "global"
	globalCompiled, _, _, err := rt.compile(globalReq, cfg, state)
	if err != nil {
		t.Fatalf("compile global secret: %v", err)
	}
	if got := globalCompiled.Headers["Authorization"]; got != "global-token" {
		t.Fatalf("global secret = %q", got)
	}

	chatReq := baseReq
	chatReq.Action = "chat"
	chatCompiled, _, _, err := rt.compile(chatReq, cfg, state)
	if err != nil {
		t.Fatalf("compile chat secret: %v", err)
	}
	if got := chatCompiled.Headers["Authorization"]; got != "chat-token" {
		t.Fatalf("chat secret = %q", got)
	}
}

func TestChatLoginUsesChatSecretAndPersistsChatState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("username") != "chat-user" || r.FormValue("password") != "chat-password" {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "chat-cookie", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"chat-token"}`))
	}))
	t.Cleanup(server.Close)

	chatDir := t.TempDir()
	chatSecretDir := filepath.Join(chatDir, ".secret", "httpx")
	if err := os.MkdirAll(chatSecretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatSecretDir, "demo.json"), []byte(`{"username":"chat-user","password":"chat-password"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(chatDirEnv, chatDir)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo"
base_url = %q
state_scope = "chat"

[login]
path = "/login"
secret_scope = "chat"
save = { "auth.authorization" = "\"Bearer \" + .body.token" }
`, server.URL))
	globalStateDir := t.TempDir()

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", globalStateDir,
		"--format", "json",
		"login", "demo",
	})
	if exitCode != ExitSuccess {
		t.Fatalf("chat login failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	chatStateDir := filepath.Join(chatDir, ".state", "httpx")
	state, err := loadState(chatStateDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.Values["auth.authorization"] != "Bearer chat-token" || len(state.Cookies) != 1 || state.LastLogin == "" {
		t.Fatalf("unexpected chat state: %#v", state)
	}
	if _, err := os.Stat(statePath(globalStateDir, "demo")); !os.IsNotExist(err) {
		t.Fatalf("global state must not be written, stat err=%v", err)
	}
}

func TestChatStateIsIsolatedAndDiscoveryHidesChatPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/remember":
			_, _ = fmt.Fprintf(w, `{"value":%q}`, r.URL.Query().Get("value"))
		case "/recall":
			_, _ = fmt.Fprintf(w, `{"value":%q}`, r.Header.Get("X-Remembered"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo"
base_url = %q
state_scope = "chat"

[actions.remember]
description = "Remember"
path = "/remember"
query = { value = { from = "param", key = "value" } }
save = { remembered = ".body.value" }

[actions.recall]
description = "Recall"
path = "/recall"
headers = { X-Remembered = { from = "state", scope = "chat", key = "remembered" } }
`, server.URL))

	runtimeRoot := t.TempDir()
	chatA := filepath.Join(runtimeRoot, "runtime", "chats", "chat-a-private")
	chatB := filepath.Join(runtimeRoot, "runtime", "chats", "chat-b-private")
	for _, dir := range []string{chatA, chatB} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	globalStateDir := t.TempDir()

	remember := func(chatDir, value string) {
		t.Helper()
		t.Setenv(chatDirEnv, chatDir)
		stdout, stderr, exitCode := runMain(t, []string{
			"--config", configDir,
			"--state", globalStateDir,
			"--format", "json",
			"run", "demo", "remember",
			"--param", "value=" + value,
		})
		if exitCode != ExitSuccess {
			t.Fatalf("remember failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
		}
	}
	recall := func(chatDir string) string {
		t.Helper()
		t.Setenv(chatDirEnv, chatDir)
		stdout, stderr, exitCode := runMain(t, []string{
			"--config", configDir,
			"--state", globalStateDir,
			"--format", "json",
			"run", "demo", "recall",
		})
		if exitCode != ExitSuccess {
			t.Fatalf("recall failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		body, ok := result["body"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected recall body: %#v", result["body"])
		}
		value, _ := body["value"].(string)
		return value
	}

	remember(chatA, "alpha")
	remember(chatB, "beta")
	if got := recall(chatA); got != "alpha" {
		t.Fatalf("chat A recalled %q", got)
	}
	if got := recall(chatB); got != "beta" {
		t.Fatalf("chat B recalled %q", got)
	}

	for dir, want := range map[string]string{chatA: "alpha", chatB: "beta"} {
		state, err := loadState(filepath.Join(dir, ".state", "httpx"), "demo")
		if err != nil {
			t.Fatal(err)
		}
		if got := state.Values["remembered"]; got != want {
			t.Fatalf("state under %s = %q, want %q", filepath.Base(dir), got, want)
		}
	}
	if _, err := os.Stat(statePath(globalStateDir, "demo")); !os.IsNotExist(err) {
		t.Fatalf("global state must remain absent, stat err=%v", err)
	}

	t.Setenv(chatDirEnv, chatA)
	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", globalStateDir,
		"--format", "json",
		"state", "demo",
	})
	if exitCode != ExitSuccess {
		t.Fatalf("state discovery failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if strings.Contains(stdout, chatA) || strings.Contains(stdout, filepath.Base(chatA)) {
		t.Fatalf("state discovery leaked chat path: %s", stdout)
	}
	var response stateResponse
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatal(err)
	}
	if response.State.Scope != string(scopeChat) || response.State.Path != filepath.Join(".state", "httpx", "demo.json") {
		t.Fatalf("unexpected chat state summary: %#v", response.State)
	}
}

func TestChatBindingTokenIsSavedAndSentWithoutAppearingInActionOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/attach":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			fileName, _ := payload["fileName"].(string)
			_, _ = fmt.Fprintf(
				w,
				`{"ok":true,"bindingToken":%q,"session":{"ready":true,"editorType":"word","capabilities":{"tools":["word_inspect"]}}}`,
				"binding-for-"+fileName,
			)
		case "/execute":
			_, _ = fmt.Fprintf(w, `{"authorization":%q}`, r.Header.Get("Authorization"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "office", fmt.Sprintf(`
version = 1
description = "Office"
base_url = %q
state_scope = "chat"

[actions.attach]
description = "Attach"
method = "POST"
path = "/attach"
body = { fileName = { from = "param", key = "file_name" }, editorType = { from = "literal", value = "word" } }
extract_type = "jq"
extract_expr = '''{attached: .body.ok, ready: .body.session.ready, editorType: .body.session.editorType, capabilities: .body.session.capabilities}'''
save = { "auth.bridge" = '''"Bearer " + .body.bindingToken''' }

[actions.execute]
description = "Execute"
path = "/execute"
headers = { Authorization = { from = "state", scope = "chat", key = "auth.bridge" } }
`, server.URL))

	runtimeRoot := t.TempDir()
	chatA := filepath.Join(runtimeRoot, "chat-a")
	chatB := filepath.Join(runtimeRoot, "chat-b")
	for _, dir := range []string{chatA, chatB} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	attach := func(chatDir, fileName string) {
		t.Helper()
		t.Setenv(chatDirEnv, chatDir)
		stdout, stderr, exitCode := runMain(t, []string{
			"--config", configDir,
			"--format", "json",
			"run", "office", "attach",
			"--param", "file_name=" + fileName,
		})
		if exitCode != ExitSuccess {
			t.Fatalf("attach failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
		}
		if strings.Contains(stdout, "binding-for-") {
			t.Fatalf("attach output leaked binding token: %s", stdout)
		}
	}
	execute := func(chatDir string) string {
		t.Helper()
		t.Setenv(chatDirEnv, chatDir)
		stdout, stderr, exitCode := runMain(t, []string{
			"--config", configDir,
			"--format", "json",
			"run", "office", "execute",
		})
		if exitCode != ExitSuccess {
			t.Fatalf("execute failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(stdout), &response); err != nil {
			t.Fatal(err)
		}
		body, _ := response["body"].(map[string]any)
		authorization, _ := body["authorization"].(string)
		return authorization
	}

	attach(chatA, "alpha.docx")
	attach(chatB, "beta.docx")
	if got := execute(chatA); got != "Bearer binding-for-alpha.docx" {
		t.Fatalf("chat A authorization = %q", got)
	}
	if got := execute(chatB); got != "Bearer binding-for-beta.docx" {
		t.Fatalf("chat B authorization = %q", got)
	}
}

func TestChatSiteDiscoveryHidesSecretAndStatePaths(t *testing.T) {
	privateChatDir := filepath.Join(t.TempDir(), "runtime", "chats", "private-chat-id")
	if err := os.MkdirAll(privateChatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(chatDirEnv, privateChatDir)
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo"
base_url = "https://example.com"
state_scope = "chat"

[login]
path = "/login"
secret_scope = "chat"
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--format", "json", "site", "demo"})
	if exitCode != ExitSuccess {
		t.Fatalf("site discovery failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if strings.Contains(stdout, privateChatDir) || strings.Contains(stdout, "private-chat-id") {
		t.Fatalf("site discovery leaked chat path: %s", stdout)
	}

	var response siteResponse
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatal(err)
	}
	if response.Site.Login == nil ||
		response.Site.Login.SecretScope != string(scopeChat) ||
		response.Site.Login.SecretPath != filepath.Join(".secret", "httpx", "demo.json") {
		t.Fatalf("unexpected chat login summary: %#v", response.Site.Login)
	}
	if response.Site.State.Scope != string(scopeChat) ||
		response.Site.State.Path != filepath.Join(".state", "httpx", "demo.json") {
		t.Fatalf("unexpected chat state summary: %#v", response.Site.State)
	}
}

func TestDefaultStateScopeRemainsGlobalWhenAPChatDirIsSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"global"}`))
	}))
	t.Cleanup(server.Close)

	chatDir := t.TempDir()
	t.Setenv(chatDirEnv, chatDir)
	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo"
base_url = %q

[actions.remember]
description = "Remember"
path = "/remember"
save = { remembered = ".body.value" }
`, server.URL))
	globalStateDir := t.TempDir()

	stdout, stderr, exitCode := runMain(t, []string{
		"--config", configDir,
		"--state", globalStateDir,
		"--format", "json",
		"run", "demo", "remember",
	})
	if exitCode != ExitSuccess {
		t.Fatalf("global run failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	state, err := loadState(globalStateDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.Values["remembered"] != "global" {
		t.Fatalf("unexpected global state: %#v", state)
	}
	if _, err := os.Stat(filepath.Join(chatDir, ".state", "httpx", "demo.json")); !os.IsNotExist(err) {
		t.Fatalf("chat state must remain absent, stat err=%v", err)
	}
}

func TestChatScopeFailsWithoutAPChatDir(t *testing.T) {
	t.Setenv(chatDirEnv, "")
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo"
base_url = "https://example.com"
state_scope = "chat"

[actions.get]
description = "Get"
path = "/"
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "get"})
	if exitCode != ExitConfig {
		t.Fatalf("expected config failure, got exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stdout+stderr, chatDirEnv) {
		t.Fatalf("expected AP_CHAT_DIR guidance, stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestInspectChatScopeDoesNotCreateStateArtifacts(t *testing.T) {
	chatDir := t.TempDir()
	t.Setenv(chatDirEnv, chatDir)
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo"
base_url = "https://example.com"
state_scope = "chat"

[actions.get]
description = "Get"
path = "/"
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "get"})
	if exitCode != ExitSuccess {
		t.Fatalf("inspect failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if _, err := os.Stat(filepath.Join(chatDir, ".state")); !os.IsNotExist(err) {
		t.Fatalf("inspect created chat state artifacts: %v", err)
	}
}

func TestExplicitChatDynamicSourceRequiresAPChatDir(t *testing.T) {
	t.Setenv(chatDirEnv, "")
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo"
base_url = "https://example.com"

[actions.get]
description = "Get"
path = "/"
headers = { Authorization = { from = "state", scope = "chat", key = "auth.authorization" } }
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "get"})
	if exitCode != ExitConfig {
		t.Fatalf("expected config failure, got exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stdout+stderr, chatDirEnv) {
		t.Fatalf("expected AP_CHAT_DIR guidance, stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestChatSecretErrorDoesNotLeakChatPath(t *testing.T) {
	privateChatDir := filepath.Join(t.TempDir(), "runtime", "chats", "private-chat-id")
	if err := os.MkdirAll(privateChatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(chatDirEnv, privateChatDir)
	configDir := writeProfileConfig(t, "demo", `
version = 1
description = "Demo"
base_url = "https://example.com"

[actions.get]
description = "Get"
path = "/"
headers = { Authorization = { from = "secret", scope = "chat", key = "authorization" } }
`)

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--format", "json", "run", "demo", "get"})
	if exitCode == ExitSuccess {
		t.Fatalf("expected missing secret failure, stdout=%s", stdout)
	}
	output := stdout + stderr
	if strings.Contains(output, privateChatDir) || strings.Contains(output, "private-chat-id") {
		t.Fatalf("chat secret error leaked chat path: %s", output)
	}
	if !strings.Contains(output, filepath.Join(".secret", "httpx", "demo.json")) {
		t.Fatalf("expected chat-relative secret path, got %s", output)
	}
}

func TestStateFileLockSerializesSameStoreAndAllowsDifferentStores(t *testing.T) {
	dirA := filepath.Join(t.TempDir(), "a")
	dirB := filepath.Join(t.TempDir(), "b")

	first, err := acquireStateLock(dirA, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	lockInfo, err := os.Stat(statePath(dirA, "demo") + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if got := lockInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("state lock permissions = %o, want 600", got)
	}

	type lockResult struct {
		lock *stateFileLock
		err  error
	}
	sameResult := make(chan lockResult, 1)
	otherResult := make(chan lockResult, 1)
	go func() {
		lock, err := acquireStateLock(dirA, "demo")
		sameResult <- lockResult{lock: lock, err: err}
	}()
	go func() {
		lock, err := acquireStateLock(dirB, "demo")
		otherResult <- lockResult{lock: lock, err: err}
	}()

	select {
	case result := <-otherResult:
		if result.err != nil {
			t.Fatal(result.err)
		}
		_ = result.lock.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("different state stores unexpectedly blocked each other")
	}

	select {
	case result := <-sameResult:
		if result.lock != nil {
			_ = result.lock.Close()
		}
		t.Fatalf("same state store acquired before release: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-sameResult:
		if result.err != nil {
			t.Fatal(result.err)
		}
		_ = result.lock.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("same state store did not acquire after release")
	}
}

func TestConcurrentChatRunsPreserveBothStateUpdates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(75 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"value":%q}`, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	t.Cleanup(server.Close)

	chatDir := t.TempDir()
	t.Setenv(chatDirEnv, chatDir)
	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
description = "Demo"
base_url = %q
state_scope = "chat"

[actions.first]
description = "First"
path = "/first"
save = { first = ".body.value" }

[actions.second]
description = "Second"
path = "/second"
save = { second = ".body.value" }
`, server.URL))

	argsFor := func(action string) []string {
		return []string{"--config", configDir, "--format", "json", "run", "demo", action}
	}
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, action := range []string{"first", "second"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			var stdout, stderr bytes.Buffer
			results <- Execute(argsFor(action), nil, &stdout, &stderr)
		}(action)
	}
	wg.Wait()
	close(results)
	for exitCode := range results {
		if exitCode != ExitSuccess {
			t.Fatalf("concurrent run failed with exit %d", exitCode)
		}
	}

	state, err := loadState(filepath.Join(chatDir, ".state", "httpx"), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.Values["first"] != "first" || state.Values["second"] != "second" {
		t.Fatalf("concurrent state update was lost: %#v", state.Values)
	}
}

func TestSaveStateUsesAtomicFileAndRestrictivePermissions(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "nested", "state")
	if err := saveState(stateDir, "demo", &profileState{
		Values: map[string]string{"token": "value"},
	}); err != nil {
		t.Fatal(err)
	}

	dirInfo, err := os.Stat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("state directory permissions = %o, want 700", got)
	}
	fileInfo, err := os.Stat(statePath(stateDir, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("state file permissions = %o, want 600", got)
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("temporary state file was not cleaned up: %s", entry.Name())
		}
	}
}

func TestMainPrintsVersionCommand(t *testing.T) {
	oldVersion := buildinfo.Version
	oldCommit := buildinfo.Commit
	oldBuildTime := buildinfo.BuildTime
	buildinfo.Version = "v0.1.0"
	buildinfo.Commit = "abc1234"
	buildinfo.BuildTime = "2026-03-26T12:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version = oldVersion
		buildinfo.Commit = oldCommit
		buildinfo.BuildTime = oldBuildTime
	})

	stdout, stderr, exitCode := runMain(t, []string{"version"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "httpx v0.1.0 (commit abc1234, built 2026-03-26T12:00:00Z)" {
		t.Fatalf("unexpected version output: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestMainPrintsVersionFlag(t *testing.T) {
	oldVersion := buildinfo.Version
	oldCommit := buildinfo.Commit
	oldBuildTime := buildinfo.BuildTime
	buildinfo.Version = "v0.1.0"
	buildinfo.Commit = "abc1234"
	buildinfo.BuildTime = "2026-03-26T12:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version = oldVersion
		buildinfo.Commit = oldCommit
		buildinfo.BuildTime = oldBuildTime
	})

	stdout, stderr, exitCode := runMain(t, []string{"--version"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "httpx v0.1.0 (commit abc1234, built 2026-03-26T12:00:00Z)" {
		t.Fatalf("unexpected --version output: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestMainHelpUsesCobraCommandTree(t *testing.T) {
	stdout, stderr, exitCode := runMain(t, []string{"help"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}
	if strings.Count(stdout, "Usage:") != 1 {
		t.Fatalf("expected single usage block, got %q", stdout)
	}
	if !strings.Contains(stdout, "Available Commands:") || !strings.Contains(stdout, "run") || !strings.Contains(stdout, "version") {
		t.Fatalf("unexpected help output: %q", stdout)
	}
	if strings.Contains(stdout, "\n  load ") {
		t.Fatalf("load command must not be listed: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestMainSubcommandHelpIncludesInheritedFlags(t *testing.T) {
	stdout, stderr, exitCode := runMain(t, []string{"inspect", "--help"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}
	if !strings.Contains(stdout, "httpx inspect <site> <action>") {
		t.Fatalf("unexpected inspect help output: %q", stdout)
	}
	if !strings.Contains(stdout, "--reveal") || !strings.Contains(stdout, "--format") {
		t.Fatalf("expected inherited flags in help output: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestMainUnknownCommandReturnsConfigExit(t *testing.T) {
	stdout, stderr, exitCode := runMain(t, []string{"nope"})
	if exitCode != ExitConfig {
		t.Fatalf("expected config exit code, got %d", exitCode)
	}
	if stdout != "" {
		t.Fatalf("unexpected stdout: %q", stdout)
	}
	if !strings.Contains(stderr, `unknown command "nope" for "httpx"`) {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestMainLoadCommandIsUnavailable(t *testing.T) {
	stdout, stderr, exitCode := runMain(t, []string{"load", "demo"})
	if exitCode != ExitConfig {
		t.Fatalf("expected config exit code, got %d", exitCode)
	}
	if stdout != "" {
		t.Fatalf("unexpected stdout: %q", stdout)
	}
	if !strings.Contains(stderr, `unknown command "load" for "httpx"`) {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestBuildActionExamplesUsesJSONFilesForStructuredInputs(t *testing.T) {
	examples := strings.Join(buildActionExamples(actionDetail{
		Site: "office",
		Name: "execute_batch",
		Params: []actionInputSpec{
			{Name: "tool_calls", Type: "array", Required: true, Example: []any{
				map[string]any{"name": "set_values", "arguments": map[string]any{
					"values": []any{[]any{"中文", nil, `quote'\"\\line\nnext`}},
				}},
			}},
			{Name: "request_id", Type: "string", Required: true, Example: "batch-1"},
		},
	}), "\n")
	if !strings.Contains(examples, `params.json: {"request_id":"batch-1","tool_calls":[{"arguments":{"values":[["中文",null,"quote'\\\"\\\\line\\nnext"]]},"name":"set_values"}]}`) {
		t.Fatalf("expected typed params JSON example: %s", examples)
	}
	if !strings.Contains(examples, `httpx run office execute_batch --param-json-file /absolute/path/params.json`) {
		t.Fatalf("expected params file command: %s", examples)
	}
	if strings.Contains(examples, "--param tool_calls=") {
		t.Fatalf("structured params must not be rendered inline: %s", examples)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeProfileConfig(t *testing.T, profile, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, profile+".toml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write site config: %v", err)
	}
	return dir
}

func writeSecret(t *testing.T, site, username, password string) string {
	t.Helper()

	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	dir := filepath.Join(dataHome, "secret", "httpx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir secret dir: %v", err)
	}
	content := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	path := filepath.Join(dir, site+".json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return dir
}

func runMain(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Execute(args, nil, &stdout, &stderr)
	return stdout.String(), stderr.String(), exitCode
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
