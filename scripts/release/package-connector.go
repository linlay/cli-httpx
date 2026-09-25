// Command package-connector builds a deterministic, complete Platform connector archive.
package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	root := flag.String("root", ".", "project root")
	binary := flag.String("binary", "", "compiled CLI path")
	goos := flag.String("os", "", "target OS")
	arch := flag.String("arch", "", "target architecture")
	flag.Parse()
	if err := build(*root, *binary, *goos, *arch); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func build(root, binary, goos, arch string) error {
	switch goos + "/" + arch {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64":
	default:
		return fmt.Errorf("unsupported target %s/%s", goos, arch)
	}

	v, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return err
	}
	version := strings.TrimSpace(string(v))
	semver := strings.TrimPrefix(version, "v")
	readJSON := func(name string, dst any) error {
		b, e := os.ReadFile(filepath.Join(root, "connector", name))
		if e != nil {
			return e
		}
		return json.Unmarshal(b, dst)
	}
	var manifest map[string]any
	if err = readJSON("connector.json", &manifest); err != nil {
		return err
	}
	id, ok := manifest["id"].(string)
	if !ok || (id != "builtin.dbx" && id != "builtin.httpx") {
		return fmt.Errorf("invalid connector id")
	}
	name := strings.TrimPrefix(id, "builtin.")
	var cli struct {
		VersionCheck struct {
			MinVersion string `json:"minVersion"`
		} `json:"versionCheck"`
	}
	if err = readJSON("cli.json", &cli); err != nil {
		return err
	}
	if !atLeast(semver, cli.VersionCheck.MinVersion) {
		return fmt.Errorf("CLI %s below minVersion %s", semver, cli.VersionCheck.MinVersion)
	}
	manifest["version"] = semver
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(filepath.Join(root, "connector"), func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Name() == ".DS_Store" || d.Name() == "Thumbs.db" {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular connector resource: %s", p)
		}
		rel, e := filepath.Rel(filepath.Join(root, "connector"), p)
		if e != nil {
			return e
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		return err
	}
	files["connector.json"] = append(manifestBytes, '\n')
	exe := name
	if goos == "windows" {
		exe += ".exe"
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	files["bin/"+exe] = b
	dist := filepath.Join(root, "dist", version)
	if err = os.MkdirAll(dist, 0755); err != nil {
		return err
	}
	out := filepath.Join(dist, fmt.Sprintf("%s_%s_%s_%s.zip", id, version, goos, arch))
	tmp, err := os.CreateTemp(dist, ".connector-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	zw := zip.NewWriter(tmp)
	// Map iteration is sorted so builds with identical inputs have identical digests.
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := &zip.FileHeader{Name: "runtime/connectors/" + id + "/" + n, Method: zip.Deflate}
		h.SetMode(0644)
		if n == "bin/"+exe {
			h.SetMode(0755)
		}
		w, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		if _, e = w.Write(files[n]); e != nil {
			return e
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), out)
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func atLeast(actual, minimum string) bool {
	parse := func(v string) []string {
		m := versionPattern.FindStringSubmatch(strings.TrimPrefix(v, "v"))
		if m == nil {
			return nil
		}
		if m[4] != "" {
			for _, part := range strings.Split(m[4], ".") {
				if numeric(part) && len(part) > 1 && part[0] == '0' {
					return nil
				}
			}
		}
		return m
	}
	a, b := parse(actual), parse(minimum)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		if cmp := compareNumber(a[i], b[i]); cmp != 0 {
			return cmp > 0
		}
	}
	if a[4] == b[4] {
		return true
	}
	if a[4] == "" {
		return true
	}
	if b[4] == "" {
		return false
	}
	ap, bp := strings.Split(a[4], "."), strings.Split(b[4], ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] == bp[i] {
			continue
		}
		an, bn := numeric(ap[i]), numeric(bp[i])
		if an && bn {
			return compareNumber(ap[i], bp[i]) > 0
		}
		if an != bn {
			return !an
		}
		return ap[i] > bp[i]
	}
	return len(ap) >= len(bp)
}
func numeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}
func compareNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}
