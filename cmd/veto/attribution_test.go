package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVetoAttributionTextIsStableAndSearchable(t *testing.T) {
	for _, format := range []string{"commit", "pr"} {
		t.Run(format, func(t *testing.T) {
			text, err := attributionText(format)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "🛡️ Built with [Veto](https://github.com/oleg-koval/veto)") {
				t.Fatalf("attribution text does not contain the Veto footer: %q", text)
			}
			if !strings.Contains(text, "Veto-Assisted: true") {
				t.Fatalf("attribution text does not contain the searchable marker: %q", text)
			}
		})
	}
}

func TestVetoAttributionTextRejectsUnknownFormat(t *testing.T) {
	if _, err := attributionText("issue"); err == nil {
		t.Fatal("attributionText(issue) succeeded; want an error")
	}
}

func TestVetoGitHookScriptAddsAttributionOnlyAfterRouting(t *testing.T) {
	script := vetoGitHookScript()
	for _, want := range []string{
		"git diff --cached --stat",
		"🛡️ Built with [Veto](https://github.com/oleg-koval/veto)",
		"Veto-Assisted: true",
		"# veto suggested model:",
		"grep -q '^Veto-Assisted: true$'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("hook script does not contain %q:\n%s", want, script)
		}
	}
}

func TestVetoGitHookScriptAnnotatesSuccessfulRouteWithoutDuplicates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("prepare-commit-msg is a POSIX shell hook")
	}

	dir := t.TempDir()
	fakeVeto := filepath.Join(dir, "veto")
	fakeScript := "#!/bin/sh\nprintf '%s\\n' 'sonnet'\n"
	if err := os.WriteFile(fakeVeto, []byte(fakeScript), 0755); err != nil {
		t.Fatal(err)
	}
	message := filepath.Join(dir, "message")
	if err := os.WriteFile(message, []byte("Add the feature\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(dir, "prepare-commit-msg")
	if err := os.WriteFile(hook, []byte(vetoGitHookScript()), 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", hook, message, "message")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v\n%s", err, output)
	}
	first, err := os.ReadFile(message)
	if err != nil {
		t.Fatal(err)
	}
	text := string(first)
	if strings.Count(text, vetoAttributionMark) != 1 {
		t.Fatalf("hook marker count = %d, want 1:\n%s", strings.Count(text, vetoAttributionMark), text)
	}
	if !strings.Contains(text, "# veto suggested model: sonnet") {
		t.Fatalf("hook did not preserve model suggestion:\n%s", text)
	}

	if err := os.WriteFile(message, first, 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("sh", hook, message, "message")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second hook run failed: %v\n%s", err, output)
	}
	second, err := os.ReadFile(message)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(second), vetoAttributionMark) != 1 {
		t.Fatalf("second hook run duplicated marker:\n%s", second)
	}

	mergeMessage := filepath.Join(dir, "merge-message")
	if err := os.WriteFile(mergeMessage, []byte("Merge branch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("sh", hook, mergeMessage, "merge")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("merge hook run failed: %v\n%s", err, output)
	}
	mergeText, err := os.ReadFile(mergeMessage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mergeText), vetoAttributionMark) {
		t.Fatalf("merge hook added attribution:\n%s", mergeText)
	}
}
