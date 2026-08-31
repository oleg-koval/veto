package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFormatCLITableRowAlignsLongValues(t *testing.T) {
	widths := []int{
		utf8.RuneCountInString("ollama-qwen2-5-coder"),
		utf8.RuneCountInString("http://localhost:11434/v1/chat/completions"),
		utf8.RuneCountInString("qwen2.5-coder:7b"),
	}
	row := formatCLITableRow([]string{"Codex", "ChatGPT (cli)", "codex"}, widths)

	statusAt := strings.Index(row, "ChatGPT (cli)")
	modelAt := strings.Index(row, "codex")
	if statusAt != widths[0]+2 {
		t.Fatalf("status column starts at %d, want %d: %q", statusAt, widths[0]+2, row)
	}
	wantModelAt := widths[0] + 2 + widths[1] + 2
	if modelAt != wantModelAt {
		t.Fatalf("model column starts at %d, want %d: %q", modelAt, wantModelAt, row)
	}
}
