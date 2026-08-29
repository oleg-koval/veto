//go:build windows

package main

import "os"

func doctorFileOwnedByCurrentUser(os.FileInfo) (bool, bool) {
	return false, false
}
