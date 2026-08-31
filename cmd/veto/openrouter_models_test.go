package main

import (
	"testing"

	"github.com/oleg-koval/veto/pkg/execution"
	"github.com/oleg-koval/veto/pkg/openroutercatalog"
	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRouterCatalogCapabilitiesPreserveUnknownValues(t *testing.T) {
	contextLength := 128000
	zero := 0.0
	input := 0.000001
	models := []openroutercatalog.Model{
		{
			ID: "known/free", Name: "Known Free", ContextLength: &contextLength,
			InputModalities: []string{"text"}, OutputModalities: []string{"text"},
			SupportedParameters: []string{"tools"}, Status: openroutercatalog.StatusAvailable,
			PromptUSDPerToken: &zero, CompletionUSDPerToken: &zero,
		},
		{
			ID: "unknown/price", Name: "Unknown Price", Status: openroutercatalog.StatusAvailable,
			InputModalities:   []string{},
			PromptUSDPerToken: &input,
		},
		{ID: "retiring/model", Name: "Retiring", Status: openroutercatalog.StatusScheduledForRemoval},
	}

	got := openRouterCatalogCapabilities(models)
	require.Len(t, got, 2)
	assert.Equal(t, "known/free", got[0].Name)
	assert.Empty(t, got[0].Tier, "catalog does not publish Veto quality tiers")
	assert.Equal(t, 128000, got[0].MaxContextTokens)
	assert.False(t, got[0].CostPer1kInputUnknown)
	assert.False(t, got[0].CostPer1kOutputUnknown)
	assert.Zero(t, got[0].CostPer1kInputUSD)
	assert.Equal(t, []string{"text"}, got[0].InputModalities)
	assert.Equal(t, []string{"tools"}, got[0].SupportedParameters)
	assert.Zero(t, got[1].MaxContextTokens)
	assert.False(t, got[1].CostPer1kInputUnknown)
	assert.True(t, got[1].CostPer1kOutputUnknown)
	assert.Equal(t, 0.001, got[1].CostPer1kInputUSD)
	assert.NotNil(t, got[1].InputModalities, "known empty must differ from unknown")
	assert.Nil(t, got[1].OutputModalities)
}

func TestAddOpenRouterCatalogModelsAppliesPolicyAndPreservesCuratedBinding(t *testing.T) {
	reg := &providerRegistry{
		executors: map[string]execution.RuntimeAdapter{"keep": textOnlyTestExecutor{}},
		caps:      map[string]router.ModelCapabilities{"keep": {Name: "keep", Tier: "large"}},
	}
	models := []openroutercatalog.Model{
		{ID: "keep", Name: "Dynamic Keep", Status: openroutercatalog.StatusAvailable},
		{ID: "add", Name: "Add", Status: openroutercatalog.StatusAvailable},
		{ID: "drop", Name: "Drop", Status: openroutercatalog.StatusAvailable},
	}
	addOpenRouterCatalogModels(reg, "key", models, router.CandidatePreferences{AllowedModels: []string{"keep", "add"}})

	assert.Equal(t, "large", reg.caps["keep"].Tier)
	assert.Contains(t, reg.caps, "add")
	assert.NotContains(t, reg.caps, "drop")
}
