package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTripFunc lets tests inject a custom http.RoundTripper without a real server.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// --- helpers ---

func anthropicAcceptResp(text string) anthropicResponse {
	return anthropicResponse{
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: text}},
	}
}

func openAIAcceptResp(text string) openAIResponse {
	return openAIResponse{
		Choices: []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{{Message: struct {
			Content string `json:"content"`
		}{Content: text}}},
	}
}

// --- Anthropic executor tests ---

func TestAnthropicExecutor_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "sk-test", r.Header.Get("x-api-key"))
		assert.Equal(t, anthropicAPIVersion, r.Header.Get("anthropic-version"))
		assert.Equal(t, "application/json", r.Header.Get("content-type"))

		var req anthropicRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "claude-haiku", req.Model)
		assert.Equal(t, admissionMaxTokens, req.MaxTokens)
		assert.Equal(t, "user", req.Messages[0].Role)

		json.NewEncoder(w).Encode(anthropicAcceptResp("hello"))
	}))
	defer srv.Close()

	exec := &AnthropicExecutor{apiKey: "sk-test", model: "claude-haiku", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "test prompt")
	require.NoError(t, res.Error)
	assert.Equal(t, "hello", res.Output)
}

func TestAnthropicExecutor_MultipleContentBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := anthropicResponse{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
				{Type: "text", Text: "part one"},
				{Type: "other", Text: "ignored"},
				{Type: "text", Text: "part two"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	exec := &AnthropicExecutor{apiKey: "k", model: "m", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "prompt")
	require.NoError(t, res.Error)
	assert.Equal(t, "part onepart two", res.Output)
}

func TestAnthropicExecutor_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		resp := anthropicResponse{
			Error: &struct {
				Message string `json:"message"`
			}{Message: "invalid api key"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	exec := &AnthropicExecutor{apiKey: "bad", model: "m", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "prompt")
	require.Error(t, res.Error)
	assert.Contains(t, res.Error.Error(), "invalid api key")
}

func TestAnthropicExecutor_NonOKNoErrorField(t *testing.T) {
	// 500 is not in the retryable set, so this exercises the non-OK path directly
	// without incurring retry backoff.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(anthropicResponse{})
	}))
	defer srv.Close()

	exec := &AnthropicExecutor{apiKey: "k", model: "m", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "prompt")
	require.Error(t, res.Error)
	assert.Contains(t, res.Error.Error(), "500")
}

func TestAnthropicExecutor_ContextCanceled(t *testing.T) {
	exec := &AnthropicExecutor{apiKey: "k", model: "m", endpoint: "http://127.0.0.1:0", client: &http.Client{}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res := exec.Run(ctx, "prompt")
	require.Error(t, res.Error)
}

func TestNewAnthropicExecutor_UsesCorrectEndpoint(t *testing.T) {
	exec := NewAnthropicExecutor("key", "claude-haiku")
	assert.Equal(t, anthropicEndpoint, exec.endpoint)
}

// --- OpenAI executor tests ---

func TestOpenAIExecutor_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk-openai", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Empty(t, r.Header.Get("HTTP-Referer"), "openai executor must not set OpenRouter headers")

		var req openAIRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "gpt-4o", req.Model)
		assert.Equal(t, admissionMaxTokens, req.MaxTokens)

		json.NewEncoder(w).Encode(openAIAcceptResp("gpt says hi"))
	}))
	defer srv.Close()

	exec := &OpenAIExecutor{apiKey: "sk-openai", model: "gpt-4o", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "hello")
	require.NoError(t, res.Error)
	assert.Equal(t, "gpt says hi", res.Output)
}

func TestOpenAIExecutor_APIError(t *testing.T) {
	// 400 is not retryable — tests error-message extraction without retry backoff
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		resp := openAIResponse{
			Error: &struct {
				Message string `json:"message"`
			}{Message: "invalid request payload"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	exec := &OpenAIExecutor{apiKey: "k", model: "gpt-4o", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "prompt")
	require.Error(t, res.Error)
	assert.Contains(t, res.Error.Error(), "invalid request payload")
}

func TestOpenAIExecutor_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openAIResponse{Choices: nil})
	}))
	defer srv.Close()

	exec := &OpenAIExecutor{apiKey: "k", model: "m", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "prompt")
	require.Error(t, res.Error)
	assert.Contains(t, res.Error.Error(), "empty response")
}

