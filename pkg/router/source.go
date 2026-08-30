package router

import "context"

// ModelSource discovers model metadata without choosing how models execute.
type ModelSource interface {
	SourceID() string
	Models(ctx context.Context) ([]ModelCapabilities, error)
}

// StaticModelSource provides veto's built-in model catalog.
type StaticModelSource struct{}

// SourceID returns the identifier for the built-in catalog source.
func (StaticModelSource) SourceID() string { return "builtin" }

// Models returns the full built-in catalog with source metadata populated.
func (source StaticModelSource) Models(_ context.Context) ([]ModelCapabilities, error) {
	models := catalog()
	for i := range models {
		models[i].Source = source.SourceID()
	}
	return models, nil
}

var _ ModelSource = StaticModelSource{}
