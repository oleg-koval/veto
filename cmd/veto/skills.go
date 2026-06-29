package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oleg-koval/veto/pkg/router"
	"gopkg.in/yaml.v3"
)

// skill is a parsed ~/.veto/skills/<name>.md entry.
type skill struct {
	Name     string   `yaml:"name"`
	Kinds    []string `yaml:"kinds"`
	Keywords []string `yaml:"keywords"`
	Body     string   // content after the closing ---
}

// skillsDirOverride lets tests redirect the skills directory to a temp path.
var skillsDirOverride string

func skillsDir() string {
	if skillsDirOverride != "" {
		return skillsDirOverride
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "skills")
}

// loadSkills reads all .md files in skillsDir and parses their frontmatter + body.
// Missing or empty directory → nil (not an error).
func loadSkills() []skill {
	dir := skillsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var skills []skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if s, ok := parseSkillFile(data); ok {
			skills = append(skills, s)
		}
	}
	return skills
}

// parseSkillFile extracts YAML frontmatter and body from a skill .md file.
// Reuses the same --- fence format as ParsePlan.
func parseSkillFile(data []byte) (skill, bool) {
	if !bytes.HasPrefix(data, []byte("---")) {
		return skill{}, false
	}
	rest := bytes.TrimPrefix(data, []byte("---"))
	rest = bytes.TrimPrefix(rest, []byte("\r\n"))
	rest = bytes.TrimPrefix(rest, []byte("\n"))
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return skill{}, false
	}
	var s skill
	if err := yaml.Unmarshal(rest[:idx], &s); err != nil {
		return skill{}, false
	}
	s.Body = strings.TrimSpace(string(rest[idx+4:])) // skip "\n---"
	return s, true
}

// matchSkills returns skills matching task's kind (and keywords if set). Cap: 2.
func matchSkills(task router.TaskSpec) []skill {
	all := loadSkills()
	var matched []skill
	for _, s := range all {
		if !skillKindMatches(s, string(task.Kind)) {
			continue
		}
		if len(s.Keywords) > 0 && !skillKeywordsOverlap(s.Keywords, task.Objective) {
			continue
		}
		matched = append(matched, s)
		if len(matched) == 2 {
			break
		}
	}
	return matched
}

func skillKindMatches(s skill, kind string) bool {
	for _, k := range s.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func skillKeywordsOverlap(keywords []string, objective string) bool {
	lower := strings.ToLower(objective)
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

const generateSkillPromptTemplate = `Write a concise, reusable skill snippet for an AI model executor
that will be prepended to tasks of kind %q.

The snippet should be 3-8 bullet points: practical instructions that help the executor do %q tasks well.
Output ONLY the bullet points — no frontmatter, no code fences, no preamble, no conclusion.`

// generateSkill routes a skill-generation task to the cheapest available model,
// saves the result to ~/.veto/skills/<slug>.md, and returns the body.
// Best-effort: returns ("", nil) on failure — never blocks the main routing call.
func generateSkill(ctx context.Context, reg *providerRegistry, mgr *router.Manager, task router.TaskSpec) (string, error) {
	prompt := fmt.Sprintf(generateSkillPromptTemplate, task.Kind, task.Kind)
	genSpec := router.TaskSpec{
		ID:        taskHash(prompt, "summarize", "low", 0),
		Kind:      router.KindSummarize,
		Objective: prompt,
		Risk:      router.RiskLow,
	}
	render := NewRenderer(true) // quiet: suppress nested pipeline noise
	_, body, err := routeAndCapture(ctx, reg, mgr, render, genSpec, nil)
	if err != nil || strings.TrimSpace(body) == "" {
		return "", err
	}
	body = strings.TrimSpace(body)

	// save to ~/.veto/skills/<slug>.md for future reuse
	slug := skillSlug(string(task.Kind))
	content := fmt.Sprintf("---\nname: %s\nkinds: [%s]\nkeywords: []\n---\n%s\n",
		string(task.Kind), string(task.Kind), body)
	if err := os.MkdirAll(skillsDir(), 0700); err == nil {
		_ = os.WriteFile(filepath.Join(skillsDir(), slug+".md"), []byte(content), 0600)
	}
	return body, nil
}

// resolveSkills returns (names, bodies) for the skills matched (or generated) for task.
// Names are used for display; bodies are injected into the executor prompt.
func resolveSkills(ctx context.Context, reg *providerRegistry, mgr *router.Manager, task router.TaskSpec) (names, bodies []string) {
	matched := matchSkills(task)
	if len(matched) > 0 {
		for _, s := range matched {
			names = append(names, s.Name)
			bodies = append(bodies, s.Body)
		}
		return names, bodies
	}
	// nothing in library — generate one
	body, _ := generateSkill(ctx, reg, mgr, task)
	if body == "" {
		return nil, nil
	}
	return []string{string(task.Kind) + " (generated)"}, []string{body}
}

// withSkills prepends matched skill bodies to the task objective.
// Returns objective unchanged when skills is empty (no-op for internal routes).
func withSkills(objective string, bodies []string) string {
	if len(bodies) == 0 {
		return objective
	}
	return "## Relevant skills\n\n" + strings.Join(bodies, "\n\n") + "\n\n## Task\n\n" + objective
}

var reSkillSlug = regexp.MustCompile(`[^a-z0-9]+`)

func skillSlug(name string) string {
	s := reSkillSlug.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "skill"
	}
	return s
}
