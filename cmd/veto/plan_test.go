package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlan_Valid(t *testing.T) {
	data := []byte(`---
title: Test Plan
version: 1
steps:
  - task: "Do the thing"
    kind: code-change
    risk: medium
---
Optional notes here.
`)
	plan, err := ParsePlan(data)
	require.NoError(t, err)
	assert.Equal(t, "Test Plan", plan.Title)
	assert.Equal(t, 1, plan.Version)
	require.Len(t, plan.Steps, 1)
	assert.Equal(t, "Do the thing", plan.Steps[0].Task)
	assert.Equal(t, "code-change", plan.Steps[0].Kind)
	assert.Equal(t, "medium", plan.Steps[0].Risk)
}

func TestParsePlan_DependsOn(t *testing.T) {
	data := []byte(`---
title: Two-step
version: 1
steps:
  - task: first
    kind: review
    risk: low
  - task: second
    kind: code-change
    risk: high
    depends_on: [1]
    success_criteria: "tests pass"
---
`)
	plan, err := ParsePlan(data)
	require.NoError(t, err)
	assert.Equal(t, []int{1}, plan.Steps[1].DependsOn)
	assert.Equal(t, "tests pass", plan.Steps[1].SuccessCriteria)
}

func TestParsePlan_NoFrontmatter(t *testing.T) {
	_, err := ParsePlan([]byte("# A plan\nJust some prose.\n"))
	assert.ErrorContains(t, err, "frontmatter")
}

func TestParsePlan_UnclosedFrontmatter(t *testing.T) {
	_, err := ParsePlan([]byte("---\ntitle: Oops\nversion: 1\n"))
	assert.ErrorContains(t, err, "closing")
}

func TestValidatePlan_Valid(t *testing.T) {
	p := &VetoPlan{
		Title:   "Good plan",
		Version: 1,
		Steps:   []PlanStep{{Task: "do it", Kind: "review", Risk: "low"}},
	}
	assert.Empty(t, ValidatePlan(p))
}

func TestValidatePlan_WrongVersion(t *testing.T) {
	p := &VetoPlan{Title: "T", Version: 2, Steps: []PlanStep{{Task: "x", Kind: "review", Risk: "low"}}}
	assert.NotEmpty(t, ValidatePlan(p))
}

func TestValidatePlan_EmptyTitle(t *testing.T) {
	p := &VetoPlan{Title: "  ", Version: 1, Steps: []PlanStep{{Task: "x", Kind: "review", Risk: "low"}}}
	assert.NotEmpty(t, ValidatePlan(p))
}

func TestValidatePlan_NoSteps(t *testing.T) {
	p := &VetoPlan{Title: "T", Version: 1}
	assert.NotEmpty(t, ValidatePlan(p))
}

func TestValidatePlan_MissingTask(t *testing.T) {
	p := &VetoPlan{Title: "T", Version: 1, Steps: []PlanStep{{Task: "", Kind: "review", Risk: "low"}}}
	assert.NotEmpty(t, ValidatePlan(p))
}

func TestValidatePlan_BadKind(t *testing.T) {
	p := &VetoPlan{Title: "T", Version: 1, Steps: []PlanStep{{Task: "x", Kind: "magic", Risk: "low"}}}
	errs := ValidatePlan(p)
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0], "invalid kind")
}

func TestValidatePlan_BadRisk(t *testing.T) {
	p := &VetoPlan{Title: "T", Version: 1, Steps: []PlanStep{{Task: "x", Kind: "review", Risk: "critical"}}}
	errs := ValidatePlan(p)
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0], "invalid risk")
}

func TestValidatePlan_ForwardDependency(t *testing.T) {
	p := &VetoPlan{
		Title: "T", Version: 1,
		Steps: []PlanStep{
			{Task: "a", Kind: "review", Risk: "low", DependsOn: []int{2}},
			{Task: "b", Kind: "review", Risk: "low"},
		},
	}
	assert.NotEmpty(t, ValidatePlan(p))
}

func TestValidatePlan_SelfDependency(t *testing.T) {
	p := &VetoPlan{
		Title: "T", Version: 1,
		Steps: []PlanStep{
			{Task: "a", Kind: "review", Risk: "low", DependsOn: []int{1}},
		},
	}
	assert.NotEmpty(t, ValidatePlan(p))
}

func TestPlanSlug(t *testing.T) {
	assert.Equal(t, "my-plan", planSlug("My Plan"))
	assert.Equal(t, "migrate-auth-to-jwt", planSlug("Migrate Auth to JWT!"))
	assert.Equal(t, "plan", planSlug("---!!!---"))
	assert.Equal(t, "abc", planSlug("  abc  "))
}
