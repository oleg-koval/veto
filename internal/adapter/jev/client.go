// Package jev adapts TypeSafe's System One API to Veto's evidence-only shadow
// decision port. It never participates in authoritative routing.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
)

const (
	DefaultBaseURL    = "https://api.typesafe.ai"
	DefaultModel      = "jev-latest"
	maxResponseBytes  = 1024 * 1024
	maxRequestBytes   = 256 * 1024
	selectionQuestion = "selection"
	noSelectionChoice = "none"
)

// Client is safe for concurrent use after construction.
type Client struct {
	endpoint *url.URL
	apiKey   string
	model    string
	http     *http.Client
	now      func() time.Time
}

// New constructs a Jev shadow client. The supplied HTTP client is copied and
// redirects are always rejected so bearer credentials never cross endpoints.
func New(baseURL, apiKey, model string, client *http.Client) (*Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("jev: invalid base URL")
	}
	endpoint := base.ResolveReference(&url.URL{Path: "/v1/systemone"})
	if model == "" {
		model = DefaultModel
	}
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("jev: redirects are not allowed")
	}
	return &Client{endpoint: endpoint, apiKey: apiKey, model: model, http: &cloned, now: time.Now}, nil
}

type systemOneRequest struct {
	State     requestState        `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]question `json:"questions"`
}

type requestState struct {
	Objective       string            `json:"objective"`
	Kind            router.TaskKind   `json:"kind"`
	Risk            router.Risk       `json:"risk"`
	Complexity      router.Complexity `json:"complexity"`
	RequiredTools   []string          `json:"required_tools,omitempty"`
	SuccessCriteria []string          `json:"success_criteria,omitempty"`
	Candidates      []stateCandidate  `json:"candidates"`
}

type stateCandidate struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Provider string   `json:"provider,omitempty"`
	Runtime  string   `json:"runtime,omitempty"`
	Tier     string   `json:"tier,omitempty"`
	Tools    []string `json:"tools,omitempty"`
}

type question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   *usage                     `json:"usage"`
}

type usage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

type choiceAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

type noulAnswer struct {
	Type string   `json:"type"`
	Noul *float64 `json:"noul"`
}

// DecideShadow performs one typed System One call for the whole shortlist.
func (c *Client) DecideShadow(ctx context.Context, request router.DecisionRequest) (router.ShadowPrediction, error) {
	if c == nil || strings.TrimSpace(c.apiKey) == "" {
		return router.ShadowPrediction{}, router.ErrShadowUnavailable
	}
	payload, keys, err := c.buildRequest(request)
	if err != nil {
		return router.ShadowPrediction{}, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return router.ShadowPrediction{}, fmt.Errorf("jev: encode request: %w", err)
	}
	if len(encoded) > maxRequestBytes {
		return router.ShadowPrediction{}, fmt.Errorf("jev: request exceeds %d bytes", maxRequestBytes)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return router.ShadowPrediction{}, fmt.Errorf("jev: create request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	started := c.now()
	response, err := c.http.Do(httpRequest)
	latency := c.now().Sub(started)
	if err != nil {
		return router.ShadowPrediction{}, fmt.Errorf("jev: request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return router.ShadowPrediction{}, fmt.Errorf("jev: HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return router.ShadowPrediction{}, fmt.Errorf("jev: read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return router.ShadowPrediction{}, fmt.Errorf("%w: response exceeds %d bytes", router.ErrShadowMalformed, maxResponseBytes)
	}
	var decoded systemOneResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&decoded); err != nil {
		return router.ShadowPrediction{}, fmt.Errorf("%w: invalid JSON", router.ErrShadowMalformed)
	}
	if err := ensureEOF(decoder); err != nil {
		return router.ShadowPrediction{}, err
	}
	prediction, err := parseResponse(decoded, keys)
	if err != nil {
		return router.ShadowPrediction{}, err
	}
	prediction.Telemetry = router.DecisionTelemetry{
		InputTokens: *decoded.Usage.InputTokens, OutputTokens: *decoded.Usage.OutputTokens,
		TotalTokens: *decoded.Usage.InputTokens + *decoded.Usage.OutputTokens, UsageKnown: true,
		LatencyMs: latency.Milliseconds(), LatencyKnown: true,
	}
	return prediction, nil
}

// buildRequest validates the shortlist and builds selection and success questions with candidate key mappings.
func (c *Client) buildRequest(request router.DecisionRequest) (systemOneRequest, map[string]string, error) {
	if err := request.Validate(); err != nil {
		return systemOneRequest{}, nil, err
	}
	objective := request.Task.AdmissionObjective
	if objective == "" {
		objective = request.Task.Objective
	}
	state := requestState{
		Objective: objective, Kind: request.Task.Kind, Risk: request.Task.Risk, Complexity: request.Task.Complexity,
		RequiredTools: append([]string(nil), request.Task.RequiredTools...), SuccessCriteria: append([]string(nil), request.Task.SuccessCriteria...),
	}
	questions := make(map[string]question, len(request.Candidates)+1)
	criteria := make(map[string]any, len(request.Candidates)+1)
	keys := make(map[string]string, len(request.Candidates))
	for index, candidate := range request.Candidates {
		key := fmt.Sprintf("candidate_%d", index+1)
		keys[key] = candidate.Model.Name
		state.Candidates = append(state.Candidates, stateCandidate{
			Key: key, Name: candidate.Model.Name, Provider: candidate.Model.Provider, Runtime: candidate.Model.Runtime,
			Tier: candidate.Model.Tier, Tools: append([]string(nil), candidate.Tools.Tools...),
		})
		criteria[key] = map[string]any{"candidate_key": key, "choose_when": "best likely successful eligible candidate"}
		questions["success_"+key] = question{
			Type: "noul", Instructions: map[string]any{"task": "Will this candidate successfully complete the task?", "candidate_key": key},
			Criteria: map[string]any{"true": "successful completion", "false": "failure or unsuitable"},
		}
	}
	criteria[noSelectionChoice] = "No offered candidate is likely to complete the task successfully."
	questions[selectionQuestion] = question{Type: "choice", Instructions: "Select the best eligible candidate, or none.", Criteria: criteria}
	return systemOneRequest{State: state, Model: c.model, Questions: questions}, keys, nil
}

// parseResponse validates typed answers and usage, then maps the selected key to a model prediction.
func parseResponse(response systemOneResponse, keys map[string]string) (router.ShadowPrediction, error) {
	if strings.TrimSpace(response.Model) == "" || response.Usage == nil || response.Usage.InputTokens == nil || response.Usage.OutputTokens == nil ||
		*response.Usage.InputTokens < 0 || *response.Usage.OutputTokens < 0 || *response.Usage.OutputTokens > math.MaxInt-*response.Usage.InputTokens {
		return router.ShadowPrediction{}, malformed("missing model or invalid usage")
	}
	if len(response.Answers) != len(keys)+1 {
		return router.ShadowPrediction{}, malformed("answer count mismatch")
	}
	selectionRaw, ok := response.Answers[selectionQuestion]
	if !ok {
		return router.ShadowPrediction{}, malformed("missing selection answer")
	}
	var selection choiceAnswer
	if err := decodeExact(selectionRaw, &selection); err != nil || selection.Type != "choice" {
		return router.ShadowPrediction{}, malformed("invalid selection answer")
	}
	if selection.Confidence == nil || !unit(*selection.Confidence) {
		return router.ShadowPrediction{}, malformed("invalid selection confidence")
	}
	expectedChoices := make(map[string]bool, len(keys)+1)
	expectedChoices[noSelectionChoice] = true
	for key := range keys {
		expectedChoices[key] = true
	}
	if !expectedChoices[selection.Choice] || len(selection.Probabilities) != len(expectedChoices) {
		return router.ShadowPrediction{}, malformed("invalid selected choice")
	}
	var sum float64
	for key := range expectedChoices {
		value, exists := selection.Probabilities[key]
		if !exists || !unit(value) {
			return router.ShadowPrediction{}, malformed("invalid choice probabilities")
		}
		sum += value
	}
	if sum < .95 || sum > 1.05 {
		return router.ShadowPrediction{}, malformed("choice probabilities do not sum to one")
	}

	success := make(map[string]float64, len(keys))
	for key := range keys {
		raw, exists := response.Answers["success_"+key]
		if !exists {
			return router.ShadowPrediction{}, malformed("missing candidate success answer")
		}
		var answer noulAnswer
		if err := decodeExact(raw, &answer); err != nil || answer.Type != "noul" || answer.Noul == nil || !unit(*answer.Noul) {
			return router.ShadowPrediction{}, malformed("invalid candidate success answer")
		}
		success[key] = *answer.Noul
	}

	prediction := router.ShadowPrediction{Confidence: router.DecisionProbability{Known: true, Value: *selection.Confidence}}
	if selection.Choice != noSelectionChoice {
		prediction.SelectedCandidate = keys[selection.Choice]
		prediction.Probability = router.DecisionProbability{Known: true, Value: success[selection.Choice]}
	}
	return prediction, nil
}

// decodeExact decodes one JSON value, rejecting unknown fields and trailing values.
func decodeExact(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	return ensureEOF(decoder)
}

// ensureEOF rejects any content after the decoded JSON value except whitespace.
func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return malformed("multiple JSON values")
	}
	return nil
}

// unit reports whether a value is finite and within the inclusive unit interval.
func unit(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

// malformed wraps a validation detail with the malformed shadow response sentinel.
func malformed(detail string) error {
	return fmt.Errorf("%w: %s", router.ErrShadowMalformed, detail)
}

var _ router.ShadowDecider = (*Client)(nil)
