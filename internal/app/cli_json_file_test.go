package app

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParamJSONFilePreservesTypesAndInlineParamsOverride(t *testing.T) {
	t.Parallel()

	path := writeJSONInputFile(t, `{
  "name": "from-file",
  "count": 7,
  "enabled": true,
  "nested": {"source": "file"},
  "items": ["A", "B"],
  "nothing": null
}`)
	req, err := parseArgs([]string{
		"run", "demo", "create",
		"--param", "name=inline",
		"--param", "nested=inline-value",
		"--param-json-file", path,
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	if req.Options.Params["name"] != "inline" || req.Options.Params["nested"] != "inline-value" {
		t.Fatalf("expected inline params to override file values: %#v", req.Options.Params)
	}
	if req.Options.Params["count"] != float64(7) || req.Options.Params["enabled"] != true {
		t.Fatalf("expected typed scalar params: %#v", req.Options.Params)
	}
	items, ok := req.Options.Params["items"].([]any)
	if !ok || len(items) != 2 || items[0] != "A" || items[1] != "B" {
		t.Fatalf("expected typed array param: %#v", req.Options.Params["items"])
	}
	if value, exists := req.Options.Params["nothing"]; !exists || value != nil {
		t.Fatalf("expected explicit null param: %#v", req.Options.Params)
	}
}

func TestExtractJSONFileMergesWithInlineExtract(t *testing.T) {
	t.Parallel()

	path := writeJSONInputFile(t, `{"file_only":{"owner":"A"},"shared":{"source":"file"},"days":30}`)
	req, err := parseArgs([]string{
		"run", "demo", "summary",
		"--extract", `{"shared":{"source":"inline"},"days":7}`,
		"--extract-json-file", path,
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	if req.Options.ExtractInput["days"] != float64(7) {
		t.Fatalf("expected inline extract to override file input: %#v", req.Options.ExtractInput)
	}
	shared, ok := req.Options.ExtractInput["shared"].(map[string]any)
	if !ok || len(shared) != 1 || shared["source"] != "inline" {
		t.Fatalf("expected shallow replacement of nested extract value: %#v", req.Options.ExtractInput)
	}
	fileOnly, ok := req.Options.ExtractInput["file_only"].(map[string]any)
	if !ok || fileOnly["owner"] != "A" {
		t.Fatalf("expected file-only extract value: %#v", req.Options.ExtractInput)
	}
}

func TestJSONFileInputsSupportExplicitStdin(t *testing.T) {
	t.Parallel()

	paramReq, err := parseArgsWithReader(
		[]string{"run", "demo", "create", "--param-json-file", "-"},
		strings.NewReader(`{"payload":{"id":42},"enabled":true}`),
	)
	if err != nil {
		t.Fatalf("parse param stdin failed: %v", err)
	}
	payload, ok := paramReq.Options.Params["payload"].(map[string]any)
	if !ok || payload["id"] != float64(42) || paramReq.Options.Params["enabled"] != true {
		t.Fatalf("unexpected stdin params: %#v", paramReq.Options.Params)
	}

	extractReq, err := parseArgsWithReader(
		[]string{"inspect", "demo", "summary", "--extract-json-file", "-"},
		strings.NewReader(`{"group":["A","B"]}`),
	)
	if err != nil {
		t.Fatalf("parse extract stdin failed: %v", err)
	}
	groups, ok := extractReq.Options.ExtractInput["group"].([]any)
	if !ok || len(groups) != 2 || groups[1] != "B" {
		t.Fatalf("unexpected stdin extract input: %#v", extractReq.Options.ExtractInput)
	}
}

func TestJSONFileInputsRejectConflictingOrUnsupportedStdinBeforeReading(t *testing.T) {
	t.Parallel()

	_, err := parseArgsWithReader(
		[]string{"run", "demo", "summary", "--param-json-file", "-", "--extract-json-file", "-"},
		failingJSONInputReader{},
	)
	if err == nil || !strings.Contains(err.Error(), "cannot both read from stdin") {
		t.Fatalf("expected stdin conflict, got %v", err)
	}

	_, err = parseArgsWithReader(
		[]string{"sites", "--param-json-file", "-"},
		failingJSONInputReader{},
	)
	if err == nil || !strings.Contains(err.Error(), "--param-json-file is not supported with sites") {
		t.Fatalf("expected unsupported command error before stdin read, got %v", err)
	}

	_, err = parseArgsWithReader(
		[]string{"login", "demo", "--extract-json-file", "-"},
		failingJSONInputReader{},
	)
	if err == nil || !strings.Contains(err.Error(), "--extract-json-file is not supported with login") {
		t.Fatalf("expected unsupported login error before stdin read, got %v", err)
	}
}

func TestJSONFileInputValidationErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	invalidJSON := filepath.Join(tmpDir, "invalid.json")
	nonObject := filepath.Join(tmpDir, "array.json")
	empty := filepath.Join(tmpDir, "empty.json")
	trailing := filepath.Join(tmpDir, "trailing.json")
	oversized := filepath.Join(tmpDir, "oversized.json")
	writeTestInput(t, invalidJSON, []byte(`{"value":`))
	writeTestInput(t, nonObject, []byte(`[]`))
	writeTestInput(t, empty, nil)
	writeTestInput(t, trailing, []byte(`{"value":1} trailing`))
	writeTestInput(t, oversized, []byte(`{"value":"`+strings.Repeat("x", maxCLIJSONInputBytes)+`"}`))

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing", args: []string{"run", "demo", "create", "--param-json-file", filepath.Join(tmpDir, "missing.json")}, want: "read --param-json-file"},
		{name: "invalid", args: []string{"run", "demo", "create", "--param-json-file", invalidJSON}, want: "invalid --param-json-file"},
		{name: "array", args: []string{"run", "demo", "create", "--param-json-file", nonObject}, want: "expected a JSON object"},
		{name: "empty", args: []string{"run", "demo", "create", "--param-json-file", empty}, want: "invalid --param-json-file"},
		{name: "trailing", args: []string{"run", "demo", "create", "--extract-json-file", trailing}, want: "invalid --extract-json-file"},
		{name: "oversized", args: []string{"run", "demo", "create", "--param-json-file", oversized}, want: "exceeds 1 MiB"},
		{name: "empty path", args: []string{"run", "demo", "create", "--param-json-file="}, want: "requires a non-empty path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestJSONFileFlagsMayOnlyBeProvidedOnce(t *testing.T) {
	t.Parallel()

	path := writeJSONInputFile(t, `{}`)
	for _, args := range [][]string{
		{"run", "demo", "create", "--param-json-file", path, "--param-json-file", path},
		{"run", "demo", "create", "--extract-json-file", path, "--extract-json-file", path},
	} {
		if _, err := parseArgs(args); err == nil || !strings.Contains(err.Error(), "may only be provided once") {
			t.Fatalf("expected duplicate file flag error for %#v, got %v", args, err)
		}
	}
}

func TestTypedFileParamsCompileAcrossRequestLocationsAndRemainRedacted(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Typed params"
base_url = "https://example.com"

[actions.typed]
description = "Typed JSON body"
method = "POST"
path = "/typed"
query = { limit = { from = "param", key = "limit" } }
body = { nested = { from = "param", key = "nested" }, items = { from = "param", key = "items" }, enabled = { from = "param", key = "enabled" }, nothing = { from = "param", key = "nothing" } }

[actions.form]
description = "Typed form value"
method = "POST"
path = "/form"
form = { filter = { from = "param", key = "filter" } }
`)
	paramsPath := writeJSONInputFile(t, `{
  "limit": 7,
  "nested": {"id":42},
  "items": ["A","B"],
  "enabled": true,
  "nothing": null,
  "filter": {"group":["A","B"]}
}`)
	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	runReq, err := parseArgs([]string{"run", "demo", "typed", "--param-json-file", paramsPath})
	if err != nil {
		t.Fatalf("parse run args: %v", err)
	}
	compiled, _, _, err := NewRuntime(io.Discard, io.Discard).compile(runReq, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile typed action: %v", err)
	}
	if compiled.URL != "https://example.com/typed?limit=7" {
		t.Fatalf("unexpected typed query: %s", compiled.URL)
	}
	body, ok := compiled.Body.(map[string]any)
	if !ok || body["enabled"] != true || body["nothing"] != nil {
		t.Fatalf("unexpected typed body: %#v", compiled.Body)
	}
	nested, ok := body["nested"].(map[string]any)
	if !ok || nested["id"] != float64(42) {
		t.Fatalf("unexpected nested body value: %#v", body["nested"])
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 || items[1] != "B" {
		t.Fatalf("unexpected array body value: %#v", body["items"])
	}

	formReq, err := parseArgs([]string{"run", "demo", "form", "--param-json-file", paramsPath})
	if err != nil {
		t.Fatalf("parse form args: %v", err)
	}
	compiledForm, _, _, err := NewRuntime(io.Discard, io.Discard).compile(formReq, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile form action: %v", err)
	}
	formBody, ok := compiledForm.Body.(map[string]string)
	if !ok || formBody["filter"] != `{"group":["A","B"]}` {
		t.Fatalf("unexpected typed form body: %#v", compiledForm.Body)
	}

	inspectReq, err := parseArgs([]string{"inspect", "demo", "typed", "--param-json-file", paramsPath})
	if err != nil {
		t.Fatalf("parse inspect args: %v", err)
	}
	inspected, _, _, err := NewRuntime(io.Discard, io.Discard).compile(inspectReq, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile inspect action: %v", err)
	}
	inspectBody, ok := inspected.Body.(map[string]any)
	if !ok || inspectBody["nested"] != redactedValue || inspectBody["items"] != redactedValue || inspectBody["nothing"] != redactedValue {
		t.Fatalf("expected file params to remain redacted: %#v", inspected.Body)
	}
}

func TestExtractJSONFileInputsReachJQAndRegexExtractors(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, `
version = 1
description = "Extractor file inputs"
base_url = "https://example.com"

[actions.jq]
description = "JQ input"
path = "/jq"
extract_type = "jq"
extract_expr = ".extract.file_only + \"-\" + .extract.shared"

[actions.regex]
description = "Regex input"
path = "/regex"
extract_type = "regex"
extract_pattern = "group=({{extract.group}})"
extract_group = 1
`)
	extractPath := writeJSONInputFile(t, `{"file_only":"file","shared":"file","group":"A"}`)
	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	jqReq, err := parseArgs([]string{
		"run", "demo", "jq",
		"--extract-json-file", extractPath,
		"--extract", `{"shared":"inline"}`,
	})
	if err != nil {
		t.Fatalf("parse jq args: %v", err)
	}
	jqCompiled, _, _, err := NewRuntime(io.Discard, io.Discard).compile(jqReq, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile jq action: %v", err)
	}
	jqContext := extractorContext(200, nil, map[string]any{"ok": true}, []byte(`{"ok":true}`), jqCompiled.ExtractInput)
	jqOutput, err := executeExtractor(jqCompiled.compiledExtractor, jqContext, []byte(`{"ok":true}`))
	if err != nil || jqOutput != "file-inline" {
		t.Fatalf("unexpected jq output %#v, error %v", jqOutput, err)
	}

	regexReq, err := parseArgs([]string{
		"run", "demo", "regex",
		"--extract-json-file", extractPath,
		"--extract", `{"group":"B"}`,
	})
	if err != nil {
		t.Fatalf("parse regex args: %v", err)
	}
	regexCompiled, _, _, err := NewRuntime(io.Discard, io.Discard).compile(regexReq, cfg, &profileState{Values: map[string]string{}})
	if err != nil {
		t.Fatalf("compile regex action: %v", err)
	}
	rawBody := []byte("group=A group=B")
	regexContext := extractorContext(200, nil, string(rawBody), rawBody, regexCompiled.ExtractInput)
	regexOutput, err := executeExtractor(regexCompiled.compiledExtractor, regexContext, rawBody)
	if err != nil || regexOutput != "B" {
		t.Fatalf("unexpected regex output %#v, error %v", regexOutput, err)
	}
}

func parseArgsWithReader(args []string, stdin io.Reader) (commandRequest, error) {
	var (
		req      commandRequest
		captured bool
	)
	root := newRootCommand(stdin, io.Discard, io.Discard, func(next commandRequest) int {
		req = next
		captured = true
		return ExitSuccess
	})
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return commandRequest{}, err
	}
	if !captured {
		return commandRequest{}, flag.ErrHelp
	}
	return req, nil
}

func writeJSONInputFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.json")
	writeTestInput(t, path, []byte(content))
	return path
}

func writeTestInput(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write JSON input: %v", err)
	}
}

type failingJSONInputReader struct{}

func (failingJSONInputReader) Read([]byte) (int, error) {
	return 0, errors.New("stdin should not be read")
}
