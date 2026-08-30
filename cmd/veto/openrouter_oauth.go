package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	openRouterAuthURL     = "https://openrouter.ai/auth"
	openRouterExchangeURL = "https://openrouter.ai/api/v1/auth/keys"
	openRouterOAuthWait   = 2 * time.Minute
	openRouterOAuthMax    = 1 << 20
)

// Flow contract: https://openrouter.ai/docs/guides/overview/auth/oauth

type openRouterOAuthDeps struct {
	listen      func(string, string) (net.Listener, error)
	random      io.Reader
	httpDo      func(*http.Request) (*http.Response, error)
	openBrowser func(string)
	authURL     string
	exchangeURL string
	wait        time.Duration
}

func defaultOpenRouterOAuthDeps() openRouterOAuthDeps {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("OpenRouter OAuth redirects are disabled")
		},
	}
	return openRouterOAuthDeps{
		listen: net.Listen, random: rand.Reader, httpDo: client.Do, openBrowser: openBrowser,
		authURL: openRouterAuthURL, exchangeURL: openRouterExchangeURL, wait: openRouterOAuthWait,
	}
}

func authorizeOpenRouter(ctx context.Context, deps openRouterOAuthDeps) (string, error) {
	listener, err := deps.listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start OpenRouter callback: %w", err)
	}
	defer listener.Close()

	verifier, err := oauthRandomToken(deps.random)
	if err != nil {
		return "", err
	}
	state, err := oauthRandomToken(deps.random)
	if err != nil {
		return "", err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	callbackPath := "/callback/" + state
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])

	auth, err := url.Parse(deps.authURL)
	if err != nil || auth.Scheme != "https" || auth.Host == "" {
		return "", errors.New("OpenRouter authorization endpoint must use HTTPS")
	}
	query := auth.Query()
	query.Set("callback_url", callbackURL)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	auth.RawQuery = query.Encode()

	type callbackResult struct {
		code string
		err  error
	}
	result := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query := r.URL.Query()
		if providerError := strings.TrimSpace(query.Get("error")); providerError != "" {
			select {
			case result <- callbackResult{err: errors.New("OpenRouter authorization was declined")}:
			default:
			}
			http.Error(w, "authorization declined", http.StatusBadRequest)
			return
		}
		codes := query["code"]
		if len(codes) != 1 {
			http.Error(w, "invalid authorization response", http.StatusBadRequest)
			return
		}
		code := strings.TrimSpace(codes[0])
		if code == "" || len(code) > 4096 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
			http.Error(w, "invalid authorization response", http.StatusBadRequest)
			return
		}
		select {
		case result <- callbackResult{code: code}:
			_, _ = io.WriteString(w, "OpenRouter is connected. You can close this tab.")
		default:
			http.Error(w, "authorization already completed", http.StatusConflict)
		}
	})
	server := &http.Server{
		Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		IdleTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	deps.openBrowser(auth.String())
	wait := deps.wait
	if wait <= 0 {
		wait = openRouterOAuthWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var code string
	select {
	case response := <-result:
		if response.err != nil {
			return "", response.err
		}
		code = response.code
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", errors.New("OpenRouter authorization timed out")
	case err := <-serveDone:
		return "", fmt.Errorf("OpenRouter callback stopped: %w", err)
	}
	return exchangeOpenRouterCode(ctx, deps, code, verifier)
}

func oauthRandomToken(random io.Reader) (string, error) {
	buffer := make([]byte, 32)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", errors.New("generate OpenRouter OAuth secret")
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func exchangeOpenRouterCode(ctx context.Context, deps openRouterOAuthDeps, code, verifier string) (string, error) {
	endpoint, err := url.Parse(deps.exchangeURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return "", errors.New("OpenRouter token endpoint must use HTTPS")
	}
	body, err := json.Marshal(map[string]string{
		"code": code, "code_verifier": verifier, "code_challenge_method": "S256",
	})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := deps.httpDo(request)
	if err != nil {
		return "", fmt.Errorf("exchange OpenRouter authorization: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenRouter authorization exchange returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, openRouterOAuthMax+1))
	if err != nil || len(data) > openRouterOAuthMax {
		return "", errors.New("OpenRouter authorization response is unreadable")
	}
	var payload struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return "", errors.New("OpenRouter authorization response is malformed")
	}
	payload.Key = strings.TrimSpace(payload.Key)
	if payload.Key == "" || len(payload.Key) > 512 || strings.IndexFunc(payload.Key, unicode.IsControl) >= 0 {
		return "", errors.New("OpenRouter authorization response did not contain a valid key")
	}
	return payload.Key, nil
}
