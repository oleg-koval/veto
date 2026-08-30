package openroutercatalog

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultEndpoint         = "https://openrouter.ai/api/v1/models"
	defaultTimeout          = 10 * time.Second
	defaultMaxAge           = 5 * time.Minute
	defaultMaxResponseBytes = 16 << 20
	maxCatalogModels        = 1000
)

var ErrCacheUnavailable = errors.New("openrouter catalog cache unavailable")

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	apiKey           string
	cachePath        string
	endpoint         string
	httpClient       httpDoer
	now              func() time.Time
	timeout          time.Duration
	maxAge           time.Duration
	maxResponseBytes int64
	maxCacheBytes    int64
	writeCache       func(string, persistedCache) error
}

// New returns a bounded OpenRouter catalog client. The API key is used only
// for requests and is never written to the catalog cache.
func New(apiKey, cachePath string) *Client {
	return &Client{
		apiKey: apiKey, cachePath: cachePath, endpoint: defaultEndpoint,
		httpClient: newHTTPClient(), now: time.Now, timeout: defaultTimeout,
		maxAge: defaultMaxAge, maxResponseBytes: defaultMaxResponseBytes,
		maxCacheBytes: defaultMaxResponseBytes,
		writeCache:    writeCacheFile,
	}
}

// Load returns a validated catalog. Offline mode never performs a request.
// When refresh fails, a valid stale cache remains usable and is returned.
func (c *Client) Load(ctx context.Context, offline bool) (Snapshot, error) {
	now := c.now().UTC()
	cached, cacheErr := readCacheFile(c.cachePath, c.maxCacheBytes)
	if cacheErr == nil {
		snapshot := snapshotFromCache(cached, now, c.maxAge, offline)
		if offline || snapshot.State == StateFresh {
			return snapshot, nil
		}
	} else if offline {
		return Snapshot{Offline: true}, fmt.Errorf("%w: %v", ErrCacheUnavailable, cacheErr)
	}

	etag := ""
	if cacheErr == nil {
		etag = cached.ETag
	}
	models, responseETag, notModified, err := c.fetch(ctx, etag)
	if err != nil {
		if cacheErr == nil {
			return snapshotFromCache(cached, now, c.maxAge, false), nil
		}
		return Snapshot{}, err
	}
	if notModified {
		if cacheErr != nil {
			return Snapshot{}, errors.New("openrouter catalog returned not-modified without a usable cache")
		}
		refreshed := cached
		refreshed.FetchedAt = now
		if responseETag != "" {
			refreshed.ETag = responseETag
		}
		if err := c.writeCache(c.cachePath, refreshed); err != nil {
			return snapshotFromCache(cached, now, c.maxAge, false), nil
		}
		return snapshotFromCache(refreshed, now, c.maxAge, false), nil
	}

	updated := persistedCache{Version: cacheVersion, FetchedAt: now, ETag: responseETag, Models: models}
	if err := c.writeCache(c.cachePath, updated); err != nil {
		if cacheErr == nil {
			return snapshotFromCache(cached, now, c.maxAge, false), nil
		}
		return Snapshot{}, fmt.Errorf("persist openrouter catalog: %w", err)
	}
	return snapshotFromCache(updated, now, c.maxAge, false), nil
}

