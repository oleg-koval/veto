package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/oleg-koval/veto/pkg/router"
)

type routingPreferencesConfig struct {
	PinnedModels      []string `json:"pinned_models,omitempty"`
	PinnedProviders   []string `json:"pinned_providers,omitempty"`
	FavoriteModels    []string `json:"favorite_models,omitempty"`
	FavoriteProviders []string `json:"favorite_providers,omitempty"`
	AllowedModels     []string `json:"allowed_models,omitempty"`
	AllowedProviders  []string `json:"allowed_providers,omitempty"`
	ExcludedModels    []string `json:"excluded_models,omitempty"`
	ExcludedProviders []string `json:"excluded_providers,omitempty"`
}

func loadCandidatePreferences() router.CandidatePreferences {
	data, err := os.ReadFile(vetoCfgPath())
	if err != nil {
		return router.CandidatePreferences{}
	}
	var config struct {
		Routing        routingPreferencesConfig `json:"routing"`
		DisabledModels []string                 `json:"disabled_models"`
	}
	if json.Unmarshal(data, &config) != nil {
		return router.CandidatePreferences{}
	}
	return router.CandidatePreferences{
		PinnedModels:      cleanPreferenceList(config.Routing.PinnedModels),
		PinnedProviders:   cleanPreferenceList(config.Routing.PinnedProviders),
		FavoriteModels:    cleanPreferenceList(config.Routing.FavoriteModels),
		FavoriteProviders: cleanPreferenceList(config.Routing.FavoriteProviders),
		AllowedModels:     cleanPreferenceList(config.Routing.AllowedModels),
		AllowedProviders:  cleanPreferenceList(config.Routing.AllowedProviders),
		DisabledModels:    cleanPreferenceList(config.DisabledModels),
		ExcludedModels:    cleanPreferenceList(config.Routing.ExcludedModels),
		ExcludedProviders: cleanPreferenceList(config.Routing.ExcludedProviders),
	}
}

func cleanPreferenceList(values []string) []string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !slices.Contains(clean, value) {
			clean = append(clean, value)
		}
	}
	return clean
}
