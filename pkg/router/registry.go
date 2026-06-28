package router

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
			Tier:               tierSmall,
			MaxContextTokens:   200000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.00025,
			CostPer1kOutputUSD: 0.00125,
			Strengths:          []TaskKind{KindExtract, KindSummarize},
			Weaknesses:         []TaskKind{KindDebug, KindPlan},
		},
		{
			Name:               "sonnet",
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
			Tier:               tierLarge,
			MaxContextTokens:   200000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search", "computer-use"},
			CostPer1kInputUSD:  0.015,
			CostPer1kOutputUSD: 0.075,
			Strengths:          []TaskKind{KindDebug, KindPlan, KindCodeChange, KindRefactor},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "gpt-4o",
			Tier:               tierMid,
			MaxContextTokens:   128000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit", "web-search"},
			CostPer1kInputUSD:  0.0025,
			CostPer1kOutputUSD: 0.01,
			Strengths:          []TaskKind{KindCodeChange, KindReview, KindRefactor},
			Weaknesses:         []TaskKind{},
		},
		{
			Name:               "gpt-4o-mini",
			Tier:               tierSmall,
			MaxContextTokens:   128000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.00015,
			CostPer1kOutputUSD: 0.0006,
			Strengths:          []TaskKind{KindExtract, KindSummarize},
			Weaknesses:         []TaskKind{KindDebug, KindPlan},
		},
		{
			Name:               "llama-3.1-405b",
			Tier:               tierLarge,
			MaxContextTokens:   128000,
			SupportsTools:      []string{toolBash, toolRead, "write", "edit"},
			CostPer1kInputUSD:  0.0027,
			CostPer1kOutputUSD: 0.0027,
			Strengths:          []TaskKind{KindCodeChange, KindPlan},
			Weaknesses:         []TaskKind{},
		},
	}
}

// NewRegistry returns a registry pre-loaded with the full model catalog.
func NewRegistry() *Registry {
	return &Registry{models: catalog()}
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
