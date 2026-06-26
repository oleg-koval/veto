package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	models := reg.All()
	assert.Len(t, models, 3, "default registry should have 3 tiers")
}

func TestRegistry_All_ReturnsCopy(t *testing.T) {
	reg := NewRegistry()
	a := reg.All()
	b := reg.All()
	a[0].Name = "mutated"
	assert.NotEqual(t, a[0].Name, b[0].Name, "All() must return a copy")
}

func TestRegistry_ByName(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		wantOK    bool
	}{
		{"haiku exists", "haiku", true},
		{"sonnet exists", "sonnet", true},
		{"opus exists", "opus", true},
		{"unknown absent", "gpt-4", false},
	}
	reg := NewRegistry()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok := reg.ByName(tt.modelName)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.modelName, m.Name)
			}
		})
	}
}

func TestRegistry_Signal_NeutralBaseline(t *testing.T) {
	reg := NewRegistry()
	sig := reg.Signal("any-model", KindCodeChange)
	assert.InDelta(t, 0.5, sig.HistoricalSuccessRate, 0.001)
	assert.InDelta(t, 0.1, sig.HistoricalRejectRate, 0.001)
	assert.InDelta(t, 0.7, sig.AvgEvalScore, 0.001)
}

func TestRegistry_ModelTiers(t *testing.T) {
	reg := NewRegistry()
	haiku, ok := reg.ByName("haiku")
	require.True(t, ok)
	assert.Equal(t, "small", haiku.Tier)
	assert.Greater(t, haiku.CostPer1kInputUSD, 0.0)

	opus, ok := reg.ByName("opus")
	require.True(t, ok)
	assert.Greater(t, opus.CostPer1kInputUSD, haiku.CostPer1kInputUSD, "opus must cost more than haiku")
}
