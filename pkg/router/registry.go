package router

import "context"

// tool name constants used across registry and filter logic.
const (
	toolBash = "bash"
	toolRead = "read"
)

const (
	tierSmall = "small"
	tierMid   = "mid"
	tierLarge = "large"
)

// Registry holds the known model capabilities and provides signal lookups.
type Registry struct {
	models []ModelCapabilities
}

// catalog is the full set of models veto knows about across providers.
// A run only considers the subset whose provider is actually configured.
func catalog() []ModelCapabilities {
	return []ModelCapabilities{
		{
			Name:               "haiku",
			Provider:           "anthropic",
			APIModel:           "claude-haiku-4-5-20251001",
			Tier:               tierSmall,
			MaxContextTokens:   200000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.00025,
			CostPer1kOutputUSD: 0.00125,
			Strengths:          []TaskKind{KindExtract, KindSummarize, KindCodeChange},
			Weaknesses:         []TaskKind{KindDebug, KindPlan},
		},
		{
			Name:               "sonnet",
			Provider:           "anthropic",
			APIModel:           "claude-sonnet-4-6",
			Tier:               tierMid,
			MaxContextTokens:   200000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.003,
			CostPer1kOutputUSD: 0.015,
			Strengths:          []TaskKind{KindCodeChange, KindReview, KindRefactor},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "opus",
			Provider:           "anthropic",
			APIModel:           "claude-opus-4-8",
			Tier:               tierLarge,
			MaxContextTokens:   200000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search", "computer-use"},
			CostPer1kInputUSD:  0.015,
			CostPer1kOutputUSD: 0.075,
			Strengths:          []TaskKind{KindDebug, KindPlan, KindCodeChange, KindRefactor},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "gpt-4.1",
			Provider:           "openai",
			APIModel:           "gpt-4.1",
			Tier:               tierMid,
			MaxContextTokens:   1047576,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.002,
			CostPer1kOutputUSD: 0.008,
			Strengths:          []TaskKind{KindCodeChange, KindReview, KindRefactor},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "gpt-4.1-mini",
			Provider:           "openai",
			APIModel:           "gpt-4.1-mini",
			Tier:               tierSmall,
			MaxContextTokens:   1047576,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.0004,
			CostPer1kOutputUSD: 0.0016,
			Strengths:          []TaskKind{KindExtract, KindSummarize},
			Weaknesses:         []TaskKind{KindDebug, KindPlan},
		},
		{
			Name:               "sol",
			Provider:           "openai",
			APIModel:           "gpt-5.6-sol",
			Tier:               tierLarge,
			MaxContextTokens:   1050000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.005,
			CostPer1kOutputUSD: 0.030,
			// sol is planning-only: hard-restricted via Weaknesses to every kind but KindPlan.
			Strengths:  []TaskKind{KindPlan},
			Weaknesses: []TaskKind{KindExtract, KindSummarize, KindCodeChange, KindDebug, KindReview, KindRefactor},
		},
		{
			Name:               "terra",
			Provider:           "openai",
			APIModel:           "gpt-5.6-terra",
			Tier:               tierMid,
			MaxContextTokens:   1050000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.0025,
			CostPer1kOutputUSD: 0.015,
			Strengths:          []TaskKind{KindCodeChange, KindReview, KindRefactor},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "luna",
			Provider:           "openai",
			APIModel:           "gpt-5.6-luna",
			Tier:               tierSmall,
			MaxContextTokens:   1050000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.001,
			CostPer1kOutputUSD: 0.006,
			Strengths:          []TaskKind{KindExtract, KindSummarize},
			Weaknesses:         []TaskKind{KindDebug, KindPlan},
		},
		{
			Name:               "meta-llama/llama-4-maverick",
			Provider:           "openrouter",
			APIModel:           "meta-llama/llama-4-maverick",
			Tier:               tierLarge,
			MaxContextTokens:   1000000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.0002,
			CostPer1kOutputUSD: 0.0006,
			Strengths:          []TaskKind{KindCodeChange, KindPlan},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "grok-3-mini",
			Provider:           "xai",
			APIModel:           "grok-3-mini",
			Tier:               tierSmall,
			MaxContextTokens:   131072,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.0003,
			CostPer1kOutputUSD: 0.0005,
			Strengths:          []TaskKind{KindExtract, KindSummarize, KindCodeChange},
			Weaknesses:         []TaskKind{KindDebug, KindPlan},
		},
		{
			Name:               "grok-3",
			Provider:           "xai",
			APIModel:           "grok-3",
			Tier:               tierMid,
			MaxContextTokens:   131072,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.003,
			CostPer1kOutputUSD: 0.015,
			Strengths:          []TaskKind{KindCodeChange, KindReview, KindRefactor, KindDebug},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "grok-4.3",
			Provider:           "xai",
			APIModel:           "grok-4.3",
			Tier:               tierLarge,
			MaxContextTokens:   1000000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.00125,
			CostPer1kOutputUSD: 0.0025,
			Strengths:          []TaskKind{KindCodeChange, KindReview, KindRefactor, KindDebug, KindPlan},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "grok-4.5",
			Provider:           "xai",
			APIModel:           "grok-4.5",
			Tier:               tierLarge,
			MaxContextTokens:   500000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.002,
			CostPer1kOutputUSD: 0.006,
			Strengths:          []TaskKind{KindCodeChange, KindReview, KindRefactor, KindDebug, KindPlan},
			Weaknesses:         []TaskKind{},
		},
	}
}

// NewRegistry returns a registry pre-loaded with the full model catalog.
func NewRegistry() *Registry {
	models, _ := (StaticModelSource{}).Models(context.Background())
	return &Registry{models: models}
}

// NewRegistryFor returns a registry containing only the named catalog models —
// used to route across just the providers the user has configured. Unknown
// names are ignored; an empty list yields the full catalog.
func NewRegistryFor(names []string) *Registry {
	if len(names) == 0 {
		return NewRegistry()
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var models []ModelCapabilities
	for _, m := range catalog() {
		if want[m.Name] {
			models = append(models, m)
		}
	}
	return &Registry{models: models}
}

// NewRegistryFromModels builds a registry from an explicit capability list.
// Use this when the caller assembles the model list from both built-ins and
// user-defined local models, rather than filtering the catalog by name.
func NewRegistryFromModels(models []ModelCapabilities) *Registry {
	return &Registry{models: models}
}

// All returns all registered models as a copy.
func (r *Registry) All() []ModelCapabilities {
	out := make([]ModelCapabilities, len(r.models))
	copy(out, r.models)
	return out
}

// ByName returns the model with the given name, or false if not found.
func (r *Registry) ByName(name string) (ModelCapabilities, bool) {
	for _, m := range r.models {
		if m.Name == name {
			return m, true
		}
	}
	return ModelCapabilities{}, false
}

// Signal returns historical routing data for a model+kind pair.
// MVP stub: returns a neutral baseline; a real store replaces this in Phase 2.
func (r *Registry) Signal(_ string, _ TaskKind) RoutingSignal {
	return RoutingSignal{
		HistoricalSuccessRate: 0.5,
		HistoricalRejectRate:  0.1,
		AvgEvalScore:          0.7,
		RecentCostUSD:         0.0,
		RecentLatencyMs:       0.0,
	}
}
