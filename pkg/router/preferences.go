package router

import "slices"

// CandidatePreferences applies user-owned eligibility and ordering policy
// before any candidate receives a paid admission call.
type CandidatePreferences struct {
	PinnedModels      []string
	PinnedProviders   []string
	FavoriteModels    []string
	FavoriteProviders []string
	AllowedModels     []string
	AllowedProviders  []string
	DisabledModels    []string
	ExcludedModels    []string
	ExcludedProviders []string
}

// Filter returns eligible models without mutating the input. Exclusions and
// disables win, allowlists constrain independently, and any pin is exclusive.
func (p CandidatePreferences) Filter(models []ModelCapabilities) []ModelCapabilities {
	out := make([]ModelCapabilities, 0, len(models))
	for _, model := range models {
		if p.excluded(model) || !p.allowed(model) || !p.pinned(model) {
			continue
		}
		out = append(out, model)
	}
	return out
}

// Prioritize stably promotes favorite models, then favorite providers.
func (p CandidatePreferences) Prioritize(ranked []ModelCapabilities) []ModelCapabilities {
	modelFavorites := make([]ModelCapabilities, 0, len(ranked))
	providerFavorites := make([]ModelCapabilities, 0, len(ranked))
	rest := make([]ModelCapabilities, 0, len(ranked))
	for _, model := range ranked {
		switch {
		case matchesModel(p.FavoriteModels, model):
			modelFavorites = append(modelFavorites, model)
		case slices.Contains(p.FavoriteProviders, model.Provider):
			providerFavorites = append(providerFavorites, model)
		default:
			rest = append(rest, model)
		}
	}
	out := append(modelFavorites, providerFavorites...)
	return append(out, rest...)
}

func (p CandidatePreferences) excluded(model ModelCapabilities) bool {
	return matchesModel(p.DisabledModels, model) || matchesModel(p.ExcludedModels, model) ||
		slices.Contains(p.ExcludedProviders, model.Provider)
}

func (p CandidatePreferences) allowed(model ModelCapabilities) bool {
	if len(p.AllowedModels) > 0 && !matchesModel(p.AllowedModels, model) {
		return false
	}
	return len(p.AllowedProviders) == 0 || slices.Contains(p.AllowedProviders, model.Provider)
}

func (p CandidatePreferences) pinned(model ModelCapabilities) bool {
	if len(p.PinnedModels) == 0 && len(p.PinnedProviders) == 0 {
		return true
	}
	return matchesModel(p.PinnedModels, model) || slices.Contains(p.PinnedProviders, model.Provider)
}

func matchesModel(names []string, model ModelCapabilities) bool {
	return slices.Contains(names, model.Name) || (model.APIModel != "" && slices.Contains(names, model.APIModel))
}
