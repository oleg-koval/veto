//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockMissionStoreFile acquires a blocking, cross-process exclusive advisory
// lock on the mission index's sidecar lock file. Closing the returned file
// releases the lock.
func lockMissionStoreFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	overlapped := windows.Overlapped{}
	const lockFullFile = ^uint32(0)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, lockFullFile, lockFullFile, &overlapped); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