func TestNewOpenAIExecutor_UsesOpenAIEndpoint(t *testing.T) {
	exec := NewOpenAIExecutor("key", "gpt-4o")
	assert.Equal(t, openAIEndpoint, exec.endpoint)
}

func TestNewOpenRouterExecutor_UsesOpenRouterEndpoint(t *testing.T) {
	exec := NewOpenRouterExecutor("key", "llama")
	assert.Equal(t, openRouterEndpoint, exec.endpoint)
}

// TestOpenRouterExecutor_SetsRefererHeaders verifies that the OpenRouter executor
// injects the HTTP-Referer and X-Title headers. We use a custom RoundTripper
// so the endpoint URL stays set to openRouterEndpoint (the header gate condition).
func TestOpenRouterExecutor_SetsRefererHeaders(t *testing.T) {
	var capturedReferer, capturedTitle string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedReferer = r.Header.Get("HTTP-Referer")
		capturedTitle = r.Header.Get("X-Title")
		// return a minimal valid response
		body, _ := json.Marshal(openAIAcceptResp("ok"))
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		_, _ = rec.Write(body)
		return rec.Result(), nil
	})

	exec := NewOpenRouterExecutor("sk-or-test", "llama-3")
	exec.client = &http.Client{Transport: transport}

	res := exec.Run(t.Context(), "prompt")
	require.NoError(t, res.Error)
	assert.Equal(t, "https://github.com/oleg-koval/veto", capturedReferer)
	assert.Equal(t, "veto", capturedTitle)
}

// TestOpenAIExecutor_NoRefererHeaders ensures regular OpenAI executor doesn't
// leak OpenRouter headers even when using the same struct.
func TestOpenAIExecutor_NoRefererHeaders(t *testing.T) {
	var capturedReferer string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedReferer = r.Header.Get("HTTP-Referer")
		body, _ := json.Marshal(openAIAcceptResp("ok"))
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		_, _ = rec.Write(body)
		return rec.Result(), nil
	})

	exec := NewOpenAIExecutor("sk-openai", "gpt-4o")
	exec.client = &http.Client{Transport: transport}

	res := exec.Run(t.Context(), "prompt")
	require.NoError(t, res.Error)
	assert.Empty(t, capturedReferer)
}

// TestOpenAIExecutor_BadJSON verifies decoding errors surface correctly.
func TestOpenAIExecutor_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json at all")
	}))
	defer srv.Close()

	exec := &OpenAIExecutor{apiKey: "k", model: "m", endpoint: srv.URL, client: srv.Client()}
	res := exec.Run(t.Context(), "prompt")
	require.Error(t, res.Error)
}

// --- Retry tests ---

func TestAnthropicExecutor_RetryOn503ThenSucceeds(t *testing.T) {
	attempt := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt++
		if attempt < 2 {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusServiceUnavailable)
			rec.Write([]byte("{}"))
			return rec.Result(), nil
		}
		body, _ := json.Marshal(anthropicAcceptResp("retried"))
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		rec.Write(body)
		return rec.Result(), nil
	})

	exec := &AnthropicExecutor{apiKey: "k", model: "m", endpoint: anthropicEndpoint, client: &http.Client{Transport: transport}}
	res := exec.Run(t.Context(), "prompt")
	require.NoError(t, res.Error)
	assert.Equal(t, "retried", res.Output)
	assert.Equal(t, 2, attempt)
}

func TestAnthropicExecutor_ExhaustsRetries(t *testing.T) {
	attempt := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt++
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusServiceUnavailable)
		rec.Write([]byte("{}"))
		return rec.Result(), nil
	})

	exec := &AnthropicExecutor{apiKey: "k", model: "m", endpoint: anthropicEndpoint, client: &http.Client{Transport: transport}}
	res := exec.Run(t.Context(), "prompt")
	require.Error(t, res.Error)
	assert.Equal(t, maxRetries, attempt)
}

