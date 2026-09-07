//go:build !windows

package app

import (
	"context"
	"reflect"
	"testing"
)

func TestUnixSourceShellUnchanged(t *testing.T) {
	t.Setenv("AP_GIT_BASH_EXE", "not-used-on-Unix")
	cmd, err := sourceShellCommand(context.Background(), "printf ok")
	if err != nil || !reflect.DeepEqual(cmd.Args, []string{"/bin/sh", "-lc", "printf ok"}) {
		t.Fatalf("Unix invocation: %v %v", cmd, err)
	}
	got, err := (resolver{reveal: true}).resolveSource(context.Background(), sourceSpec{From: "shell", Cmd: "printf ok"})
	if err != nil || got != "ok" {
		t.Fatalf("Unix execution: %v %v", got, err)
	}
}
