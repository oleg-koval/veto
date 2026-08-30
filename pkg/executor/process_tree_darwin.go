//go:build darwin

package executor

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func killProcessTree(root int) error {
	_ = syscall.Kill(-root, syscall.SIGSTOP)
	processes, snapshotErr := unix.SysctlKinfoProcSlice("kern.proc.all")
	if snapshotErr == nil {
		children := make(map[int][]int)
		for _, process := range processes {
			children[int(process.Eproc.Ppid)] = append(children[int(process.Eproc.Ppid)], int(process.Proc.P_pid))
		}
		for _, pid := range descendantPIDs(root, children) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	err := syscall.Kill(-root, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

func descendantPIDs(root int, children map[int][]int) []int {
	var descendants []int
	var visit func(int)
	visit = func(parent int) {
		for _, child := range children[parent] {
			visit(child)
			descendants = append(descendants, child)
		}
	}
	visit(root)
	return descendants
}
