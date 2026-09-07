package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSaveTUIMissionRejectsMissionStoreReadErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".veto", "missions.json")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}

	err := saveTUIMission(tuiMissionRecord{RunID: "run-1", Objective: "test objective"})
	if err == nil || !strings.Contains(err.Error(), "read mission index") {
		t.Fatalf("error = %v, want mission index read error", err)
	}
	if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
		t.Fatalf("mission store was replaced after read error: info=%v error=%v", info, statErr)
	}
}

func TestMissionPromptTruncationPreservesUTF8(t *testing.T) {
	objective := strings.Repeat("a", maxMissionPrompt-1) + "é"
	bounded := boundMissionPrompt(objective)
	if !utf8.ValidString(bounded) {
		t.Fatalf("boundMissionPrompt returned invalid UTF-8: %q", bounded[len(bounded)-50:])
	}
}

func TestMissionTitleTruncationPreservesUTF8(t *testing.T) {
	objective := strings.Repeat("a", 92) + "ébbb"
	title := missionTitle(objective)
	if !utf8.ValidString(title) {
		t.Fatalf("missionTitle returned invalid UTF-8: %q", title)
	}
	if !strings.HasSuffix(title, "...") {
		t.Fatalf("missionTitle = %q, want truncation suffix", title)
	}
}
