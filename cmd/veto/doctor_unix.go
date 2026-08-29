//go:build !windows

package main

import (
	"os"
	"syscall"
)

func doctorFileOwnedByCurrentUser(info os.FileInfo) (bool, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, false
	}
	return stat.Uid == uint32(os.Getuid()), true
}

func doctorStatePermissionsSupported() bool { return true }
