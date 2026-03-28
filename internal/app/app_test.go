package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		"--extract", `{"days":7,"group":["WRM","OFFICE"]}`,
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if value, ok := req.Options.ExtractInput["days"].(float64); !ok || value != 7 {
		t.Fatalf("unexpected extract input: %#v", req.Options.ExtractInput)
	}
	groups, ok := req.Options.ExtractInput["group"].([]any)
	if !ok || len(groups) != 2 || groups[0] != "WRM" || groups[1] != "OFFICE" {
		t.Fatalf("unexpected extract input groups: %#v", req.Options.ExtractInput)
	}
}

func TestParseArgsDefaultsByCommand(t *testing.T) {
	t.Parallel()

	runReq, err := parseArgs([]string{"run", "demo", "list"})
	if err != nil {
		t.Fatalf("parseArgs run failed: %v", err)
	}
	if runReq.Options.Format != formatBody {
		t.Fatalf("expected run default format body, got %q", runReq.Options.Format)
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

func TestDefaultStateDirUsesLocalHTTPXStateWithoutXDG(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_STATE_HOME", "")

	got := defaultStateDir()
	want := filepath.Join(homeDir, ".local", "httpx-state")
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
		{"run", "--format", "text", "demo", "list"},
		{"run", "demo", "list", "--extract", `[]`},
		{"run", "demo", "list", "--extract", `{"days":`},
		{"run", "demo", "list", "--extract", `{"days":7}`, "--extract", `{"group":"WRM"}`},
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
	t.Setenv("XDG_CONFIG_HOME", "")

	if got := defaultConfigDir(); got != "/root/.config/httpx" {
		t.Fatalf("unexpected default config dir: %q", got)
	}
}

func TestDefaultStateDirUsesHomeLocalHTTPXState(t *testing.T) {
	t.Setenv("HOME", "/root")
	t.Setenv("XDG_STATE_HOME", "")

	if got := defaultStateDir(); got != "/root/.local/httpx-state" {
		t.Fatalf("unexpected default state dir: %q", got)
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
			Params: map[string]string{
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
			Params: map[string]string{
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
	t.Setenv("HTTPX_PROXY", "http://127.0.0.1:8001")

	configPath := writeConfig(t, fmt.Sprintf(`
version = 1
description = "Demo site"
base_url = %q
proxy = { from = "env", key = "HTTPX_PROXY" }

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
form = { data = { UserId = "alice", Password = "secret", SystemType = "100", ClientType = "2" } }
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

func TestRunUsesDefaultSiteConfigPath(t *testing.T) {
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
description = "Demo site"
base_url = %q
login_action = "login_request"

[actions.login_request]
description = "Sign in"
method = "POST"
path = "/login"
form = { username = { from = "env", key = "HTTPX_USER" }, password = { from = "env", key = "HTTPX_PASS" } }
save = { "auth.authorization" = "\"Bearer \" + .body.token" }

[actions.data]
description = "Load data"
path = "/data"
headers = { Authorization = { from = "state", key = "auth.authorization" } }
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
	if state.Values["auth.authorization"] != "Bearer token-123" {
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
	if value, ok := env.Extract.(float64); !ok || value != 42 {
		t.Fatalf("unexpected extract: %#v", env.Extract)
	}
	if env.Body != nil {
		t.Fatalf("expected body to be omitted when extractor is configured, got %#v", env.Body)
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
description = "Demo site"
base_url = %q
login_action = "login_request"

[actions.login_request]
description = "Sign in"
method = "POST"
path = "/login"
save = { "oem.sessionid" = ".body.session_id" }

[actions.me]
description = "Load profile"
path = "/me"
cookies = { "oem.sessionid" = { from = "state", key = "oem.sessionid" } }
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

	var env envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("unmarshal run output: %v", err)
	}
	if ok, _ := env.Extract.(bool); !ok {
		t.Fatalf("unexpected extract: %#v", env.Extract)
	}
	if env.Body != nil {
		t.Fatalf("expected body to be omitted when extractor is configured, got %#v", env.Body)
	}
}

func TestInspectRedactsDynamicValues(t *testing.T) {
	t.Setenv("HTTPX_SECRET", "secret-token")

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
headers = { Authorization = { from = "env", key = "HTTPX_SECRET" } }
query = { token = { from = "env", key = "HTTPX_SECRET" } }
body = { nested = { from = "env", key = "HTTPX_SECRET" } }
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

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "body", "run", "demo", "ping"})
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

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "body", "run", "demo", "ping"})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != "pong" {
		t.Fatalf("unexpected body output: %q", stdout)
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

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "body", "run", "demo", "repo"})
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
		"--format", "body",
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

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "body", "run", "demo", "ids"})
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
		_, _ = w.Write([]byte("group=WRM group=OFFICE"))
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
		"--format", "body",
		"run", "demo", "ids",
		"--extract", `{"group":"OFFICE"}`,
	})
	if exitCode != ExitSuccess {
		t.Fatalf("expected success, got %d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if stdout != `OFFICE` {
		t.Fatalf("unexpected regex extractor output: %q", stdout)
	}
}

func TestBodyFormatRegexExtractorFailsWhenExtractInputMissing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("group=WRM"))
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
		"--format", "body",
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

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "--state", t.TempDir(), "--format", "body", "run", "demo", "ids"})
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
extracts = [{ name = "group", type = "string", description = "Exact group match", example = "WRM" }]
extract_type = "regex"
extract_pattern = "token=([A-Za-z0-9_-]+)"
extract_group = 1
`, server.URL))

	stdout, stderr, exitCode := runMain(t, []string{"--config", configDir, "inspect", "demo", "secret", "--extract", `{"group":"WRM"}`})
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
	if compiled.ExtractInput["group"] != "WRM" {
		t.Fatalf("unexpected extract input: %#v", compiled.ExtractInput)
	}
	if len(compiled.Params) != 1 || compiled.Params[0].Name != "id" {
		t.Fatalf("unexpected params metadata: %#v", compiled.Params)
	}
	if len(compiled.Extracts) != 1 || compiled.Extracts[0].Name != "group" {
		t.Fatalf("unexpected extracts metadata: %#v", compiled.Extracts)
	}
}

func TestDiscoveryCommandsExposeSummariesOnly(t *testing.T) {
	configDir := t.TempDir()
	stateDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(configDir, "alpha.toml"), []byte(strings.TrimSpace(`
version = 1
description = "Alpha site"
base_url = "https://alpha.example.com"
login_action = "signin"

[actions.signin]
description = "Sign in"
method = "POST"
path = "/login"

[actions.profile]
description = "Load profile"
path = "/me"
extracts = [{ name = "group", type = "string", description = "Filter group", example = "WRM" }]
`)+"\n"), 0o600); err != nil {
		t.Fatalf("write alpha config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "beta.toml"), []byte(strings.TrimSpace(`
version = 1
description = "Beta site"
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
	if !strings.Contains(stdout, "alpha") || !strings.Contains(stdout, "Alpha site") || !strings.Contains(stdout, "yes") {
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
	if siteResp.Site.LoginAction != "signin" {
		t.Fatalf("unexpected login_action in site response: %#v", siteResp)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "actions", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("actions failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	if !strings.Contains(stdout, "signin") || !strings.Contains(stdout, "profile") {
		t.Fatalf("unexpected actions output: %q", stdout)
	}
	if !strings.Contains(stdout, "PARAMS") || !strings.Contains(stdout, "EXTRACTS") {
		t.Fatalf("expected params/extracts columns in actions output: %q", stdout)
	}
	if strings.Contains(stdout, "LOGIN") || strings.Contains(stdout, "yes") || strings.Contains(stdout, "no") {
		t.Fatalf("unexpected login marker in actions output: %q", stdout)
	}

	stdout, stderr, exitCode = runMain(t, []string{"--config", configDir, "--state", stateDir, "--format", "json", "actions", "alpha"})
	if exitCode != ExitSuccess {
		t.Fatalf("actions json failed: exit=%d stderr=%s stdout=%s", exitCode, stderr, stdout)
	}
	var actionsResp map[string]any
	if err := json.Unmarshal([]byte(stdout), &actionsResp); err != nil {
		t.Fatalf("unmarshal actions output: %v", err)
	}
	actions, ok := actionsResp["actions"].([]any)
	if !ok || len(actions) != 2 {
		t.Fatalf("unexpected actions response: %#v", actionsResp)
	}
	for _, item := range actions {
		action, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected action item: %#v", item)
		}
		if _, exists := action["is_login_action"]; exists {
			t.Fatalf("unexpected is_login_action field: %#v", action)
		}
	}
	foundProfile := false
	for _, item := range actions {
		action := item.(map[string]any)
		if action["name"] == "profile" {
			foundProfile = true
			if action["extracts"] != float64(1) {
				t.Fatalf("expected extract count for profile action: %#v", action)
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
	if actionResp.Action.Name != "profile" || len(actionResp.Action.Extracts) != 1 || actionResp.Action.Extracts[0].Name != "group" {
		t.Fatalf("unexpected action response: %#v", actionResp)
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
