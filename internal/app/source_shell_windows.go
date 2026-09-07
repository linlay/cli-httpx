//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/linlay/cli-httpx/internal/processgroup"
)

func sourceShellCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	executable := os.Getenv("AP_GIT_BASH_EXE")
	if !filepath.IsAbs(executable) || !strings.EqualFold(filepath.Base(executable), "bash.exe") {
		return nil, fmt.Errorf("Windows from:shell requires the absolute AP_GIT_BASH_EXE supplied by an enabled Platform Git Bash session")
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("AP_GIT_BASH_EXE is unavailable or not a regular file")
	}
	cmd := exec.CommandContext(ctx, executable, "--noprofile", "--norc", "-o", "pipefail", "-c", command)
	cmd.WaitDelay = 250 * time.Millisecond
	return cmd, nil
}

func runSourceShell(cmd *exec.Cmd) error { return processgroup.Run(cmd) }
