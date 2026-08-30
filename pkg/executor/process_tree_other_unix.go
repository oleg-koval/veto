//go:build unix && !darwin && !linux

package executor

import (
	"errors"
	"os"
	"syscall"
)

func killProcessTree(root int) error {
	err := syscall.Kill(-root, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
