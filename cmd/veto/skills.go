package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oleg-koval/veto/pkg/router"
	"gopkg.in/yaml.v3"
)

// skill is a parsed skill file (.md with --- frontmatter).
type skill struct {
	Name     string   `yaml:"name"`
	Kinds    []string `yaml:"kinds"`    // empty = matches all kinds
	Keywords []string `yaml:"keywords"` // empty = no keyword filter
	Body     string   // content after the closing ---
	Source   string   // file path (for display)
}

// skillsConfig holds approved skill source directories and approved individual skill files.
// Stored in ~/.veto/config.json under "skills".
type skillsConfig struct {
	// ApprovedDirs are directories veto may load skills from (beyond ~/.veto/skills/).
	ApprovedDirs []string `json:"approved_dirs,omitempty"`
	// ApprovedFiles are individual skill file paths approved by the user.
	ApprovedFiles []string `json:"approved_files,omitempty"`
	// AutoApproveNew: when true, new skills found in approved dirs are used without prompting.
	AutoApproveNew bool `json:"auto_approve_new,omitempty"`
}

// skillsDirOverride lets tests redirect the veto-generated skills directory.
var skillsDirOverride string

// vetoCfgPathOverride lets tests redirect ~/.veto/config.json.
var vetoCfgPathOverride string

func skillsDir() string {
	if skillsDirOverride != "" {
		return skillsDirOverride
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "skills")
}

// vetoCfgPath returns the path to ~/.veto/config.json.
func vetoCfgPath() string {
	if vetoCfgPathOverride != "" {
		return vetoCfgPathOverride
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "config.json")
}

