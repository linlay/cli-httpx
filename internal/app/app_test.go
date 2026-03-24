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

[profiles.demo]
base_url = "https://example.com"

[profiles.demo.actions.get]
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

func TestCompileMergesDefaultsActionAndCLIOverride(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1

[profiles.demo]
base_url = %q
timeout = "2s"
retries = 1

[profiles.demo.headers]
X-Base = "base"
X-Shared = "profile"

[profiles.demo.query]
scope = "base"
region = "cn"

[profiles.demo.actions.info]
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
		Kind:    commandRun,
		Profile: "demo",
		Action:  "info",
		Options: globalOptions{
			ConfigPath: configPath,
			StateDir:   t.TempDir(),
			Timeout:    3 * time.Second,
			Format:     formatJSON,
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
		"run", "demo", "list",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if req.Options.Params["user"] != "alice" || req.Options.Params["page"] != "2" {
		t.Fatalf("unexpected params: %#v", req.Options.Params)
	}
}

func TestCompileSupportsParamAndLiteralSources(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1

[profiles.demo]
base_url = %q

[profiles.demo.actions.search]
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
		Kind:    commandRun,
		Profile: "demo",
		Action:  "search",
		Options: globalOptions{
			ConfigPath: configPath,
			StateDir:   t.TempDir(),
			Format:     formatJSON,
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

[profiles.demo]
base_url = %q
cookies = { locale = "zh-CN" }

[profiles.demo.actions.me]
path = "/me"
cookies = { session = { from = "state", key = "auth.session" }, mode = { from = "literal", value = "agent" } }
`, server.URL))

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	rt := NewRuntime(ioDiscard{}, ioDiscard{})
	req := commandRequest{
		Kind:    commandRun,
		Profile: "demo",
		Action:  "me",
		Options: globalOptions{
			ConfigPath: configPath,
			StateDir:   t.TempDir(),
			Format:     formatJSON,
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

[profiles.demo]
base_url = %q

[profiles.demo.actions.login]
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
		Kind:    commandRun,
		Profile: "demo",
		Action:  "login",
		Options: globalOptions{
			ConfigPath: configPath,
			StateDir:   t.TempDir(),
			Format:     formatJSON,
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

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1

[profiles.demo]
base_url = %q
login_action = "login"

[profiles.demo.actions.login]
method = "POST"
path = "/login"
form = { username = { from = "env", key = "HTTPX_USER" }, password = { from = "env", key = "HTTPX_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }

[profiles.demo.actions.data]
path = "/data"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
extract = ".body.value"
`, server.URL))

	stateDir := t.TempDir()
	loginStdout, loginStderr, exitCode := runMain(t, []string{"--config", configPath, "--state-dir", stateDir, "login", "demo"})
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

	runStdout, runStderr, exitCode := runMain(t, []string{"--config", configPath, "--state-dir", stateDir, "run", "demo", "data"})
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

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1

[profiles.demo]
base_url = %q
login_action = "login"

[profiles.demo.actions.login]
method = "POST"
path = "/login"
save = { "oem.sessionid" = ".body.session_id" }

[profiles.demo.actions.me]
path = "/me"
cookies = { "oem.sessionid" = { from = "state", key = "oem.sessionid" } }
extract = ".body.ok"
`, server.URL))

	stateDir := t.TempDir()
	_, _, exitCode := runMain(t, []string{"--config", configPath, "--state-dir", stateDir, "login", "demo"})
	if exitCode != ExitSuccess {
		t.Fatalf("login failed with exit=%d", exitCode)
	}

	stdout, stderr, exitCode := runMain(t, []string{"--config", configPath, "--state-dir", stateDir, "run", "demo", "me"})
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

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1

[profiles.demo]
base_url = %q

[profiles.demo.actions.secret]
method = "POST"
path = "/secret"
headers = { Authorization = { from = "env", key = "HTTPX_SECRET" } }
query = { token = { from = "env", key = "HTTPX_SECRET" } }
body = { nested = { from = "env", key = "HTTPX_SECRET" } }
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configPath, "inspect", "demo", "secret"})
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

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1

[profiles.demo]
base_url = %q

[profiles.demo.actions.fail]
path = "/fail"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configPath, "--state-dir", t.TempDir(), "run", "demo", "fail"})
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

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1

[profiles.demo]
base_url = %q

[profiles.demo.actions.ping]
path = "/ping"
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configPath, "--state-dir", t.TempDir(), "--format", "body", "run", "demo", "ping"})
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
