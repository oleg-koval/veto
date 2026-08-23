package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyProviderModels_RecordsResponseAndMissingIDs(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1"},{"id":"other"}]}`))
	}))
	t.Cleanup(server.Close)

	artifactDir := t.TempDir()
	result, err := verifyProviderModels(context.Background(), modelListProviders["openai"], "secret-key", server.URL, artifactDir, time.Second)
	require.NoError(t, err)
	assert.Equal(t, "Bearer secret-key", gotAuth)
	assert.Equal(t, []string{"gpt-4.1", "gpt-4.1-mini", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"}, result.ConfiguredModels)
	assert.Contains(t, result.AvailableModels, "gpt-4.1")
	assert.Contains(t, result.MissingModels, "gpt-4.1-mini")
	require.NotEmpty(t, result.Artifact)
	body, readErr := os.ReadFile(result.Artifact)
	require.NoError(t, readErr)
	assert.Contains(t, string(body), `"gpt-4.1"`)
	meta, globErr := filepath.Glob(strings.TrimSuffix(result.Artifact, ".json") + ".meta.json")
	require.NoError(t, globErr)
	assert.Len(t, meta, 1)
}

func TestVerifyProviderModels_HTTPErrorPersistsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not authorized"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	result, err := verifyProviderModels(context.Background(), modelListProviders["openai"], "secret-key", server.URL, t.TempDir(), time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 401")
	assert.NotEmpty(t, result.Artifact)
	body, readErr := os.ReadFile(result.Artifact)
	require.NoError(t, readErr)
	assert.Contains(t, string(body), "not authorized")
}

func TestVerifyProviderModels_RejectsUnsafeEndpoint(t *testing.T) {
	_, err := verifyProviderModels(context.Background(), modelListProviders["openai"], "secret-key", "https://example.test/v1/models?api_key=leak", t.TempDir(), time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query parameters")
}