// loadSkillsConfig reads the skills section from ~/.veto/config.json.
func loadSkillsConfig() skillsConfig {
	data, err := os.ReadFile(vetoCfgPath())
	if err != nil {
		return skillsConfig{}
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil {
		return skillsConfig{}
	}
	raw, ok := full["skills"]
	if !ok {
		return skillsConfig{}
	}
	var cfg skillsConfig
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

// saveSkillsConfig writes the skills section back to ~/.veto/config.json (merges).
func saveSkillsConfig(cfg skillsConfig) error {
	path := vetoCfgPath()
	var full map[string]json.RawMessage
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &full)
	}
	if full == nil {
		full = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	full["skills"] = raw
	out, err := json.MarshalIndent(full, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}

// skillSourceDirs returns the union of the veto-generated dir and user-approved dirs.
func skillSourceDirs() []string {
	dirs := []string{skillsDir()} // veto-generated: always included, always approved
	cfg := loadSkillsConfig()
	for _, d := range cfg.ApprovedDirs {
		if d != skillsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// loadSkills reads all approved skills from all source directories.
// Skills in ~/.veto/skills/ are always approved. Others must be in cfg.ApprovedFiles or cfg.ApprovedDirs.
func loadSkills() []skill {
	cfg := loadSkillsConfig()
	approvedFiles := make(map[string]bool, len(cfg.ApprovedFiles))
	for _, f := range cfg.ApprovedFiles {
		approvedFiles[f] = true
	}

	vetoDir := skillsDir()
	var skills []skill
	for _, dir := range skillSourceDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			// veto-generated dir: always approved. Others: must be in approved list.
			if dir != vetoDir && !approvedFiles[path] {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if s, ok := parseSkillFile(data); ok {
				s.Source = path
				skills = append(skills, s)
			}
		}
	}
	return skills
}

// parseSkillFile extracts YAML frontmatter and body from a skill .md file.
// Reuses the same --- fence format as ParsePlan.
// Skills without `kinds` match all task kinds (lowest priority).
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

// matchSkills returns up to 2 skills matching task kind (and keywords if set).
// Skills with no kinds declared match any kind (lower priority than kind-specific ones).
func matchSkills(task router.TaskSpec) []skill {
	all := loadSkills()
	var specific, generic []skill
	for _, s := range all {
		if len(s.Kinds) > 0 {
			if !skillKindMatches(s, string(task.Kind)) {
				continue
			}
			if len(s.Keywords) > 0 && !skillKeywordsOverlap(s.Keywords, task.Objective) {
				continue
			}
			specific = append(specific, s)
		} else {
			// no kinds declared: matches all kinds, lower priority
			if len(s.Keywords) > 0 && !skillKeywordsOverlap(s.Keywords, task.Objective) {
				continue
			}
			generic = append(generic, s)
		}
	}
	// prefer kind-specific; fill up to cap 2 with generics
	combined := append(specific, generic...)
	if len(combined) > 2 {
		combined = combined[:2]
	}
	return combined
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
	render := NewRenderer(true)
	_, body, err := routeAndCapture(ctx, reg, mgr, render, genSpec, nil)
	if err != nil || strings.TrimSpace(body) == "" {
		return "", err
	}
	body = strings.TrimSpace(body)

	slug := skillSlug(string(task.Kind))
	content := fmt.Sprintf("---\nname: %s\nkinds: [%s]\nkeywords: []\n---\n%s\n",
		string(task.Kind), string(task.Kind), body)
	if err := os.MkdirAll(skillsDir(), 0700); err == nil {
		_ = os.WriteFile(filepath.Join(skillsDir(), slug+".md"), []byte(content), 0600)
	}
	return body, nil
}

// resolveSkills returns (names, bodies) for the skills matching task from approved skill files.
// Skills are never auto-generated here; generation is a separate offline step.
func resolveSkills(_ context.Context, _ *providerRegistry, _ *router.Manager, task router.TaskSpec) (names, bodies []string) {
	for _, s := range matchSkills(task) {
		names = append(names, s.Name)
		bodies = append(bodies, s.Body)
	}
	return names, bodies
}

// withSkills prepends matched skill bodies to the task objective.
// Returns objective unchanged when skills is empty.
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

// scanUnapprovedSkills finds .md files in dirs that aren't yet approved.
// Called at startup to notify users of new skills pending review.
func scanUnapprovedSkills(dirs []string, cfg skillsConfig) []string {
	vetoDir := skillsDir()
	approved := make(map[string]bool, len(cfg.ApprovedFiles))
	for _, f := range cfg.ApprovedFiles {
		approved[f] = true
	}
	approvedDirSet := make(map[string]bool, len(cfg.ApprovedDirs)+1)
	approvedDirSet[vetoDir] = true
	for _, d := range cfg.ApprovedDirs {
		approvedDirSet[d] = true
	}

	var unapproved []string
	for _, dir := range dirs {
		if approvedDirSet[dir] {
			continue // whole dir already approved
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if !approved[path] {
				unapproved = append(unapproved, path)
			}
		}
	}
	return unapproved
}

// cmdSetup runs the interactive skill discovery and approval flow.
func cmdSetup() {
	home, _ := os.UserHomeDir()
	cfg := loadSkillsConfig()

	// Candidate directories to scan
	candidates := []string{
		filepath.Join(home, ".claude", "skills"),
	}

	fmt.Println()
	fmt.Println("  veto setup — skill discovery")
	fmt.Println()

	// Auto-approve new setting
	fmt.Printf("  Auto-approve new skills found in approved directories? [y/N]: ")
	var ans string
	fmt.Scanln(&ans) //nolint:errcheck
	cfg.AutoApproveNew = strings.ToLower(strings.TrimSpace(ans)) == "y"
	fmt.Println()

	// Scan each candidate directory
	anyFound := false
	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var mdFiles []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				mdFiles = append(mdFiles, filepath.Join(dir, e.Name()))
			}
		}
		if len(mdFiles) == 0 {
			continue
		}
		anyFound = true
		fmt.Printf("  Found %d skill file(s) in %s:\n", len(mdFiles), dir)
		for _, f := range mdFiles {
			data, _ := os.ReadFile(f)
			s, ok := parseSkillFile(data)
			name := filepath.Base(f)
			if ok && s.Name != "" {
				name = s.Name
			}
			fmt.Printf("    • %-30s  %s\n", name, f)
		}
		fmt.Println()
		fmt.Printf("  Approve all skills from %s? [y/N]: ", dir)
		fmt.Scanln(&ans) //nolint:errcheck
		if strings.ToLower(strings.TrimSpace(ans)) == "y" {
			// approve the whole directory
			if !containsStr(cfg.ApprovedDirs, dir) {
				cfg.ApprovedDirs = append(cfg.ApprovedDirs, dir)
			}
		} else {
			// offer per-file approval
			for _, f := range mdFiles {
				if containsStr(cfg.ApprovedFiles, f) {
					continue
				}
				data, _ := os.ReadFile(f)
				s, ok := parseSkillFile(data)
				name := filepath.Base(f)
				if ok && s.Name != "" {
					name = s.Name
				}
				fmt.Printf("    Approve %q? [y/N]: ", name)
				fmt.Scanln(&ans) //nolint:errcheck
				if strings.ToLower(strings.TrimSpace(ans)) == "y" {
					cfg.ApprovedFiles = append(cfg.ApprovedFiles, f)
				}
			}
		}
		fmt.Println()
	}

	if !anyFound {
		fmt.Println("  No external skill files found.")
		fmt.Printf("  (Checked: %s)\n", strings.Join(candidates, ", "))
		fmt.Println()
		fmt.Println("  Your veto-generated skills live in ~/.veto/skills/ and are always available.")
		fmt.Println("  To add a skill directory, edit ~/.veto/config.json under \"skills.approved_dirs\".")
	}

	if err := saveSkillsConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "  error saving config: %v\n", err)
		return
	}
	fmt.Println("  Settings saved to ~/.veto/config.json")
	fmt.Println()

	// Notify about pending unapproved skills on next run
	unapproved := scanUnapprovedSkills(candidates, cfg)
	if len(unapproved) > 0 && !cfg.AutoApproveNew {
		fmt.Printf("  %d unapproved skill(s) still pending — run 'veto setup' again to review.\n", len(unapproved))
	}
}

// checkPendingSkills prints a one-line notice at startup when new unapproved skills exist.
// Call from main before executing a command.
func checkPendingSkills() {
	cfg := loadSkillsConfig()
	if cfg.AutoApproveNew {
		return
	}
	home, _ := os.UserHomeDir()
	candidates := []string{filepath.Join(home, ".claude", "skills")}
	unapproved := scanUnapprovedSkills(candidates, cfg)
	if len(unapproved) > 0 {
		fmt.Fprintf(os.Stderr, "  notice: %d new skill file(s) pending approval — run 'veto setup' to review.\n", len(unapproved))
	}
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
