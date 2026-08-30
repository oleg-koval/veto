package main

import (
	"context"

	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/openroutercatalog"
	"github.com/oleg-koval/veto/pkg/router"
)

func openRouterCatalogCapabilities(models []openroutercatalog.Model) []router.ModelCapabilities {
	capabilities := make([]router.ModelCapabilities, 0, len(models))
	for _, model := range models {
		if model.Status != openroutercatalog.StatusAvailable {
			continue
		}
		capability := router.ModelCapabilities{
			Name:                   model.ID,
			Source:                 "openrouter-catalog",
			Provider:               "openrouter",
			APIModel:               model.ID,
			InputModalities:        cloneCatalogStrings(model.InputModalities),
			OutputModalities:       cloneCatalogStrings(model.OutputModalities),
			SupportedParameters:    cloneCatalogStrings(model.SupportedParameters),
			SupportsTools:          []string{},
			CostPer1kInputUnknown:  model.PromptUSDPerToken == nil,
			CostPer1kOutputUnknown: model.CompletionUSDPerToken == nil,
		}
		if model.ContextLength != nil {
			capability.MaxContextTokens = *model.ContextLength
		}
		if model.PromptUSDPerToken != nil {
			capability.CostPer1kInputUSD = *model.PromptUSDPerToken * 1000
		}
		if model.CompletionUSDPerToken != nil {
			capability.CostPer1kOutputUSD = *model.CompletionUSDPerToken * 1000
		}
		capabilities = append(capabilities, capability)
	}
	return capabilities
}

func cloneCatalogStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func loadOpenRouterCatalogModels(apiKey, cachePath string, offline bool) ([]openroutercatalog.Model, error) {
	snapshot, err := openroutercatalog.New(apiKey, cachePath).Load(context.Background(), offline)
	if err != nil {
		return nil, err
	}
	return snapshot.Models, nil
}

func addOpenRouterCatalogModels(reg *providerRegistry, apiKey string, models []openroutercatalog.Model, preferences router.CandidatePreferences) {
	for _, capability := range preferences.Filter(openRouterCatalogCapabilities(models)) {
		if _, exists := reg.caps[capability.Name]; exists {
			continue
		}
		reg.caps[capability.Name] = capability
		reg.executors[capability.Name] = executor.NewOpenRouterExecutor(apiKey, capability.APIModel)
	}
}
