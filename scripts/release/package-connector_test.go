package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestMinimumVersion(t *testing.T) {
	for _, tc := range []struct {
		actual, minimum string
		want            bool
	}{
		{"0.1.3", "0.1.2", true}, {"0.1.1", "0.1.2", false}, {"1.0.0-beta.1", "1.0.0", false},
		{"1.0.0", "1.0.0-rc.1", true}, {"1.0.0-beta.10", "1.0.0-beta.2", true},
		{"1.0.0-1", "1.0.0-alpha", false}, {"1.0.0+build.2", "1.0.0+build.1", true},
		{"01.0.0", "1.0.0", false}, {"1.0.0-01", "1.0.0-alpha", false},
	} {
		if got := atLeast(tc.actual, tc.minimum); got != tc.want {
			t.Fatalf("%s >= %s: %v", tc.actual, tc.minimum, got)
		}
	}
}

func TestCompletePackageIsDeterministicAndVersioned(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("VERSION", "v1.2.3\n")
	write("connector/connector.json", `{"id":"builtin.dbx","name":"DBX","type":"cli","auth_mode":null}`)
	write("connector/cli.json", `{"versionCheck":{"minVersion":"1.2.0"}}`)
	write("connector/skills/usage/SKILL.md", "original skill bytes")
	write("connector/skills/usage/references/example.md", "reference bytes")
	write("compiled-cli", "binary bytes")
	if err := build(root, filepath.Join(root, "compiled-cli"), "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "dist/v1.2.3/builtin.dbx_v1.2.3_windows_amd64.zip")
	first, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	for _, f := range reader.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[f.Name] = data
	}
	reader.Close()
	prefix := "runtime/connectors/builtin.dbx/"
	for path, want := range map[string]string{"bin/dbx.exe": "binary bytes", "skills/usage/SKILL.md": "original skill bytes", "skills/usage/references/example.md": "reference bytes"} {
		if string(contents[prefix+path]) != want {
			t.Fatalf("missing/changed %s", path)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(contents[prefix+"connector.json"], &m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != "1.2.3" {
		t.Fatalf("version: %v", m)
	}
	if err := build(root, filepath.Join(root, "compiled-cli"), "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(archive)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("non-deterministic archive: %v", err)
	}
	write("VERSION", "v1.1.0")
	if err := build(root, filepath.Join(root, "compiled-cli"), "windows", "amd64"); err == nil {
		t.Fatal("incompatible CLI accepted")
	}
}
