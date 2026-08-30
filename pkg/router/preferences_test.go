package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCandidatePreferencesFilterPrecedence(t *testing.T) {
	models := []ModelCapabilities{
		{Name: "openai/a", Provider: "openrouter"},
		{Name: "anthropic/b", Provider: "openrouter"},
		{Name: "local", Provider: "local"},
	}
	prefs := CandidatePreferences{
		PinnedProviders:   []string{"openrouter"},
		AllowedModels:     []string{"openai/a", "anthropic/b"},
		DisabledModels:    []string{"anthropic/b"},
		ExcludedProviders: []string{"local"},
	}

	assert.Equal(t, []ModelCapabilities{models[0]}, prefs.Filter(models))
}

func TestCandidatePreferencesPinAndAllowSemantics(t *testing.T) {
	models := []ModelCapabilities{
		{Name: "a", Provider: "one"},
		{Name: "b", Provider: "two"},
		{Name: "c", Provider: "two"},
	}

	assert.Equal(t, []ModelCapabilities{models[1]}, (CandidatePreferences{
		PinnedModels: []string{"b"},
	}).Filter(models))
	assert.Equal(t, []ModelCapabilities{models[1], models[2]}, (CandidatePreferences{
		PinnedProviders: []string{"two"},
	}).Filter(models))
	assert.Equal(t, []ModelCapabilities{models[1]}, (CandidatePreferences{
		AllowedModels:    []string{"b", "c"},
		AllowedProviders: []string{"two"},
		ExcludedModels:   []string{"c"},
	}).Filter(models))
}

func TestCandidatePreferencesPromotesFavoritesStably(t *testing.T) {
	ranked := []ModelCapabilities{
		{Name: "a", Provider: "one"},
		{Name: "b", Provider: "two"},
		{Name: "c", Provider: "two"},
		{Name: "d", Provider: "three"},
	}
	prefs := CandidatePreferences{
		FavoriteModels:    []string{"c"},
		FavoriteProviders: []string{"three"},
	}

	ordered := prefs.Prioritize(ranked)
	assert.Equal(t, []string{"c", "d", "a", "b"}, []string{
		ordered[0].Name, ordered[1].Name, ordered[2].Name, ordered[3].Name,
	})
	assert.Equal(t, "a", ranked[0].Name, "input must not be mutated")
}
