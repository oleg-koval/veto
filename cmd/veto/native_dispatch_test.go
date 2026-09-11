package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/dispatch"
	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStartForwardsPromptAndEnvironmentWithoutLoggingPrompt(t *testing.T) {
	bin := t.TempDir()
	home := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args")
	envPath := filepath.Join(t.TempDir(), "env")
	script := filepath.Join(bin, "claude")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+argsPath+"\"\n[ \"$ANTHROPIC_API_KEY\" = sk-secret ] && printf preserved > \""+envPath+"\"\nprintf native-output\n"), 0700))
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	eventLedger = nil
	experimentLedger = nil
	eventRunID = ""
	var output, diagnostics bytes.Buffer
	code := runStart(context.Background(), []string{"--agent", "claude", "--no-feedback", "fix parser; keep prompt one arg"}, strings.NewReader(""), &output, &diagnostics, executor.NewNativeLauncher(), dispatch.NewAvailabilityStore(filepath.Join(home, ".veto", "unavailable.json")))
	require.Equal(t, 0, code, diagnostics.String())
	require.Contains(t, output.String(), "native-output")
	args, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	require.Equal(t, "fix parser; keep prompt one arg\n", string(args))
	_, err = os.Stat(envPath)
	require.NoError(t, err, "native environment should be inherited")
	log, err := os.ReadFile(filepath.Join(home, ".veto", "experiment.log"))
	require.NoError(t, err)
	require.NotContains(t, string(log), "fix parser; keep prompt one arg")
}

func TestRunStartPropagatesNativeExitCode(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "claude")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nexit 23\n"), 0700))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", bin)
	var output, diagnostics bytes.Buffer
	code := runStart(context.Background(), []string{"--agent", "claude", "task"}, strings.NewReader(""), &output, &diagnostics, executor.NewNativeLauncher(), dispatch.NewAvailabilityStore(filepath.Join(t.TempDir(), "unavailable.json")))
	require.Equal(t, 23, code)
}

func TestRunStartCancellationStopsNativeProcess(t *testing.T) {
	bin := t.TempDir()
	home := t.TempDir()
	startedPath := filepath.Join(t.TempDir(), "started")
	script := filepath.Join(bin, "claude")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1 $2 $3\" = \"auth status --json\" ]; then printf '%s\\n' '{\"loggedIn\":false}'; exit 0; fi\n: > \"$VETO_TEST_STARTED\"\n/bin/sleep 30\n"), 0700))
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("VETO_TEST_STARTED", startedPath)
	require.NoError(t, resetExperimentLogger())
	t.Cleanup(func() { _ = resetExperimentLogger() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- runStart(ctx, []string{"--agent", "claude", "--no-feedback", "task"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, executor.NewNativeLauncher(), dispatch.NewAvailabilityStore(filepath.Join(home, ".veto", "unavailable.json")))
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(startedPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := os.Stat(startedPath)
	require.NoError(t, err, "native process did not start")
	cancel()
	select {
	case code := <-done:
		require.NotZero(t, code)
	case <-time.After(5 * time.Second):
		t.Fatal("native process did not stop after cancellation")
	}
}

func TestNativeAgentStatusesDiscoversClaudeCLIAuthentication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf '%s\\n' '{\"loggedIn\":true,\"authMethod\":\"claude.ai\",\"subscriptionType\":\"pro\"}'\n"), 0700))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", bin)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_SUBSCRIPTION", "")

	statuses := nativeAgentStatuses(context.Background(), dispatch.NewAvailabilityStore(filepath.Join(t.TempDir(), "unavailable.json")))
	var claude dispatch.AgentStatus
	for _, status := range statuses {
		if status.Name == "claude" {
			claude = status
			break
		}
	}
	assert.Equal(t, dispatch.AuthAuthenticated, claude.Auth)
	assert.True(t, claude.Installed)
}

func TestNativeAgentStatusesDoesNotTreatStoredAPIKeyAsInheritedConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf '%s\\n' '{\"loggedIn\":true,\"authMethod\":\"claude.ai\",\"subscriptionType\":\"pro\"}'\n"), 0700))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", bin)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_SUBSCRIPTION", "")
	require.NoError(t, saveCredential("ANTHROPIC_API_KEY", "stored-key"))

	statuses := nativeAgentStatuses(context.Background(), dispatch.NewAvailabilityStore(filepath.Join(t.TempDir(), "unavailable.json")))
	var claude dispatch.AgentStatus
	for _, status := range statuses {
		if status.Name == "claude" {
			claude = status
			break
		}
	}
	assert.Equal(t, dispatch.AuthAuthenticated, claude.Auth)
	assert.NotContains(t, claude.Warning, "both present")
}

func TestNativeAgentStatusesMarksLoggedOutClaudeUnauthenticated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf '%s\\n' '{\"loggedIn\":false}'\n"), 0700))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", bin)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_SUBSCRIPTION", "")

	statuses := nativeAgentStatuses(context.Background(), dispatch.NewAvailabilityStore(filepath.Join(t.TempDir(), "unavailable.json")))
	var claude dispatch.AgentStatus
	for _, status := range statuses {
		if status.Name == "claude" {
			claude = status
			break
		}
	}
	assert.Equal(t, dispatch.AuthUnauthenticated, claude.Auth)
}

func TestNativeProposalRejectsUnsupportedOverrideModel(t *testing.T) {
	statuses := []dispatch.AgentStatus{{Name: "claude", Installed: true, Auth: dispatch.AuthAuthenticated}, {Name: "codex", Installed: true, Auth: dispatch.AuthAuthenticated}}
	_, _, err := nativeProposal(map[string]string{"choose": "agent", "objective": "fix code", "override-agent": "claude", "override-model": "gpt-5-codex"}, statuses)
	require.ErrorContains(t, err, `model "gpt-5-codex" is not known to be supported by claude`)
}
