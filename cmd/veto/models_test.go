package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTempModelsPath(t *testing.T) {
	t.Helper()
	localModelsPathOverride = filepath.Join(t.TempDir(), "models.json")
	t.Cleanup(func() { localModelsPathOverride = "" })
}

func TestSaveAndLoadLocalModel(t *testing.T) {
	setTempModelsPath(t)

	lm := LocalModel{Name: "test-local", Endpoint: "http://localhost:11434/v1/chat/completions", Model: "llama3"}
	require.NoError(t, saveLocalModel(lm))

	models, err := loadLocalModels()
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "test-local", models[0].Name)
	assert.Equal(t, "llama3", models[0].Model)
}

func TestSaveLocalModel_ReplacesExisting(t *testing.T) {
	setTempModelsPath(t)

	require.NoError(t, saveLocalModel(LocalModel{Name: "mymodel", Endpoint: "http://localhost:11434/v1/chat/completions", Model: "v1"}))
	require.NoError(t, saveLocalModel(LocalModel{Name: "mymodel", Endpoint: "http://localhost:11434/v1/chat/completions", Model: "v2"}))

	models, err := loadLocalModels()
	require.NoError(t, err)
	require.Len(t, models, 1, "replace by name — must not duplicate")
	assert.Equal(t, "v2", models[0].Model)
}

func TestRemoveLocalModel(t *testing.T) {
	setTempModelsPath(t)

	require.NoError(t, saveLocalModel(LocalModel{Name: "a", Endpoint: "http://localhost:11434/v1/chat/completions", Model: "m1"}))
	require.NoError(t, saveLocalModel(LocalModel{Name: "b", Endpoint: "http://localhost:11434/v1/chat/completions", Model: "m2"}))

	require.NoError(t, removeLocalModel("a"))

	models, err := loadLocalModels()
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "b", models[0].Name)
}

func TestRemoveLocalModel_NotFound(t *testing.T) {
	setTempModelsPath(t)
	// no error, just prints a message
	require.NoError(t, removeLocalModel("nonexistent"))
}

func TestLoadLocalModels_NoFile(t *testing.T) {
	setTempModelsPath(t)
	models, err := loadLocalModels()
	require.NoError(t, err)
	assert.Nil(t, models)
}

func TestLocalModelCapabilities_Defaults(t *testing.T) {
	lm := LocalModel{Name: "x", Endpoint: "http://localhost/v1/chat/completions", Model: "y"}
	caps := lm.capabilities()

	assert.Equal(t, "small", caps.Tier)
	assert.Equal(t, 8192, caps.MaxContextTokens)
	assert.Equal(t, []string{"bash", "read", "write", "edit"}, caps.SupportsTools)
	assert.Zero(t, caps.CostPer1kInputUSD)
	assert.Zero(t, caps.CostPer1kOutputUSD)
}

func TestLocalModelCapabilities_Overrides(t *testing.T) {
	lm := LocalModel{
		Name:             "x",
		Endpoint:         "http://localhost/v1/chat/completions",
		Model:            "y",
		Tier:             "large",
		MaxContextTokens: 32768,
		SupportsTools:    []string{"bash"},
		Strengths:        []string{"code-change"},
		Weaknesses:       []string{"plan"},
	}
	caps := lm.capabilities()
	assert.Equal(t, "large", caps.Tier)
	assert.Equal(t, 32768, caps.MaxContextTokens)
	assert.Equal(t, []string{"bash"}, caps.SupportsTools)
	require.Len(t, caps.Strengths, 1)
	require.Len(t, caps.Weaknesses, 1)
}

func TestValidateLocalModel(t *testing.T) {
	builtins := map[string]bool{"opus": true, "sonnet": true, "haiku": true}

	cases := []struct {
		name    string
		lm      LocalModel
		wantErr bool
	}{
		{"valid", LocalModel{Name: "local-a", Endpoint: "http://localhost:11434/v1/chat/completions", Model: "m"}, false},
		{"empty name", LocalModel{Endpoint: "http://localhost/v1/c", Model: "m"}, true},
		{"empty endpoint", LocalModel{Name: "x", Model: "m"}, true},
		{"empty model", LocalModel{Name: "x", Endpoint: "http://localhost/v1/c"}, true},
		{"bad url", LocalModel{Name: "x", Endpoint: "not-a-url", Model: "m"}, true},
		{"catalog conflict", LocalModel{Name: "opus", Endpoint: "http://localhost/v1/c", Model: "m"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLocalModel(tc.lm, builtins)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