func (c *Client) fetch(ctx context.Context, etag string) ([]Model, string, bool, error) {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, "", false, errors.New("openrouter catalog endpoint must be an HTTPS URL without userinfo or fragments")
	}
	query := endpoint.Query()
	query.Set("limit", strconv.Itoa(maxCatalogModels))
	query.Set("output_modalities", "all")
	endpoint.RawQuery = query.Encode()

	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", false, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "veto-openrouter-catalog")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, "", false, fmt.Errorf("fetch openrouter catalog: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil && !sameOrigin(request.URL, response.Request.URL) {
		return nil, "", false, errors.New("openrouter catalog redirect changed origin")
	}
	responseETag := strings.TrimSpace(response.Header.Get("ETag"))
	if len(responseETag) > 512 {
		return nil, "", false, errors.New("openrouter catalog ETag is too large")
	}
	if response.StatusCode == http.StatusNotModified {
		return nil, responseETag, true, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("openrouter catalog returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > c.maxResponseBytes {
		return nil, "", false, errors.New("openrouter catalog response is too large")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, "", false, fmt.Errorf("read openrouter catalog: %w", err)
	}
	if int64(len(body)) > c.maxResponseBytes {
		return nil, "", false, errors.New("openrouter catalog response is too large")
	}
	models, err := parseResponse(body)
	if err != nil {
		return nil, "", false, err
	}
	return models, responseETag, false, nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// Raw field names follow OpenRouter's documented Models API contract:
// https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties
type rawResponse struct {
	Data       []rawModel `json:"data"`
	TotalCount *int       `json:"total_count"`
}

type rawModel struct {
	ID                  string           `json:"id"`
	Name                string           `json:"name"`
	ContextLength       *int             `json:"context_length"`
	Architecture        *rawArchitecture `json:"architecture"`
	SupportedParameters []string         `json:"supported_parameters"`
	Pricing             *rawPricing      `json:"pricing"`
	ExpirationDate      *string          `json:"expiration_date"`
}

type rawArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type rawPricing struct {
	Prompt     *string `json:"prompt"`
	Completion *string `json:"completion"`
}

func parseResponse(body []byte) ([]Model, error) {
	var response rawResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, errors.New("openrouter catalog response is malformed")
	}
	if len(response.Data) == 0 || len(response.Data) > maxCatalogModels {
		return nil, errors.New("openrouter catalog response has an invalid model count")
	}
	if response.TotalCount != nil && *response.TotalCount != len(response.Data) {
		return nil, errors.New("openrouter catalog response is partial")
	}

	models := make([]Model, 0, len(response.Data))
	seen := make(map[string]bool, len(response.Data))
	for _, raw := range response.Data {
		model, err := validateModel(raw)
		if err != nil {
			return nil, err
		}
		if seen[model.ID] {
			return nil, errors.New("openrouter catalog response contains duplicate model IDs")
		}
		seen[model.ID] = true
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func validateModel(raw rawModel) (Model, error) {
	raw.ID = strings.TrimSpace(raw.ID)
	raw.Name = strings.TrimSpace(raw.Name)
	if raw.ID == "" || raw.Name == "" || len(raw.ID) > 512 || len(raw.Name) > 512 {
		return Model{}, errors.New("openrouter catalog model identity is missing or too large")
	}
	if hasControl(raw.ID) || hasControl(raw.Name) {
		return Model{}, errors.New("openrouter catalog model identity contains control characters")
	}
	if raw.ContextLength != nil && (*raw.ContextLength <= 0 || *raw.ContextLength > 100_000_000) {
		return Model{}, errors.New("openrouter catalog context length is invalid")
	}
	prompt, err := parsePrice(raw.Pricing, true)
	if err != nil {
		return Model{}, err
	}
	completion, err := parsePrice(raw.Pricing, false)
	if err != nil {
		return Model{}, err
	}

	model := Model{
		ID: raw.ID, Name: raw.Name, ContextLength: raw.ContextLength,
		Status: StatusAvailable, PromptUSDPerToken: prompt, CompletionUSDPerToken: completion,
	}
	if raw.Architecture != nil {
		model.InputModalities, err = normalizeStrings(raw.Architecture.InputModalities)
		if err != nil {
			return Model{}, err
		}
		model.OutputModalities, err = normalizeStrings(raw.Architecture.OutputModalities)
		if err != nil {
			return Model{}, err
		}
	}
	model.SupportedParameters, err = normalizeStrings(raw.SupportedParameters)
	if err != nil {
		return Model{}, err
	}
	if raw.ExpirationDate != nil && strings.TrimSpace(*raw.ExpirationDate) != "" {
		model.ExpirationDate = strings.TrimSpace(*raw.ExpirationDate)
		if !validExpirationDate(model.ExpirationDate) {
			return Model{}, errors.New("openrouter catalog expiration date is invalid")
		}
		model.Status = StatusScheduledForRemoval
	}
	return model, nil
}

func parsePrice(pricing *rawPricing, prompt bool) (*float64, error) {
	if pricing == nil {
		return nil, nil
	}
	raw := pricing.Completion
	if prompt {
		raw = pricing.Prompt
	}
	if raw == nil {
		return nil, nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(*raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil, errors.New("openrouter catalog price is invalid")
	}
	return &value, nil
}

func normalizeStrings(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) > 256 {
		return nil, errors.New("openrouter catalog capability list is too large")
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 128 || hasControl(value) {
			return nil, errors.New("openrouter catalog capability value is invalid")
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result, nil
}

func validExpirationDate(value string) bool {
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func newHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		transport = transport.Clone()
	} else {
		transport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	return &http.Client{
		Transport: transport,
		Timeout:   defaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("openrouter catalog redirects are disabled")
		},
	}
}
