package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
)

const maxModelListResponseBytes = 32 << 20

type modelListProvider struct {
	name      string
	endpoint  string
	envKey    string
	headerKey string
}

var modelListProviders = map[string]modelListProvider{
	"anthropic": {
		name:      "anthropic",
		endpoint:  "https://api.anthropic.com/v1/models",
		envKey:    "ANTHROPIC_API_KEY",
		headerKey: "x-api-key",
	},
	"openai": {
		name:      "openai",
		endpoint:  "https://api.openai.com/v1/models",
		envKey:    "OPENAI_API_KEY",
		headerKey: "authorization",
	},
	"openrouter": {
		name:      "openrouter",
		endpoint:  "https://openrouter.ai/api/v1/models",
		envKey:    "OPENROUTER_API_KEY",
		headerKey: "authorization",
	},
	"xai": {
		name:      "xai",
		endpoint:  "https://api.x.ai/v1/models",
		envKey:    "XAI_API_KEY",
		headerKey: "authorization",
	},
}

type modelVerification struct {
	Provider         string   `json:"provider"`
	Endpoint         string   `json:"endpoint"`
	ConfiguredModels []string `json:"configured_models"`
	AvailableModels  []string `json:"available_models"`
	MissingModels    []string `json:"missing_models"`
	Artifact         string   `json:"artifact"`
	HTTPStatus       int      `json:"http_status"`
	ResponseBytes    int      `json:"response_bytes"`
	VerifiedAt       string   `json:"verified_at"`
}

type modelListEnvelope struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type modelVerificationMetadata struct {
	Provider      string `json:"provider"`
	URL           string `json:"url"`
	FetchedAt     string `json:"fetched_at"`
	HTTPStatus    int    `json:"http_status"`
	ResponseBytes int    `json:"response_bytes"`
}

// cmdVerifyModels verifies the configured catalog against one provider's
// account-level model list. It intentionally performs one request per run so
// operators can audit the exact response and retry a single provider safely.
func cmdVerifyModels(args []string) {
	fs := flag.NewFlagSet("verify-models", flag.ExitOnError)
	provider := fs.String("provider", "openai", "provider to verify: anthropic|openai|openrouter|xai")
	endpoint := fs.String("endpoint", "", "override the provider model-list URL (for testing or a compatible gateway)")
	artifactDir := fs.String("artifacts-dir", "artifacts/http", "directory for the raw response and request metadata")
	timeout := fs.Duration("timeout", 20*time.Second, "HTTP request timeout")
	jsonOut := fs.Bool("json", false, "emit one JSON result line")
	_ = fs.Parse(args)

	p, ok := modelListProviders[strings.ToLower(strings.TrimSpace(*provider))]
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unsupported provider %q (choose anthropic, openai, openrouter, or xai)\n", *provider)
		os.Exit(1)
	}

	creds, err := loadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading credentials: %v\n", err)
		os.Exit(1)
	}
	key := getKey(p.envKey, creds)
	if key == "" {
		fmt.Fprintf(os.Stderr, "error: %s is not configured; set it or run 'veto login'\n", p.envKey)
		os.Exit(1)
	}

	requestURL := p.endpoint
	if strings.TrimSpace(*endpoint) != "" {
		requestURL = strings.TrimSpace(*endpoint)
	}
	result, err := verifyProviderModels(context.Background(), p, key, requestURL, *artifactDir, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error verifying %s models: %v\n", p.name, err)
		os.Exit(1)
	}
	if *jsonOut {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "error encoding verification result: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("  %s: %d catalog model(s), %d available\n", result.Provider, len(result.ConfiguredModels), len(result.ConfiguredModels)-len(result.MissingModels))
		if len(result.MissingModels) > 0 {
			fmt.Printf("  Missing: %s\n", strings.Join(result.MissingModels, ", "))
		} else {
			fmt.Println("  All catalog model IDs are available to this account.")
		}
		fmt.Printf("  Raw response: %s\n", result.Artifact)
	}
	if len(result.MissingModels) > 0 {
		os.Exit(1)
	}
}

func verifyProviderModels(ctx context.Context, provider modelListProvider, key, endpoint, artifactDir string, timeout time.Duration) (modelVerification, error) {
	var result modelVerification
	if err := validateVerificationURL(endpoint); err != nil {
		return result, err
	}
	if timeout <= 0 {
		return result, errors.New("timeout must be positive")
	}
	if artifactDir == "" {
		return result, errors.New("artifacts directory must not be empty")
	}

	configured := configuredProviderModelIDs(provider.name)
	sort.Strings(configured)
	result = modelVerification{
		Provider:         provider.name,
		Endpoint:         endpoint,
		ConfiguredModels: configured,
		VerifiedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, err
	}
	if provider.headerKey == "authorization" {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set(provider.headerKey, key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxModelListResponseBytes+1))
	if readErr != nil {
		return result, readErr
	}
	if len(body) > maxModelListResponseBytes {
		return result, fmt.Errorf("model-list response exceeds %d bytes", maxModelListResponseBytes)
	}
	result.HTTPStatus = resp.StatusCode
	result.ResponseBytes = len(body)
	artifact, err := persistModelListResponse(artifactDir, provider.name, endpoint, resp.StatusCode, body)
	if err != nil {
		return result, err
	}
	result.Artifact = artifact
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("provider returned HTTP %d (response saved to %s)", resp.StatusCode, artifact)
	}

	var envelope modelListEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return result, fmt.Errorf("malformed model-list response (saved to %s): %w", artifact, err)
	}
	available := make([]string, 0, len(envelope.Data))
	for _, model := range envelope.Data {
		if strings.TrimSpace(model.ID) != "" {
			available = append(available, model.ID)
		}
	}
	sort.Strings(available)
	result.AvailableModels = available
	availableSet := make(map[string]struct{}, len(available))
	for _, id := range available {
		availableSet[id] = struct{}{}
	}
	for _, id := range configured {
		if _, ok := availableSet[id]; !ok {
			result.MissingModels = append(result.MissingModels, id)
		}
	}
	return result, nil
}

func configuredProviderModelIDs(provider string) []string {
	var ids []string
	for _, model := range router.NewRegistry().All() {
		if model.Provider == provider && model.APIModel != "" {
			ids = append(ids, model.APIModel)
		}
	}
	return ids
}

func validateVerificationURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("endpoint must be an https URL without credentials, query parameters, or fragments")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return errors.New("endpoint must be an https URL (http is allowed only for localhost test gateways)")
	}
	return nil
}

func persistModelListResponse(dir, provider, endpoint string, status int, body []byte) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	base := filepath.Join(dir, provider+"-models-"+stamp)
	bodyPath := base + ".json"
	metaPath := base + ".meta.json"
	if err := os.WriteFile(bodyPath, body, 0600); err != nil {
		return "", err
	}
	meta := modelVerificationMetadata{
		Provider:      provider,
		URL:           endpoint,
		FetchedAt:     time.Now().UTC().Format(time.RFC3339),
		HTTPStatus:    status,
		ResponseBytes: len(body),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(metaPath, data, 0600); err != nil {
		return "", err
	}
	return bodyPath, nil
}
