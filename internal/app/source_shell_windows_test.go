//go:build windows

package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWindowsShellRequiresExplicitLocator(t *testing.T) {
	for _, value := range []string{"", "bash.exe", `C:\missing\bash.exe`} {
		t.Setenv("AP_GIT_BASH_EXE", value)
		if _, err := sourceShellCommand(context.Background(), "true"); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	executable := filepath.Join(t.TempDir(), "bash.exe")
	if err := os.WriteFile(executable, []byte("test only"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AP_GIT_BASH_EXE", executable)
	cmd, err := sourceShellCommand(context.Background(), "echo ok")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{executable, "--noprofile", "--norc", "-o", "pipefail", "-c", "echo ok"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("args: %v", cmd.Args)
	}
}

func TestWindowsPortableShellNative(t *testing.T) {
	executable := os.Getenv("AP_TEST_GIT_BASH_EXE")
	if executable == "" {
		t.Skip("native integration requires AP_TEST_GIT_BASH_EXE; cross compilation is not validation")
	}
	t.Setenv("AP_GIT_BASH_EXE", executable)
	t.Setenv("MSYS2_ARG_CONV_EXCL", "*")
	t.Setenv("MSYS_NO_PATHCONV", "1")
	r := resolver{reveal: true}
	got, err := r.resolveSource(context.Background(), sourceSpec{From: "shell", Cmd: "printf '%s' '中文 /api/items {\"id\":1}'"})
	if err != nil || got != "中文 /api/items {\"id\":1}" {
		t.Fatalf("shell output=%v err=%v", got, err)
	}
	if _, err := r.resolveSource(context.Background(), sourceSpec{From: "shell", Cmd: "false | true"}); err == nil {
		t.Fatal("pipefail was not enforced")
	}
	started := time.Now()
	if _, err := r.resolveSource(context.Background(), sourceSpec{From: "shell", Cmd: "sleep 30 & wait", TimeoutMS: 200}); err == nil {
		t.Fatal("timeout succeeded")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("nested shell timeout did not close child pipes promptly")
	}
}
