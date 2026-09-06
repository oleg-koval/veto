//go:build !windows

package routinghistory

import "os"

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
