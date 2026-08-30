package openroutercatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

func TestLoadUsesExplicitFreshStaleAndOfflineStates(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	cachePath := filepath.Join(t.TempDir(), "openrouter-models.json")
	require.NoError(t, writeCacheFile(cachePath, testCache(fetchedAt)))

	t.Run("fresh cache avoids network", func(t *testing.T) {
		client := New("", cachePath)
		client.now = func() time.Time { return fetchedAt.Add(time.Minute) }
		client.httpClient = doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("fresh cache must not make a request")
			return nil, nil
		})

		snapshot, err := client.Load(t.Context(), false)
		require.NoError(t, err)
		assert.Equal(t, StateFresh, snapshot.State)
		assert.False(t, snapshot.Offline)
	})

	t.Run("offline stale cache avoids network", func(t *testing.T) {
		client := New("", cachePath)
		client.now = func() time.Time { return fetchedAt.Add(time.Hour) }
		client.httpClient = doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("offline mode must not make a request")
			return nil, nil
		})

		snapshot, err := client.Load(t.Context(), true)
		require.NoError(t, err)
		assert.Equal(t, StateStale, snapshot.State)
		assert.True(t, snapshot.Offline)
	})
}

func TestLoadRefreshesStaleCacheConditionally(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	now := fetchedAt.Add(time.Hour)
	cachePath := filepath.Join(t.TempDir(), "openrouter-models.json")
	require.NoError(t, writeCacheFile(cachePath, testCache(fetchedAt)))

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, `"old-etag"`, r.Header.Get("If-None-Match"))
		w.Header().Set("ETag", `"new-etag"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(server.Close)
	client := New("", cachePath)
	client.endpoint = server.URL
	client.httpClient = server.Client()
	client.now = func() time.Time { return now }

	snapshot, err := client.Load(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, StateFresh, snapshot.State)
	assert.Equal(t, now, snapshot.FetchedAt)
	assert.Equal(t, `"new-etag"`, snapshot.ETag)

	persisted, err := readCacheFile(cachePath, defaultMaxResponseBytes)
	require.NoError(t, err)
	assert.Equal(t, now, persisted.FetchedAt)
	assert.Equal(t, `"new-etag"`, persisted.ETag)
}

func TestLoadNeverReplacesKnownGoodCacheWithInvalidResponse(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "malformed", body: []byte(`{broken`)},
		{name: "partial", body: mustJSON(t, map[string]any{
			"total_count": 2,
			"data":        validCatalogResponse()["data"],
		})},
	} {
		t.Run(test.name, func(t *testing.T) {
			cachePath := filepath.Join(t.TempDir(), "openrouter-models.json")
			require.NoError(t, writeCacheFile(cachePath, testCache(fetchedAt)))
			before, err := os.ReadFile(cachePath)
			require.NoError(t, err)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(test.body)
			}))
			t.Cleanup(server.Close)
			client := New("", cachePath)
			client.endpoint = server.URL
			client.httpClient = server.Client()
			client.now = func() time.Time { return fetchedAt.Add(time.Hour) }

			snapshot, err := client.Load(t.Context(), false)
			require.NoError(t, err)
			assert.Equal(t, StateStale, snapshot.State)
			assert.Equal(t, "cached/model", snapshot.Models[0].ID)
			after, err := os.ReadFile(cachePath)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestLoadBoundsResponseAndRequestTime(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		handler   http.HandlerFunc
		configure func(*Client)
	}{
		{
			name: "oversized",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(bytes.Repeat([]byte("x"), 128))
			},
			configure: func(client *Client) { client.maxResponseBytes = 64 },
		},
		{
			name: "timeout",
			handler: func(_ http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			},
			configure: func(client *Client) { client.timeout = 20 * time.Millisecond },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cachePath := filepath.Join(t.TempDir(), "openrouter-models.json")
			require.NoError(t, writeCacheFile(cachePath, testCache(fetchedAt)))
			server := httptest.NewTLSServer(test.handler)
			t.Cleanup(server.Close)
			client := New("", cachePath)
			client.endpoint = server.URL
			client.httpClient = server.Client()
			client.now = func() time.Time { return fetchedAt.Add(time.Hour) }
			test.configure(client)

			started := time.Now()
			snapshot, err := client.Load(t.Context(), false)
			require.NoError(t, err)
			assert.Less(t, time.Since(started), time.Second)
			assert.Equal(t, StateStale, snapshot.State)
			assert.Equal(t, "cached/model", snapshot.Models[0].ID)
		})
	}
}

func TestLoadRollsBackToKnownGoodCacheWhenWriteFails(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	cachePath := filepath.Join(t.TempDir(), "openrouter-models.json")
	require.NoError(t, writeCacheFile(cachePath, testCache(fetchedAt)))
	before, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(validCatalogResponse())
	}))
	t.Cleanup(server.Close)
	client := New("", cachePath)
	client.endpoint = server.URL
	client.httpClient = server.Client()
	client.now = func() time.Time { return fetchedAt.Add(time.Hour) }
	client.writeCache = func(string, persistedCache) error { return errors.New("disk full") }

	snapshot, err := client.Load(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, StateStale, snapshot.State)
	assert.Equal(t, fetchedAt, snapshot.FetchedAt)
	assert.Equal(t, "cached/model", snapshot.Models[0].ID)
	after, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestLoadNotModifiedWriteFailureKeepsOriginalStaleTimestamp(t *testing.T) {
	fetchedAt := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	cachePath := filepath.Join(t.TempDir(), "openrouter-models.json")
	require.NoError(t, writeCacheFile(cachePath, testCache(fetchedAt)))
	client := New("", cachePath)
	client.now = func() time.Time { return fetchedAt.Add(time.Hour) }
	client.httpClient = doerFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotModified,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    request,
		}, nil
	})
	client.writeCache = func(string, persistedCache) error { return errors.New("disk full") }

	snapshot, err := client.Load(t.Context(), false)
	require.NoError(t, err)
	assert.Equal(t, StateStale, snapshot.State)
	assert.Equal(t, fetchedAt, snapshot.FetchedAt)
}

func TestLoadPreservesUnknownAndKnownZeroMetadata(t *testing.T) {
	response := validCatalogResponse()
	models := response["data"].([]map[string]any)
	models[0]["context_length"] = nil
	models[0]["pricing"] = map[string]any{"prompt": nil, "completion": "0"}
	models[0]["expiration_date"] = "2026-09-30"

	parsed, err := parseResponse(mustJSON(t, response))
	require.NoError(t, err)
	require.Len(t, parsed, 1)
	assert.Nil(t, parsed[0].ContextLength)
	assert.Nil(t, parsed[0].PromptUSDPerToken)
	require.NotNil(t, parsed[0].CompletionUSDPerToken)
	assert.Zero(t, *parsed[0].CompletionUSDPerToken)
	assert.Equal(t, StatusScheduledForRemoval, parsed[0].Status)
	assert.Equal(t, "2026-09-30", parsed[0].ExpirationDate)
}

func TestLoadFailsClosedWithoutUsableCache(t *testing.T) {
	t.Run("offline and missing", func(t *testing.T) {
		client := New("", filepath.Join(t.TempDir(), "missing.json"))
		_, err := client.Load(t.Context(), true)
		assert.ErrorIs(t, err, ErrCacheUnavailable)
	})

	t.Run("malformed response", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{broken`))
		}))
		t.Cleanup(server.Close)
		client := New("", filepath.Join(t.TempDir(), "missing.json"))
		client.endpoint = server.URL
		client.httpClient = server.Client()
		_, err := client.Load(t.Context(), false)
		assert.Error(t, err)
	})

	t.Run("not modified response", func(t *testing.T) {
		client := New("", filepath.Join(t.TempDir(), "missing.json"))
		client.httpClient = doerFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotModified,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Request:    request,
			}, nil
		})
		_, err := client.Load(t.Context(), false)
		assert.ErrorContains(t, err, "without a usable cache")
	})
}

