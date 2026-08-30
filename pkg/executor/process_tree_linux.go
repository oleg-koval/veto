//go:build linux

package executor

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func killProcessTree(root int) error {
	_ = syscall.Kill(-root, syscall.SIGSTOP)
	if children, err := linuxProcessChildren(); err == nil {
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

func linuxProcessChildren() (map[int][]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	children := make(map[int][]int)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		closingParen := strings.LastIndexByte(string(data), ')')
		if closingParen < 0 {
			continue
		}
		fields := strings.Fields(string(data[closingParen+1:]))
		if len(fields) < 2 {
			continue
		}
		parent, err := strconv.Atoi(fields[1])
		if err == nil {
			children[parent] = append(children[parent], pid)
		}
	}
	return children, nil
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
