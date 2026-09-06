//go:build !windows

package main

import "os"

func replacePrivateFile(source, target string) error {
	return os.Rename(source, target)
}
