//go:build unix

package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeCLIExecutionTimeoutKillsSpawnedProcessGroup(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\n\"$VETO_HELPER_BINARY\" -test.run=^TestEscapedCLIChild$ >/dev/null 2>&1 &\nwait\n"), 0700))
	t.Setenv("VETO_CHILD_PID_FILE", childPIDFile)
	t.Setenv("VETO_HELPER_BINARY", os.Args[0])
	t.Setenv("VETO_ESCAPED_CLI_CHILD", "1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan Result, 1)
	go func() {
		resultCh <- (&CLIExecutor{binary: script, model: "test"}).Execute(ctx, "prompt", ExecutionOptions{})
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(childPIDFile)
		return err == nil
	}, 5*time.Second, 25*time.Millisecond)
	cancel()

	var result Result
	select {
	case result = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("CLI execution did not return after cancellation")
	}
	require.Error(t, result.Error)

	data, err := os.ReadFile(childPIDFile)
	require.NoError(t, err, "execution result: %v", result.Error)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	assert.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, 3*time.Second, 50*time.Millisecond, "timed-out CLI descendants must not survive Veto")
}

func TestEscapedCLIChild(t *testing.T) {
	if os.Getenv("VETO_ESCAPED_CLI_CHILD") != "1" {
		return
	}
	_, err := syscall.Setsid()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv("VETO_CHILD_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0600))
	time.Sleep(5 * time.Minute)
}
