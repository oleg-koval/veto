package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"gopkg.in/yaml.v3"
)

// VetoPlan is the in-memory representation of a veto plan file.
type VetoPlan struct {
	Title   string     `yaml:"title"`
	Version int        `yaml:"version"`
	Steps   []PlanStep `yaml:"steps"`
}

type PlanStep struct {
	Task            string `yaml:"task"`
	Kind            string `yaml:"kind"`
	Risk            string `yaml:"risk"`
	DependsOn       []int  `yaml:"depends_on"`
	SuccessCriteria string `yaml:"success_criteria"`
}

// ParsePlan extracts YAML frontmatter from a .md file and unmarshals it.
// Returns an error (treated as "needs conversion") if no frontmatter exists
// or the YAML is malformed.
func ParsePlan(data []byte) (*VetoPlan, error) {
	if !bytes.HasPrefix(data, []byte("---")) {
		return nil, fmt.Errorf("no YAML frontmatter (file must start with ---)")
	}
	rest := bytes.TrimPrefix(data, []byte("---"))
	rest = bytes.TrimPrefix(rest, []byte("\r\n"))
	rest = bytes.TrimPrefix(rest, []byte("\n"))
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return nil, fmt.Errorf("unclosed YAML frontmatter (missing closing ---)")
	}
	var plan VetoPlan
	if err := yaml.Unmarshal(rest[:idx], &plan); err != nil {
		return nil, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	return &plan, nil
}

// ValidatePlan checks the plan against the veto spec rules.
// Returns a list of human-readable violations; empty means valid.
func ValidatePlan(p *VetoPlan) []string {
	var errs []string
	if p.Version != 1 {
		errs = append(errs, "frontmatter must have version: 1")
	}
	if strings.TrimSpace(p.Title) == "" {
		errs = append(errs, "title must be non-empty")
	}
	if len(p.Steps) == 0 {
		return append(errs, "plan must have at least one step")
	}
	for i, s := range p.Steps {
		n := i + 1
		if strings.TrimSpace(s.Task) == "" {
			errs = append(errs, fmt.Sprintf("step %d: missing task", n))
		}
		if !validKinds[s.Kind] {
			errs = append(errs, fmt.Sprintf("step %d: invalid kind %q (valid: %s)", n, s.Kind, validKindList))
		}
		if s.Risk != "low" && s.Risk != "medium" && s.Risk != "high" {
			errs = append(errs, fmt.Sprintf("step %d: invalid risk %q (valid: low|medium|high)", n, s.Risk))
		}
		for _, dep := range s.DependsOn {
			if dep < 1 || dep >= n {
				errs = append(errs, fmt.Sprintf("step %d: depends_on %d must refer to an earlier step", n, dep))
			}
		}
	}
	return errs
}

var reSlugNonWord = regexp.MustCompile(`[^a-z0-9]+`)

func planSlug(title string) string {
	s := reSlugNonWord.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "plan"
	}
	return s
}

func convertedPlanPath(title string) string {
	home, _ := os.UserHomeDir()
	ts := time.Now().Format("2006-01-02T150405")
	return filepath.Join(home, ".veto", "plans", ts+"-"+planSlug(title)+"-converted.md")
}

const conversionPromptTemplate = `Convert the plan below into veto plan spec format.
Return ONLY a markdown file: YAML frontmatter with title, version: 1, and a steps list
(each step: task, kind, risk; optional depends_on, success_criteria).
Valid kind: code-change|debug|refactor|summarize|extract|review|plan.
Valid risk: low|medium|high.
No prose before or after the frontmatter.

PLAN:
`

// convertPlan routes the conversion to the best available model and returns
// the parsed VetoPlan and its raw bytes. The caller writes the bytes to disk.
func convertPlan(ctx context.Context, reg *providerRegistry, mgr *router.Manager, originalText string) (*VetoPlan, []byte, error) {
	render := NewRenderer(true)
	spec := router.TaskSpec{
		ID:        taskHash(originalText, "plan", "medium", 0),
		Kind:      router.TaskKind("plan"),
		Objective: conversionPromptTemplate + originalText,
		Risk:      router.Risk("medium"),
	}
	_, output, err := routeAndCapture(ctx, reg, mgr, render, spec)
	if err != nil {
		return nil, nil, fmt.Errorf("conversion routing failed: %w", err)
	}
	raw := []byte(stripCodeFences(output))
	plan, err := ParsePlan(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("model returned unparseable plan: %w", err)
	}
	return plan, raw, nil
}

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
	}
	if strings.HasSuffix(s, "```") {
		if i := strings.LastIndex(s, "\n```"); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}
