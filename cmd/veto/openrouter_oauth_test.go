package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeOpenRouterPKCESucceeds(t *testing.T) {
	exchange := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		var payload map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, "test-code", payload["code"])
		assert.Equal(t, "S256", payload["code_challenge_method"])
		assert.NotEmpty(t, payload["code_verifier"])
		_ = json.NewEncoder(w).Encode(map[string]string{"key": "sk-or-oauth-test"})
	}))
	t.Cleanup(exchange.Close)

	deps := testOpenRouterOAuthDeps(exchange)
	deps.openBrowser = func(rawURL string) {
		auth, err := url.Parse(rawURL)
		require.NoError(t, err)
		assert.Equal(t, "S256", auth.Query().Get("code_challenge_method"))
		assert.NotEmpty(t, auth.Query().Get("code_challenge"))
		callback := auth.Query().Get("callback_url")
		go func() {
			response, requestErr := http.Get(callback + "?code=test-code")
			if requestErr == nil {
				_ = response.Body.Close()
			}
		}()
	}

	key, err := authorizeOpenRouter(t.Context(), deps)
	require.NoError(t, err)
	assert.Equal(t, "sk-or-oauth-test", key)
}

func TestAuthorizeOpenRouterRejectsSpoofedCallbackPath(t *testing.T) {
	exchange := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"key": "sk-or-test"})
	}))
	t.Cleanup(exchange.Close)
	deps := testOpenRouterOAuthDeps(exchange)
	status := make(chan int, 1)
	deps.openBrowser = func(rawURL string) {
		auth, _ := url.Parse(rawURL)
		callback, _ := url.Parse(auth.Query().Get("callback_url"))
		go func() {
			spoof := *callback
			spoof.Path = "/callback/wrong-state"
			query := spoof.Query()
			query.Set("code", "spoofed")
			spoof.RawQuery = query.Encode()
			response, _ := http.Get(spoof.String())
			status <- response.StatusCode
			_ = response.Body.Close()

			query = callback.Query()
			query.Set("code", "valid")
			callback.RawQuery = query.Encode()
			response, _ = http.Get(callback.String())
			_ = response.Body.Close()
		}()
	}

	_, err := authorizeOpenRouter(t.Context(), deps)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, <-status)
}

func TestAuthorizeOpenRouterTimeoutCancellationAndPortFailure(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		deps := testOpenRouterOAuthDeps(nil)
		deps.wait = 10 * time.Millisecond
		_, err := authorizeOpenRouter(t.Context(), deps)
		assert.ErrorContains(t, err, "timed out")
	})
	t.Run("cancelled", func(t *testing.T) {
		deps := testOpenRouterOAuthDeps(nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := authorizeOpenRouter(ctx, deps)
		assert.ErrorIs(t, err, context.Canceled)
	})
	t.Run("port conflict", func(t *testing.T) {
		deps := testOpenRouterOAuthDeps(nil)
		deps.listen = func(string, string) (net.Listener, error) { return nil, errors.New("address in use") }
		_, err := authorizeOpenRouter(t.Context(), deps)
		assert.ErrorContains(t, err, "address in use")
	})
}

func TestExchangeOpenRouterCodeFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "http error", status: http.StatusForbidden, body: `{}`},
		{name: "malformed", status: http.StatusOK, body: `{broken`},
		{name: "missing key", status: http.StatusOK, body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := testOpenRouterOAuthDeps(nil)
			deps.httpDo = func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			}
			_, err := exchangeOpenRouterCode(t.Context(), deps, "code", "verifier")
			assert.Error(t, err)
		})
	}
}

func testOpenRouterOAuthDeps(exchange *httptest.Server) openRouterOAuthDeps {
	deps := defaultOpenRouterOAuthDeps()
	deps.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	deps.openBrowser = func(string) {}
	deps.wait = time.Second
	deps.authURL = "https://openrouter.ai/auth"
	if exchange != nil {
		deps.exchangeURL = exchange.URL
		deps.httpDo = exchange.Client().Do
	}
	return deps
}
