package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	reg := NewRegistry()
	models := reg.All()
	// full catalog spans Anthropic + OpenAI + OpenRouter
	assert.GreaterOrEqual(t, len(models), 6)
	for _, want := range []string{"haiku", "sonnet", "opus", "gpt-4o", "gpt-4o-mini"} {
		_, ok := reg.ByName(want)
		assert.True(t, ok, "catalog should include %s", want)
	}
}

func TestNewRegistryFor_FiltersToNamed(t *testing.T) {
	reg := NewRegistryFor([]string{"gpt-4o", "gpt-4o-mini"})
	models := reg.All()
	assert.Len(t, models, 2)
	_, ok := reg.ByName("gpt-4o")
	assert.True(t, ok)
	_, ok = reg.ByName("opus")
	assert.False(t, ok, "opus is not configured, must be excluded")
}

func TestNewRegistryFor_IgnoresUnknownNames(t *testing.T) {
	reg := NewRegistryFor([]string{"gpt-4o", "no-such-model"})
	assert.Len(t, reg.All(), 1)
}

func TestNewRegistryFor_EmptyYieldsFullCatalog(t *testing.T) {
	full := NewRegistry().All()
	got := NewRegistryFor(nil).All()
	assert.Len(t, got, len(full))
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
