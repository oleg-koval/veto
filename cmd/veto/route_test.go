package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/oleg-koval/veto/pkg/router"
)

func TestInferKind(t *testing.T) {
	tests := []struct {
		objective string
		want      string
	}{
		{"fix the nil pointer crash on startup", "debug"},
		{"the login flow is broken", "debug"},
		{"refactor the auth package", "refactor"},
		{"clean up the duplicated handlers", "refactor"},
		{"summarize this PR for the changelog", "summarize"},
		{"extract the table data from the PDF", "extract"},
		{"review the new payment code", "review"},
		{"plan the migration to postgres", "plan"},
		{"design the caching layer", "plan"},
		{"add a retry to the http client", "code-change"},
		{"", "code-change"},
	}
	for _, tt := range tests {
		t.Run(tt.objective, func(t *testing.T) {
			assert.Equal(t, tt.want, inferKind(tt.objective))
		})
	}
}

func TestInferKind_ExplicitOverrideHappensInCaller(t *testing.T) {
	// inferKind is only consulted when --kind is empty; this documents that
	// "review" wins over the default code-change purely from the keyword.
	assert.Equal(t, "review", inferKind("please review and check this"))
}

func TestContainsAny(t *testing.T) {
	assert.True(t, containsAny("hello world", "world"))
	assert.True(t, containsAny("hello world", "nope", "world"))
	assert.False(t, containsAny("hello world", "xyz"))
	assert.False(t, containsAny("hello world"))
}

// TestSavingsVsOpus documents the reward calc: any non-opus model should cost
// less than opus for the same task, yielding a positive saving.
func TestSavingsVsOpus(t *testing.T) {
	reg := router.NewRegistry()
	opus, ok := reg.ByName("opus")
	assert.True(t, ok)
	haiku, ok := reg.ByName("haiku")
	assert.True(t, ok)

	spec := router.TaskSpec{Kind: router.KindCodeChange, MaxTokens: 1000}
	saved := router.EstimatedCost(opus, spec) - router.EstimatedCost(haiku, spec)
	assert.Greater(t, saved, 0.0, "routing to haiku must be cheaper than opus")
}
