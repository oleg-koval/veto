//go:build !windows

package main

import "os"

func replaceMissionIndex(source, target string) error {
	return os.Rename(source, target)
}
