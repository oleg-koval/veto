package executor

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeLauncherBuildsArgumentArrays(t *testing.T) {
	var gotName string
	var gotArgs []string
	launcher := &NativeLauncher{
		lookup: func(name string) (string, error) { return "/tmp/" + name, nil },
		command: func(_ context.Context, name string, args ...string) *exec.Cmd {
			gotName, gotArgs = name, args
			return exec.Command("true")
		},
	}
	if _, err := launcher.Command(context.Background(), "codex", "gpt-5-codex", "fix; do not shell expand"); err != nil {
		t.Fatal(err)
	}
	if gotName != "/tmp/codex" || !reflect.DeepEqual(gotArgs, []string{"exec", "--model", "gpt-5-codex", "--", "fix; do not shell expand"}) {
		t.Fatalf("command = %s %#v", gotName, gotArgs)
	}
}

func TestNativeLauncherReportsMissingExecutable(t *testing.T) {
	launcher := NewNativeLauncher()
	launcher.lookup = func(string) (string, error) { return "", exec.ErrNotFound }
	if _, err := launcher.Command(context.Background(), "claude", "", "task"); err == nil {
		t.Fatal("expected missing executable error")
	}
}

func TestNativeLauncherPreservesWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess fixture")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\npwd\n"), 0700); err != nil {
		t.Fatal(err)
	}
	launcher := NewNativeLauncher()
	launcher.lookup = func(string) (string, error) { return script, nil }
	cmd, err := launcher.Command(context.Background(), "claude", "", "task")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(output.String()); got != workingDirectory {
		t.Fatalf("native child cwd = %q, want %q", got, workingDirectory)
	}
}

func TestNativeLauncherCancellationTerminatesChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group fixture")
	}
	bin := t.TempDir()
	script := filepath.Join(bin, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	launcher := NewNativeLauncher()
	launcher.lookup = func(string) (string, error) { return script, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, err := launcher.Command(ctx, "claude", "", "task")
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	cancel()
	err = cmd.Wait()
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("cancelled native command err=%v duration=%s", err, time.Since(started))
	}
}
