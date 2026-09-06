//go:build !windows

package dispatch

import "os"

func replaceAvailabilityFile(source, target string) error {
	return os.Rename(source, target)
}
