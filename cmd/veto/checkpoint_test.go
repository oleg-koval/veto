package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskHash_Deterministic(t *testing.T) {
	h1 := taskHash("refactor auth", "refactor", "medium", 0.05)
	h2 := taskHash("refactor auth", "refactor", "medium", 0.05)
	assert.Equal(t, h1, h2)
}

func TestTaskHash_DifferentInputs(t *testing.T) {
	base := taskHash("task A", "code-change", "low", 0)
	cases := []struct {
		desc string
		hash string
	}{
		{"different objective", taskHash("task B", "code-change", "low", 0)},
		{"different kind", taskHash("task A", "debug", "low", 0)},
		{"different risk", taskHash("task A", "code-change", "high", 0)},
		{"different max cost", taskHash("task A", "code-change", "low", 1.0)},
	}
	for _, c := range cases {
		assert.NotEqual(t, base, c.hash, "expected different hash for: %s", c.desc)
	}
}

func TestTaskHash_IsShortHex(t *testing.T) {
	h := taskHash("obj", "kind", "risk", 0)
	assert.Len(t, h, 16)
	for _, ch := range h {
		assert.Contains(t, "0123456789abcdef", string(ch))
	}
}

func TestCheckpoint_Add(t *testing.T) {
	cp := &Checkpoint{Hash: "abc", Objective: "test"}
	cp.add("haiku", false, []string{"MISSING_TOOL"})
	cp.add("sonnet", true, nil)

	require.Len(t, cp.Tried, 2)
	assert.Equal(t, "haiku", cp.Tried[0].Model)
	assert.False(t, cp.Tried[0].Accepted)
	assert.Equal(t, []string{"MISSING_TOOL"}, cp.Tried[0].Reasons)
	assert.Equal(t, "sonnet", cp.Tried[1].Model)
	assert.True(t, cp.Tried[1].Accepted)
	assert.False(t, cp.SavedAt.IsZero())
}

func TestCheckpoint_TriedNames(t *testing.T) {
	cp := &Checkpoint{}
	cp.add("haiku", false, nil)
	cp.add("sonnet", false, nil)
	cp.add("opus", true, nil)

	assert.Equal(t, []string{"haiku", "sonnet", "opus"}, cp.triedNames())
}

func TestCheckpoint_TriedNames_Empty(t *testing.T) {
	cp := &Checkpoint{}
	assert.Empty(t, cp.triedNames())
}

func TestSaveLoadDeleteCheckpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cp := &Checkpoint{Hash: "deadbeef12345678", Objective: "test task"}
	cp.add("haiku", false, []string{"PARSE_FAILURE"})
	cp.add("sonnet", true, nil)

	saveCheckpoint(cp)

	loaded, ok := loadCheckpoint("deadbeef12345678")
	require.True(t, ok)
	assert.Equal(t, "test task", loaded.Objective)
	require.Len(t, loaded.Tried, 2)
	assert.Equal(t, "haiku", loaded.Tried[0].Model)
	assert.False(t, loaded.Tried[0].Accepted)
	assert.Equal(t, "sonnet", loaded.Tried[1].Model)
	assert.True(t, loaded.Tried[1].Accepted)

	deleteCheckpoint("deadbeef12345678")
	_, ok = loadCheckpoint("deadbeef12345678")
	assert.False(t, ok)
}

func TestLoadCheckpoint_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, ok := loadCheckpoint("doesnotexist")
	assert.False(t, ok)
}

func TestLoadCheckpoint_CorruptJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	hash := "badhash00badhash"
	path := checkpointPath(hash)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte("{not valid json"), 0600)

	_, ok := loadCheckpoint(hash)
	assert.False(t, ok)
}
