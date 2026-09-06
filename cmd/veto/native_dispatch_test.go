package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/pkg/dispatch"
	"github.com/oleg-koval/veto/pkg/executor"
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
	code := runStart([]string{"--agent", "claude", "--no-feedback", "fix parser; keep prompt one arg"}, strings.NewReader(""), &output, &diagnostics, executor.NewNativeLauncher(), dispatch.NewAvailabilityStore(filepath.Join(home, ".veto", "unavailable.json")))
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
	code := runStart([]string{"--agent", "claude", "task"}, strings.NewReader(""), &output, &diagnostics, executor.NewNativeLauncher(), dispatch.NewAvailabilityStore(filepath.Join(t.TempDir(), "unavailable.json")))
	require.Equal(t, 23, code)
}
