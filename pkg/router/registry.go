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

// NewRegistry returns a registry pre-loaded with three hardcoded model tiers.
// haiku → small (extraction, cleanup), sonnet → mid (general), opus → large (deep reasoning).
func NewRegistry() *Registry {
	return &Registry{
		models: []ModelCapabilities{
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
		},
	}
}

// All returns all registered models.
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
