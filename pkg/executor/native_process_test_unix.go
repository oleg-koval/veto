//go:build unix

package executor

import "syscall"

func nativeProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
