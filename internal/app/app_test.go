package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
base_url = "https://example.com"

[actions.get]
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
}

func TestLoadConfigRejectsLegacyProfilesWrapper(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1

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

func TestCompileMergesDefaultsActionAndCLIOverride(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
base_url = %q
timeout = "2s"
retries = 1

[headers]
X-Base = "base"
X-Shared = "profile"

[query]
scope = "base"
region = "cn"

[actions.info]
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
		Profile: "demo",
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

func TestResolverSupportsEnvFileShellAndState(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "token.txt")
	if err := os.WriteFile(filePath, []byte(" file-value \n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	t.Setenv("HTTPX_ENV_VALUE", " env-value \n")

	r := resolver{
		state:  &profileState{Values: map[string]string{"saved.token": " state-value \n"}},
		reveal: true,
	}

	cases := map[string]map[string]any{
		"env": {
			"from": "env",
			"key":  "HTTPX_ENV_VALUE",
			"trim": true,
		},
		"file": {
			"from": "file",
			"path": filePath,
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
		"env":   "env-value",
		"file":  "file-value",
		"shell": "shell-value",
		"state": "state-value",
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

func TestParseArgsSupportsRepeatableParams(t *testing.T) {
	t.Parallel()

	req, err := parseArgs([]string{
		"--param", "user=alice",
		"--param", "page=2",
		"demo", "list",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if req.Options.Params["user"] != "alice" || req.Options.Params["page"] != "2" {
		t.Fatalf("unexpected params: %#v", req.Options.Params)
	}
}

func TestParseArgsDefaultsToBodyExceptInspect(t *testing.T) {
	t.Parallel()

	runReq, err := parseArgs([]string{"demo", "list"})
	if err != nil {
		t.Fatalf("parseArgs run failed: %v", err)
	}
	if runReq.Options.Format != formatBody {
		t.Fatalf("expected run default format body, got %q", runReq.Options.Format)
	}

	inspectReq, err := parseArgs([]string{"--inspect", "demo", "list"})
	if err != nil {
		t.Fatalf("parseArgs inspect failed: %v", err)
	}
	if inspectReq.Options.Format != formatJSON {
		t.Fatalf("expected inspect default format json, got %q", inspectReq.Options.Format)
	}
}

func TestParseArgsSupportsGlobalFlagsAnywhere(t *testing.T) {
	t.Parallel()

	req, err := parseArgs([]string{
		"demo",
		"--format", "json",
		"--inspect",
		"--param", "user=alice",
		"list",
		"--timeout=5s",
		"--state-dir", "/tmp/httpx-state",
		"--config", "/tmp/httpx-config",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if req.Profile != "demo" || req.Action != "list" {
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
	if !req.Options.Inspect {
		t.Fatalf("expected inspect mode")
	}
	if req.Options.Params["user"] != "alice" {
		t.Fatalf("unexpected params: %#v", req.Options.Params)
	}
}

func TestParseArgsRejectsInspectBodyFormat(t *testing.T) {
	t.Parallel()

	if _, err := parseArgs([]string{"--inspect", "--format", "body", "demo", "list"}); err == nil {
		t.Fatal("expected inspect/body parse error")
	}
}

func TestParseArgsRejectsLegacyCommands(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"run", "demo", "list"},
		{"login", "demo"},
		{"inspect", "demo", "list"},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("expected parse error for legacy args %#v", args)
		}
	}
}

func TestResolveConfigPathRejectsFile(t *testing.T) {
	t.Parallel()

	filePath := writeConfig(t, "version = 1\nbase_url = \"https://example.com\"\n[actions.me]\npath = \"/me\"\n")
	_, err := resolveConfigPath(filePath, "demo")
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("expected config dir error, got %v", err)
	}
}

func TestCompileSupportsParamAndLiteralSources(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
base_url = %q

[actions.search]
path = "/search"
headers = { X-Mode = { from = "literal", value = "agent" } }
query = { q = { from = "param", key = "query" }, page = { from = "param", key = "page", default = 1 } }
body = { keyword = { from = "param", key = "query" }, source = { from = "literal", value = "cli" } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Profile: "demo",
		Action:  "search",
		Options: globalOptions{
			StateDir: t.TempDir(),
			Format:   formatJSON,
			Params: map[string]string{
				"query": "golang",
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
}

func TestCompileSupportsCookies(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
base_url = %q
cookies = { locale = "zh-CN" }

[actions.me]
path = "/me"
cookies = { session = { from = "state", key = "auth.session" }, mode = { from = "literal", value = "agent" } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Profile: "demo",
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

func TestCompileFormEncodesNestedObjectsAsJSONString(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
base_url = %q

[actions.submit]
method = "POST"
path = "/login"
form = { data = { UserId = "alice", Password = "secret", SystemType = "100", ClientType = "2" } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Profile: "demo",
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

	encodedBody := string(compiled.BodyBytes)
	if !strings.HasPrefix(encodedBody, "data=") {
		t.Fatalf("expected form field named data, got %q", encodedBody)
	}
	if !strings.Contains(encodedBody, "%22UserId%22%3A%22alice%22") {
		t.Fatalf("expected nested object to be JSON-encoded in form body, got %q", encodedBody)
	}

	body, ok := compiled.Body.(map[string]string)
	if !ok {
		t.Fatalf("expected inspect body to be string form map, got %#v", compiled.Body)
	}
	if !strings.Contains(body["data"], `"UserId":"alice"`) {
		t.Fatalf("expected nested object stringified in inspect body, got %#v", body)
	}
}

func TestResolverReportsMissingEnvAndShellTimeout(t *testing.T) {
	t.Parallel()

	r := resolver{
		state:  &profileState{Values: map[string]string{}},
		reveal: true,
	}

	if _, err := r.resolveAny(context.Background(), map[string]any{"from": "env", "key": "HTTPX_MISSING_ENV"}); err == nil || !errors.Is(err, ErrExecution) {
		t.Fatalf("expected missing env execution error, got %v", err)
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

func TestRunUsesDefaultProfileConfigPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(server.Close)

	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	configDir := filepath.Join(configRoot, "httpx")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "demo.toml"), []byte(strings.TrimSpace(fmt.Sprintf(`
version = 1
base_url = %q

[actions.ping]
path = "/ping"
`, server.URL))+"\n"), 0o600); err != nil {
		t.Fatalf("write profile config: %v", err)
	}

	stdout, stderr, exitCode := runMain(t, []string{"demo", "ping"})
	if exitCode != ExitSuccess {
		t.Fatalf("run failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != "pong" {
		t.Fatalf("unexpected body output: %q", stdout)
	}
}

func TestLoginPersistsStateAndRunReusesCookieAndToken(t *testing.T) {
	t.Setenv("HTTPX_USER", "alice")
	t.Setenv("HTTPX_PASS", "secret")

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

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
base_url = %q
login_action = "login_request"

[actions.login_request]
method = "POST"
path = "/login"
form = { username = { from = "env", key = "HTTPX_USER" }, password = { from = "env", key = "HTTPX_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }

[actions.data]
path = "/data"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
extract = ".body.value"
`, server.URL))

	stateDir := t.TempDir()
	loginStdout, loginStderr, exitCode := runMain(t, []string{"--config", configDir, "--state-dir", stateDir, "--format", "json", "demo", "login"})
	if exitCode != ExitSuccess {
		t.Fatalf("login failed: exit=%d stderr=%s stdout=%s", exitCode, loginStderr, loginStdout)
	}

	state, err := loadState(stateDir, "demo")
	if err != nil {
		t.Fatalf("loadState failed: %v", err)
	}
	if state.Values["auth.authorization"] != "Bearer token-123" {
		t.Fatalf("expected saved token, got %#v", state.Values)
	}
	if len(state.Cookies) == 0 {
		t.Fatalf("expected persisted cookies")
	}
	if state.LastLogin == "" {
		t.Fatalf("expected last_login to be set")
	}

	runStdout, runStderr, exitCode := runMain(t, []string{"--config", configDir, "--state-dir", stateDir, "--format", "json", "demo", "data"})
	if exitCode != ExitSuccess {
		t.Fatalf("run failed: exit=%d stderr=%s stdout=%s", exitCode, runStderr, runStdout)
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
	if value, ok := env.Extract.(float64); !ok || value != 42 {
		t.Fatalf("unexpected extract: %#v", env.Extract)
	}
}

func TestRunSupportsExplicitCookiesFromState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"session_id":"session-123"}`))
		case "/me":
			cookie, err := r.Cookie("oem.sessionid")
			if err != nil || cookie.Value != "session-123" {
				http.Error(w, "missing oem.sessionid", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
base_url = %q
login_action = "login_request"

[actions.login_request]
method = "POST"
path = "/login"
save = { "oem.sessionid" = ".body.session_id" }

[actions.me]
path = "/me"
cookies = { "oem.sessionid" = { from = "state", key = "oem.sessionid" } }
extract = ".body.ok"
`, server.URL))

	stateDir := t.TempDir()
	_, _, exitCode := runMain(t, []string{"--config", configDir, "--state-dir", stateDir, "--format", "json", "demo", "login"})
	if exitCode != ExitSuccess {
		t.Fatalf("login failed with exit=%d", exitCode)
	}

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state-dir", stateDir, "--format", "json", "demo", "me"})
	if exitCode != ExitSuccess {
		t.Fatalf("run failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}

	var env envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("unmarshal run output: %v", err)
	}
	if ok, _ := env.Extract.(bool); !ok {
		t.Fatalf("unexpected extract: %#v", env.Extract)
	}
}

func TestInspectRedactsDynamicValues(t *testing.T) {
	t.Setenv("HTTPX_SECRET", "secret-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
base_url = %q

[actions.secret]
method = "POST"
path = "/secret"
headers = { Authorization = { from = "env", key = "HTTPX_SECRET" } }
query = { token = { from = "env", key = "HTTPX_SECRET" } }
body = { nested = { from = "env", key = "HTTPX_SECRET" } }
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--inspect", "demo", "secret"})
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
	if !strings.Contains(compiled.URL, "token=%2A%2A%2A") {
		t.Fatalf("expected redacted query token in URL, got %s", compiled.URL)
	}
	body, ok := compiled.Body.(map[string]any)
	if !ok || body["nested"] != redactedValue {
		t.Fatalf("expected redacted body, got %#v", compiled.Body)
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
base_url = %q

[actions.fail]
path = "/fail"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state-dir", t.TempDir(), "--format", "json", "demo", "fail"})
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
}

func TestBodyFormatOutputsRawBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(server.Close)

	configDir := writeProfileConfig(t, "demo", fmt.Sprintf(`
version = 1
base_url = %q

[actions.ping]
path = "/ping"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state-dir", t.TempDir(), "--format", "body", "demo", "ping"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != "pong" {
		t.Fatalf("unexpected body output: %q", stdout)
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
		t.Fatalf("write profile config: %v", err)
	}
	return dir
}

func runMain(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), exitCode
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
