//go:build unix

package executor

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return killProcessTree(cmd.Process.Pid)
	}
	return cmd
}
