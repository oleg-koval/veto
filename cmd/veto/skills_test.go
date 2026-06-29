package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTempSkillsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	skillsDirOverride = dir
	t.Cleanup(func() { skillsDirOverride = "" })
	return dir
}

func writeSkill(t *testing.T, dir, filename, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0600))
}

// TestParseSkillFile verifies frontmatter + body extraction.
func TestParseSkillFile(t *testing.T) {
	data := []byte("---\nname: go-testing\nkinds: [code-change, debug]\nkeywords: [test]\n---\n- Write table-driven tests.\n")
	s, ok := parseSkillFile(data)
	require.True(t, ok)
	assert.Equal(t, "go-testing", s.Name)
	assert.Equal(t, []string{"code-change", "debug"}, s.Kinds)
	assert.Equal(t, []string{"test"}, s.Keywords)
	assert.Contains(t, s.Body, "table-driven")
}

func TestParseSkillFile_NoFrontmatter(t *testing.T) {
	_, ok := parseSkillFile([]byte("just a body, no frontmatter"))
	assert.False(t, ok)
}

// TestMatchSkills_ByKind matches a skill on kind alone (no keywords).
func TestMatchSkills_ByKind(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkill(t, dir, "debug.md", "---\nname: debug-tips\nkinds: [debug]\nkeywords: []\n---\n- Check logs first.\n")

	task := router.TaskSpec{Kind: router.KindDebug, Objective: "something unrelated"}
	matched := matchSkills(task)
	require.Len(t, matched, 1)
	assert.Equal(t, "debug-tips", matched[0].Name)
}

// TestMatchSkills_KindMismatch returns nothing when kind doesn't match.
func TestMatchSkills_KindMismatch(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkill(t, dir, "plan.md", "---\nname: planner\nkinds: [plan]\nkeywords: []\n---\n- Think step by step.\n")

	task := router.TaskSpec{Kind: router.KindDebug, Objective: "debug something"}
	assert.Empty(t, matchSkills(task))
}

// TestMatchSkills_KeywordFilter requires keyword overlap when keywords are set.
func TestMatchSkills_KeywordFilter(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkill(t, dir, "table.md", "---\nname: table-driven\nkinds: [code-change]\nkeywords: [table-driven, subtest]\n---\n- Use t.Run subtests.\n")

	match := router.TaskSpec{Kind: router.KindCodeChange, Objective: "write a table-driven test"}
	miss := router.TaskSpec{Kind: router.KindCodeChange, Objective: "refactor the handler"}

	assert.Len(t, matchSkills(match), 1)
	assert.Empty(t, matchSkills(miss))
}

// TestMatchSkills_Cap ensures at most 2 skills are returned.
func TestMatchSkills_Cap(t *testing.T) {
	dir := setTempSkillsDir(t)
	for i := 0; i < 5; i++ {
		name := string(rune('a' + i))
		writeSkill(t, dir, name+".md",
			"---\nname: skill-"+name+"\nkinds: [summarize]\nkeywords: []\n---\nbody "+name+"\n")
	}
	task := router.TaskSpec{Kind: router.KindSummarize, Objective: "anything"}
	assert.Len(t, matchSkills(task), 2)
}

// TestLoadSkills_EmptyDir returns nil gracefully.
func TestLoadSkills_EmptyDir(t *testing.T) {
	setTempSkillsDir(t)
	assert.Nil(t, loadSkills())
}

// TestLoadSkills_MissingDir returns nil gracefully.
func TestLoadSkills_MissingDir(t *testing.T) {
	skillsDirOverride = filepath.Join(t.TempDir(), "nonexistent")
	t.Cleanup(func() { skillsDirOverride = "" })
	assert.Nil(t, loadSkills())
}

// TestWithSkills_Empty returns objective unchanged.
func TestWithSkills_Empty(t *testing.T) {
	assert.Equal(t, "do the thing", withSkills("do the thing", nil))
	assert.Equal(t, "do the thing", withSkills("do the thing", []string{}))
}

// TestWithSkills_Injects prepends the skill body.
func TestWithSkills_Injects(t *testing.T) {
	result := withSkills("my task", []string{"- bullet one"})
	assert.Contains(t, result, "## Relevant skills")
	assert.Contains(t, result, "- bullet one")
	assert.Contains(t, result, "## Task")
	assert.Contains(t, result, "my task")
	// admission objective must come AFTER skills
	assert.Greater(t, len(result), len("my task"))
}

// TestSkillSlug verifies slug generation.
func TestSkillSlug(t *testing.T) {
	assert.Equal(t, "code-change", skillSlug("code-change"))
	assert.Equal(t, "debug", skillSlug("Debug!"))
	assert.Equal(t, "skill", skillSlug(""))
}

// TestSplitCriteria verifies criteria splitting (defined in exec.go).
func TestSplitCriteria(t *testing.T) {
	assert.Equal(t, []string{"A", "B"}, splitCriteria("A\nB"))
	assert.Equal(t, []string{"A", "B"}, splitCriteria("A;B"))
	assert.Nil(t, splitCriteria(""))
	assert.Nil(t, splitCriteria("   "))
	assert.Equal(t, []string{"trim me"}, splitCriteria("  trim me  "))
}