func TestAnthropicExecutor_RetryRespectsContextCancel(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusTooManyRequests)
		rec.Write([]byte("{}"))
		return rec.Result(), nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already cancelled

	exec := &AnthropicExecutor{apiKey: "k", model: "m", endpoint: anthropicEndpoint, client: &http.Client{Transport: transport}}
	res := exec.Run(ctx, "prompt")
	require.Error(t, res.Error)
}

func TestOpenAIExecutor_RetryOn429ThenSucceeds(t *testing.T) {
	attempt := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt++
		if attempt < 2 {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusTooManyRequests)
			rec.Write([]byte("{}"))
			return rec.Result(), nil
		}
		body, _ := json.Marshal(openAIAcceptResp("ok after retry"))
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		rec.Write(body)
		return rec.Result(), nil
	})

	exec := &OpenAIExecutor{apiKey: "k", model: "m", endpoint: openAIEndpoint, client: &http.Client{Transport: transport}}
	res := exec.Run(t.Context(), "prompt")
	require.NoError(t, res.Error)
	assert.Equal(t, "ok after retry", res.Output)
	assert.Equal(t, 2, attempt)
}

func TestRetryAfter_HonorsHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Retry-After", "5")
	resp := rec.Result()
	assert.Equal(t, 5*time.Second, retryAfter(resp, time.Second))
}

func TestRetryAfter_FallsBackOnMissingHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	resp := rec.Result()
	assert.Equal(t, time.Second, retryAfter(resp, time.Second))
}

func TestRetryableStatus(t *testing.T) {
	for _, code := range []int{429, 502, 503, 504} {
		assert.True(t, retryableStatus(code), "should be retryable: %d", code)
	}
	for _, code := range []int{200, 400, 401, 403, 404, 500} {
		assert.False(t, retryableStatus(code), "should not be retryable: %d", code)
	}
}

// --- Benchmarks ---

// BenchmarkAnthropicExecutor_Marshal measures the JSON marshal + bytes.Reader
// setup that happens before every HTTP attempt. These are the allocations we
// optimised: one marshal, one reader (reused across retries via Seek).
func BenchmarkAnthropicExecutor_Marshal(b *testing.B) {
	exec := NewAnthropicExecutor("sk-bench", "claude-haiku")
	_ = exec // ensure constructor path is exercised
	prompt := "You are evaluating a task. Return JSON only: {\"accept\":true,\"confidence\":0.9,\"reason_codes\":[]}"
	b.ReportAllocs()
	for b.Loop() {
		// exercise only the marshal path, not the network
		_, _ = json.Marshal(anthropicRequest{
			Model:     "claude-haiku",
			MaxTokens: admissionMaxTokens,
			Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
		})
	}
}

func TestNewOpenAICompatibleExecutor_HitsCustomEndpoint(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Path
		resp := openAIResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: `{"accept":true,"confidence":0.9,"reason_codes":[]}`}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	exec := NewOpenAICompatibleExecutor("", "my-model", srv.URL+"/v1/chat/completions")
	result := exec.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Contains(t, result.Output, `"accept":true`)
	assert.Equal(t, "/v1/chat/completions", gotURL)
}

func TestNewOpenAICompatibleExecutor_EmptyAPIKey(t *testing.T) {
	// local servers typically ignore the Authorization header; ensure we don't error on empty key
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: "ok"}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	exec := NewOpenAICompatibleExecutor("", "my-model", srv.URL+"/v1/chat/completions")
	result := exec.Run(context.Background(), "prompt")
	require.NoError(t, result.Error)
	assert.Equal(t, "ok", result.Output)
}

// BenchmarkOpenAIExecutor_Marshal is the equivalent for the OpenAI executor.
func BenchmarkOpenAIExecutor_Marshal(b *testing.B) {
	prompt := "You are evaluating a task. Return JSON only: {\"accept\":true,\"confidence\":0.9,\"reason_codes\":[]}"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = json.Marshal(openAIRequest{
			Model:     "gpt-4o",
			Messages:  []openAIMessage{{Role: "user", Content: prompt}},
			MaxTokens: admissionMaxTokens,
		})
	}
}
