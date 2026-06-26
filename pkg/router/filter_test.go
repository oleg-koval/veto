package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func testModel(name string, maxCtx int, tools []string, cost float64) ModelCapabilities {
	return ModelCapabilities{
		Name:               name,
		MaxContextTokens:   maxCtx,
		SupportsTools:      tools,
		CostPer1kInputUSD:  cost,
		CostPer1kOutputUSD: cost,
	}
}

func TestHardFilter(t *testing.T) {
	allTools := []string{"bash", "read", "write"}
	small := testModel("small", 4096, allTools, 0.001)
	large := testModel("large", 200000, allTools, 0.01)
	noTool := testModel("notool", 200000, []string{"read"}, 0.001)
	weak := ModelCapabilities{
		Name:               "weak",
		MaxContextTokens:   200000,
		SupportsTools:      allTools,
		CostPer1kInputUSD:  0.001,
		CostPer1kOutputUSD: 0.001,
		Weaknesses:         []TaskKind{KindDebug},
	}

	tests := []struct {
		name       string
		task       TaskSpec
		models     []ModelCapabilities
		wantNames  []string
	}{
		{
			name:      "no filters — all pass",
			task:      TaskSpec{Kind: KindCodeChange},
			models:    []ModelCapabilities{small, large},
			wantNames: []string{"small", "large"},
		},
		{
			name:      "context limit filters small",
			task:      TaskSpec{Kind: KindCodeChange, MaxTokens: 10000},
			models:    []ModelCapabilities{small, large},
			wantNames: []string{"large"},
		},
		{
			name:      "missing tool filters noTool",
			task:      TaskSpec{Kind: KindCodeChange, RequiredTools: []string{"bash"}},
			models:    []ModelCapabilities{noTool, large},
			wantNames: []string{"large"},
		},
		{
			name:      "cost ceiling filters expensive",
			task:      TaskSpec{Kind: KindCodeChange, MaxTokens: 1000, MaxCostUSD: 0.005},
			models:    []ModelCapabilities{small, large},
			wantNames: []string{"small"},
		},
		{
			name:      "weak kind filters weak model",
			task:      TaskSpec{Kind: KindDebug},
			models:    []ModelCapabilities{weak, large},
			wantNames: []string{"large"},
		},
		{
			name:      "empty input",
			task:      TaskSpec{Kind: KindCodeChange},
			models:    nil,
			wantNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HardFilter(tt.task, tt.models)
			names := make([]string, len(got))
			for i, m := range got {
				names[i] = m.Name
			}
			assert.Equal(t, tt.wantNames, names)
		})
	}
}

func TestHasRequiredTools(t *testing.T) {
	m := ModelCapabilities{SupportsTools: []string{"bash", "read"}}
	assert.True(t, hasRequiredTools(m, []string{"bash"}))
	assert.True(t, hasRequiredTools(m, []string{"bash", "read"}))
	assert.False(t, hasRequiredTools(m, []string{"write"}))
	assert.True(t, hasRequiredTools(m, nil))
}

func TestEstimatedCost(t *testing.T) {
	m := ModelCapabilities{CostPer1kInputUSD: 0.01, CostPer1kOutputUSD: 0.01}
	// 1000 input tokens * 0.01 + 100 output tokens (10%) * 0.01 = 0.011
	cost := estimatedCost(m, TaskSpec{MaxTokens: 1000})
	assert.InDelta(t, 0.011, cost, 0.0001)

	// no MaxTokens — defaults to 1000 input tokens
	cost2 := estimatedCost(m, TaskSpec{})
	assert.InDelta(t, 0.011, cost2, 0.0001)
}
