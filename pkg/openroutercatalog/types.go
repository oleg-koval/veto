// Package openroutercatalog fetches and caches OpenRouter model metadata.
package openroutercatalog

import "time"

// CacheState indicates whether cached model metadata is still fresh.
type CacheState string

// CacheState constants define cache freshness levels.
const (
	StateFresh CacheState = "fresh"
	StateStale CacheState = "stale"
)

// ModelStatus indicates a model's availability in the OpenRouter catalog.
type ModelStatus string

// ModelStatus constants define catalog availability states.
const (
	StatusAvailable           ModelStatus = "available"
	StatusScheduledForRemoval ModelStatus = "scheduled_for_removal"
)

// Model is the validated subset of OpenRouter catalog metadata used by Veto.
// Pointer values preserve unknown prices and context separately from zero.
type Model struct {
	ID                    string      `json:"id"`
	Name                  string      `json:"name"`
	ContextLength         *int        `json:"context_length,omitempty"`
	InputModalities       []string    `json:"input_modalities"`
	OutputModalities      []string    `json:"output_modalities"`
	SupportedParameters   []string    `json:"supported_parameters"`
	Status                ModelStatus `json:"status"`
	ExpirationDate        string      `json:"expiration_date,omitempty"`
	PromptUSDPerToken     *float64    `json:"prompt_usd_per_token,omitempty"`
	CompletionUSDPerToken *float64    `json:"completion_usd_per_token,omitempty"`
}

// Snapshot is a usable catalog plus explicit cache and network state.
type Snapshot struct {
	Models    []Model
	FetchedAt time.Time
	ETag      string
	State     CacheState
	Offline   bool
}
