package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCandidatePreferencesIncludesLegacyDisabledModels(t *testing.T) {
	oldPath := vetoCfgPathOverride
	vetoCfgPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { vetoCfgPathOverride = oldPath })
	require.NoError(t, os.WriteFile(vetoCfgPath(), []byte(`{
  "disabled_models": [" old ", "old"],
  "routing": {
    "pinned_models": ["pin"],
    "pinned_providers": ["openrouter"],
    "favorite_models": ["fav"],
    "favorite_providers": ["local"],
    "allowed_models": ["allow"],
    "allowed_providers": ["openrouter"],
    "excluded_models": ["exclude"],
    "excluded_providers": ["xai"]
  }
}`), 0600))

	got := loadCandidatePreferences()
	assert.Equal(t, []string{"old"}, got.DisabledModels)
	assert.Equal(t, []string{"pin"}, got.PinnedModels)
	assert.Equal(t, []string{"openrouter"}, got.PinnedProviders)
	assert.Equal(t, []string{"fav"}, got.FavoriteModels)
	assert.Equal(t, []string{"local"}, got.FavoriteProviders)
	assert.Equal(t, []string{"allow"}, got.AllowedModels)
	assert.Equal(t, []string{"openrouter"}, got.AllowedProviders)
	assert.Equal(t, []string{"exclude"}, got.ExcludedModels)
	assert.Equal(t, []string{"xai"}, got.ExcludedProviders)
}