func TestLoadRejectsUnsafeEndpointAndChangedRedirectOrigin(t *testing.T) {
	t.Run("plain HTTP endpoint", func(t *testing.T) {
		client := New("", filepath.Join(t.TempDir(), "missing.json"))
		client.endpoint = "http://openrouter.ai/api/v1/models"
		client.httpClient = doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("unsafe endpoint must be rejected before request")
			return nil, nil
		})
		_, err := client.Load(t.Context(), false)
		assert.ErrorContains(t, err, "HTTPS URL")
	})

	t.Run("redirect changed origin", func(t *testing.T) {
		client := New("", filepath.Join(t.TempDir(), "missing.json"))
		client.httpClient = doerFunc(func(request *http.Request) (*http.Response, error) {
			redirected := request.Clone(request.Context())
			redirected.URL, _ = request.URL.Parse("https://example.com/models")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(mustJSON(t, validCatalogResponse()))),
				Request:    redirected,
			}, nil
		})
		_, err := client.Load(t.Context(), false)
		assert.ErrorContains(t, err, "changed origin")
	})
}

func TestParseResponseRejectsMalformedOrPartialModels(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{name: "missing identity", mutate: func(_ map[string]any, model map[string]any) { model["id"] = "" }},
		{name: "invalid context", mutate: func(_ map[string]any, model map[string]any) { model["context_length"] = -1 }},
		{name: "invalid price", mutate: func(_ map[string]any, model map[string]any) {
			model["pricing"] = map[string]any{"prompt": "NaN", "completion": "0"}
		}},
		{name: "invalid expiration", mutate: func(_ map[string]any, model map[string]any) { model["expiration_date"] = "someday" }},
		{name: "partial", mutate: func(response map[string]any, _ map[string]any) { response["total_count"] = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := validCatalogResponse()
			model := response["data"].([]map[string]any)[0]
			test.mutate(response, model)
			_, err := parseResponse(mustJSON(t, response))
			assert.Error(t, err)
		})
	}
}

func TestCacheRejectsSymlinksAndUsesPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache", "openrouter-models.json")
	require.NoError(t, writeCacheFile(cachePath, testCache(time.Now().UTC())))
	dirInfo, err := os.Stat(filepath.Dir(cachePath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(cachePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())

	target := filepath.Join(dir, "target.json")
	require.NoError(t, os.WriteFile(target, []byte("do not replace"), 0600))
	symlink := filepath.Join(dir, "symlink.json")
	require.NoError(t, os.Symlink(target, symlink))
	assert.Error(t, writeCacheFile(symlink, testCache(time.Now().UTC())))
	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "do not replace", string(body))

	linkedTarget := filepath.Join(dir, "linked-target")
	require.NoError(t, os.Mkdir(linkedTarget, 0700))
	linkedDir := filepath.Join(dir, "linked-cache")
	require.NoError(t, os.Symlink(linkedTarget, linkedDir))
	assert.Error(t, writeCacheFile(filepath.Join(linkedDir, "models.json"), testCache(time.Now().UTC())))

	managedTarget := filepath.Join(dir, "managed-target")
	require.NoError(t, os.Mkdir(managedTarget, 0700))
	managedRoot := filepath.Join(dir, ".veto")
	require.NoError(t, os.Symlink(managedTarget, managedRoot))
	assert.Error(t, writeCacheFile(filepath.Join(managedRoot, "cache", "models.json"), testCache(time.Now().UTC())))
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func testCache(fetchedAt time.Time) persistedCache {
	contextLength := 4096
	zero := 0.0
	return persistedCache{
		Version: cacheVersion, FetchedAt: fetchedAt, ETag: `"old-etag"`,
		Models: []Model{{
			ID: "cached/model", Name: "Cached Model", ContextLength: &contextLength,
			InputModalities: []string{"text"}, OutputModalities: []string{"text"},
			SupportedParameters: []string{}, Status: StatusAvailable,
			PromptUSDPerToken: &zero, CompletionUSDPerToken: &zero,
		}},
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return body
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
