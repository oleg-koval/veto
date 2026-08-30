package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestRequiresExecutableRuntime(t *testing.T) {
	tests := []struct {
		name      string
		objective string
		want      bool
	}{
		{
			name:      "reported PR fix and push",
			objective: "fix and resolve all codex comments in this pr, push when you done https://github.com/oleg-koval/roazon/pull/1513",
			want:      true,
		},
		{
			name:      "explicit repository edit",
			objective: "modify the repository files and commit the changes",
			want:      true,
		},
		{
			name:      "content-only code generation",
			objective: "write a Go function that parses a duration",
			want:      false,
		},
		{
			name:      "pull request summary",
			objective: "summarize https://github.com/oleg-koval/roazon/pull/1513",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, requiresExecutableRuntime(tt.objective))
		})
	}
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

func TestPrintRouteJSONSuccess(t *testing.T) {
	var out bytes.Buffer
	printRouteJSONSuccess(&out, router.ModelCapabilities{
		Name: "sonnet", Source: "builtin", Provider: "anthropic", APIModel: "claude-sonnet", Runtime: "opencode",
		Tier: "mid",
	}, "code-change", "medium", "moderate", 0.937, 0.0123)

	var got routeJSONSuccess
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, routeJSONSuccess{
		Model:      "sonnet",
		Source:     "builtin",
		Provider:   "anthropic",
		APIModel:   "claude-sonnet",
		Runtime:    "opencode",
		Tier:       "mid",
		Kind:       "code-change",
		Risk:       "medium",
		Complexity: "moderate",
		Confidence: 0.937,
		SavedUSD:   0.0123,
	}, got)
	assert.Equal(t, byte('\n'), out.Bytes()[out.Len()-1])
}

func TestPrintRouteJSONError(t *testing.T) {
	var out bytes.Buffer
	printRouteJSONError(&out, "no_candidate", "review", "high", "complex", []routeJSONProviderError{
		{Model: "sol", Detail: "openai api: unsupported parameter"},
	})

	var got routeJSONError
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, routeJSONError{
		Error:      "no_candidate",
		Kind:       "review",
		Risk:       "high",
		Complexity: "complex",
		ProviderErrors: []routeJSONProviderError{
			{Model: "sol", Detail: "openai api: unsupported parameter"},
		},
	}, got)
	assert.Equal(t, byte('\n'), out.Bytes()[out.Len()-1])
}
