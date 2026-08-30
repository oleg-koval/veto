package openroutercatalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFetchesValidCatalogAndCaches(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/models", r.URL.Path)
		assert.Equal(t, "1000", r.URL.Query().Get("limit"))
		assert.Equal(t, "all", r.URL.Query().Get("output_modalities"))
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("ETag", `"catalog-v1"`)
		_ = json.NewEncoder(w).Encode(validCatalogResponse())
	}))
	t.Cleanup(server.Close)

	cachePath := filepath.Join(t.TempDir(), "cache", "openrouter-models.json")
	client := New("test-key", cachePath)
	client.endpoint = server.URL + "/api/v1/models"
	client.httpClient = server.Client()
	client.now = func() time.Time { return fetchedAt }

	snapshot, err := client.Load(t.Context(), false)
	require.NoError(t, err)
	require.Len(t, snapshot.Models, 1)
	assert.Equal(t, StateFresh, snapshot.State)
	assert.False(t, snapshot.Offline)
	assert.Equal(t, fetchedAt, snapshot.FetchedAt)
	assert.Equal(t, `"catalog-v1"`, snapshot.ETag)
	model := snapshot.Models[0]
	assert.Equal(t, "openai/gpt-4", model.ID)
	assert.Equal(t, "GPT-4", model.Name)
	require.NotNil(t, model.ContextLength)
	assert.Equal(t, 8192, *model.ContextLength)
	assert.Equal(t, []string{"text"}, model.InputModalities)
	assert.Equal(t, []string{"text"}, model.OutputModalities)
	assert.Equal(t, []string{"max_tokens", "temperature"}, model.SupportedParameters)
	assert.Equal(t, StatusAvailable, model.Status)
	require.NotNil(t, model.PromptUSDPerToken)
	assert.Equal(t, 0.00003, *model.PromptUSDPerToken)
	require.NotNil(t, model.CompletionUSDPerToken)
	assert.Equal(t, 0.00006, *model.CompletionUSDPerToken)

	cacheBody, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	assert.NotContains(t, string(cacheBody), "test-key")
	info, err := os.Stat(cachePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func validCatalogResponse() map[string]any {
	return map[string]any{
		"total_count": 1,
		"data": []map[string]any{{
			"id":             "openai/gpt-4",
			"name":           "GPT-4",
			"context_length": 8192,
			"architecture": map[string]any{
				"input_modalities":  []string{"text"},
				"output_modalities": []string{"text"},
			},
			"supported_parameters": []string{"temperature", "max_tokens"},
			"pricing": map[string]any{
				"prompt":     "0.00003",
				"completion": "0.00006",
			},
			"expiration_date": nil,
		}},
	}
}
