package router

import (
	"context"
	"strings"
	"testing"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/router/mocks"
	"github.com/stretchr/testify/assert"
)

func TestInferComplexity(t *testing.T) {
	tests := []struct {
		objective string
		kind      TaskKind
		want      Complexity
	}{
		// complex — specific architectural terms
		{"build e2e CQRS infrastructure with event sourcing", KindCodeChange, ComplexityComplex},
		{"design microservices architecture for distributed payment system", KindPlan, ComplexityComplex},
		{"build production-grade distributed orchestration platform from scratch", KindCodeChange, ComplexityComplex},
		{"implement multi-tenant architecture with event-driven microservices", KindCodeChange, ComplexityComplex},

		// moderate — technical but not architectural
		{"implement authentication service with JWT", KindCodeChange, ComplexityModerate},
		{"deploy the api pipeline to staging", KindCodeChange, ComplexityModerate},
		{"debug the payment service crash", KindDebug, ComplexityModerate},
		{"integrate stripe with the checkout flow", KindCodeChange, ComplexityModerate},
		{"fix and resolve all codex comments in this pr, push when you done https://github.com/oleg-koval/roazon/pull/1513", KindCodeChange, ComplexityModerate},

		// simple — short, everyday tasks
		{"create simple html page containing short history of Amsterdam", KindCodeChange, ComplexitySimple},
		{"fix the login bug", KindCodeChange, ComplexitySimple},
		{"add a unit test", KindCodeChange, ComplexitySimple},
		{"update the readme", KindCodeChange, ComplexitySimple},
		{"rename function foo to bar", KindRefactor, ComplexitySimple},
		{"summarize this document", KindSummarize, ComplexitySimple},
		{"extract the email addresses", KindExtract, ComplexitySimple},

		// kind-driven: plan bumps score even for vague objectives
		{"plan the next quarter features", KindPlan, ComplexityModerate},
	}

	for _, tt := range tests {
		t.Run(tt.objective, func(t *testing.T) {
			got := InferComplexity(tt.objective, tt.kind)
			assert.Equal(t, tt.want, got, "objective: %q kind: %s", tt.objective, tt.kind)
		})
	}
}

func TestTierMeetsComplexity(t *testing.T) {
	tests := []struct {
		tier       string
		complexity Complexity
		want       bool
	}{
		{tierSmall, ComplexitySimple, true},
		{tierMid, ComplexitySimple, true},
		{tierLarge, ComplexitySimple, true},

		{tierSmall, ComplexityModerate, false},
		{tierMid, ComplexityModerate, true},
		{tierLarge, ComplexityModerate, true},

		{tierSmall, ComplexityComplex, false},
		{tierMid, ComplexityComplex, false},
		{tierLarge, ComplexityComplex, true},

		// empty complexity treated as simple
		{tierSmall, "", true},
	}

	for _, tt := range tests {
		got := tierMeetsComplexity(tt.tier, tt.complexity)
		assert.Equal(t, tt.want, got, "tier=%s complexity=%s", tt.tier, tt.complexity)
	}
}

func TestHardFilter_ComplexityPrunesSmallModels(t *testing.T) {
	models := []ModelCapabilities{
		{Name: "haiku", Tier: tierSmall},
		{Name: "sonnet", Tier: tierMid},
		{Name: "opus", Tier: tierLarge},
	}
	task := TaskSpec{Kind: KindCodeChange, Complexity: ComplexityComplex}
	got := HardFilter(task, models)

	assert.Len(t, got, 1)
	assert.Equal(t, "opus", got[0].Name)
}

func TestManager_Route_ComplexTaskRestrictsToLargeTier(t *testing.T) {
	var asked []string
	exec := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, prompt string) executor.Result {
			for _, name := range []string{"haiku", "sonnet", "gpt-4.1-mini", "gpt-4.1", "opus", "meta-llama/llama-4-maverick"} {
				if strings.Contains(prompt, "You are the "+name) {
					asked = append(asked, name)
				}
			}
			return executor.Result{Output: acceptJSON()}
		},
	}

	reg := NewRegistryFor([]string{"haiku", "sonnet", "opus", "meta-llama/llama-4-maverick"})
	gate := NewAdmissionGate(exec)
	mgr := NewManager(reg, gate, NewMemoryStore())

	// manager must infer complex from the objective and restrict to large tier
	task := TaskSpec{
		ID:        "cqrs",
		Kind:      KindCodeChange,
		Objective: "build e2e CQRS infrastructure with event sourcing",
	}

	model, _, err := mgr.Route(context.Background(), task)
	assert.NoError(t, err)
	assert.Equal(t, tierLarge, model.Tier, "complex task must route to large-tier model only")
	for _, name := range asked {
		m, _ := reg.ByName(name)
		assert.Equal(t, tierLarge, m.Tier, "only large-tier models should be asked")
	}
}
