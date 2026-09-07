//go:build !windows

package app

import (
	"context"
	"os/exec"
)

func sourceShellCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "/bin/sh", "-lc", command), nil
}

func runSourceShell(cmd *exec.Cmd) error { return cmd.Run() }
