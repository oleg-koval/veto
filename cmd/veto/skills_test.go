package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setTempSkillsDir redirects the veto-generated skills dir (always approved).
func setTempSkillsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	skillsDirOverride = dir
	t.Cleanup(func() { skillsDirOverride = "" })
	return dir
}

func writeSkillFile(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
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

func TestParseSkillFile_NoKinds_Valid(t *testing.T) {
	data := []byte("---\nname: generic\n---\n- Always be concise.\n")
	s, ok := parseSkillFile(data)
	require.True(t, ok)
	assert.Empty(t, s.Kinds) // matches all kinds
	assert.Contains(t, s.Body, "concise")
}

// TestMatchSkills_ByKind matches a specific-kind skill.
func TestMatchSkills_ByKind(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkillFile(t, dir, "debug.md", "---\nname: debug-tips\nkinds: [debug]\nkeywords: []\n---\n- Check logs first.\n")

	task := router.TaskSpec{Kind: router.KindDebug, Objective: "something"}
	matched := matchSkills(task)
	require.Len(t, matched, 1)
	assert.Equal(t, "debug-tips", matched[0].Name)
}

// TestMatchSkills_GenericKindMatchesAll verifies skills with no kinds field match any kind.
func TestMatchSkills_GenericKindMatchesAll(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkillFile(t, dir, "generic.md", "---\nname: generic\nkeywords: []\n---\n- Be concise.\n")

	for _, k := range []router.TaskKind{router.KindDebug, router.KindPlan, router.KindReview} {
		matched := matchSkills(router.TaskSpec{Kind: k, Objective: "anything"})
		require.Len(t, matched, 1, "generic skill should match kind %s", k)
	}
}

// TestMatchSkills_SpecificBeatsGeneric — kind-specific skills come before generic ones.
func TestMatchSkills_SpecificBeatsGeneric(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkillFile(t, dir, "specific.md", "---\nname: specific\nkinds: [debug]\nkeywords: []\n---\nspecific body\n")
	writeSkillFile(t, dir, "generic.md", "---\nname: generic\nkeywords: []\n---\ngeneric body\n")

	matched := matchSkills(router.TaskSpec{Kind: router.KindDebug, Objective: "anything"})
	require.Len(t, matched, 2)
	assert.Equal(t, "specific", matched[0].Name, "specific skill must rank first")
}

// TestMatchSkills_KindMismatch returns nothing when kind doesn't match and no generic skills exist.
func TestMatchSkills_KindMismatch(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkillFile(t, dir, "plan.md", "---\nname: planner\nkinds: [plan]\nkeywords: []\n---\n- Think step by step.\n")

	assert.Empty(t, matchSkills(router.TaskSpec{Kind: router.KindDebug, Objective: "debug something"}))
}

// TestMatchSkills_KeywordFilter requires overlap when keywords are set.
func TestMatchSkills_KeywordFilter(t *testing.T) {
	dir := setTempSkillsDir(t)
	writeSkillFile(t, dir, "table.md", "---\nname: table-driven\nkinds: [code-change]\nkeywords: [table-driven]\n---\n- Use t.Run.\n")

	assert.Len(t, matchSkills(router.TaskSpec{Kind: router.KindCodeChange, Objective: "write a table-driven test"}), 1)
	assert.Empty(t, matchSkills(router.TaskSpec{Kind: router.KindCodeChange, Objective: "refactor the handler"}))
}

// TestMatchSkills_Cap ensures at most 2 skills are returned.
func TestMatchSkills_Cap(t *testing.T) {
	dir := setTempSkillsDir(t)
	for i := 0; i < 5; i++ {
		name := string(rune('a' + i))
		writeSkillFile(t, dir, name+".md",
			"---\nname: skill-"+name+"\nkinds: [summarize]\nkeywords: []\n---\nbody "+name+"\n")
	}
	assert.Len(t, matchSkills(router.TaskSpec{Kind: router.KindSummarize, Objective: "anything"}), 2)
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

// TestScanUnapprovedSkills_FileApproval — per-file approval removes the file from the unapproved list.
func TestScanUnapprovedSkills_FileApproval(t *testing.T) {
	dir := t.TempDir()
	extFile := writeSkillFile(t, dir, "ext.md", "---\nname: external\nkinds: [debug]\n---\nexternal body\n")

	// Without approval: unapproved.
	unapproved := scanUnapprovedSkills([]string{dir}, skillsConfig{})
	assert.Len(t, unapproved, 1)
	assert.Equal(t, extFile, unapproved[0])

	// With per-file approval: not unapproved.
	unapproved = scanUnapprovedSkills([]string{dir}, skillsConfig{ApprovedFiles: []string{extFile}})
	assert.Empty(t, unapproved)
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

// TestScanUnapprovedSkills_Empty returns nothing when dir is empty.
func TestScanUnapprovedSkills_Empty(t *testing.T) {
	dir := t.TempDir()
	result := scanUnapprovedSkills([]string{dir}, skillsConfig{})
	assert.Empty(t, result)
}

// TestScanUnapprovedSkills_ApprovedDir — whole-dir approval marks all files as approved.
func TestScanUnapprovedSkills_ApprovedDir(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "a.md", "---\nname: a\n---\nbody\n")
	result := scanUnapprovedSkills([]string{dir}, skillsConfig{ApprovedDirs: []string{dir}})
	assert.Empty(t, result)
}

// TestContainsStr verifies the helper.
func TestContainsStr(t *testing.T) {
	assert.True(t, containsStr([]string{"a", "b"}, "b"))
	assert.False(t, containsStr([]string{"a"}, "z"))
}
