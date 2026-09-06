//go:build !unix

package executor

func nativeProcessAlive(int) bool { return false }
