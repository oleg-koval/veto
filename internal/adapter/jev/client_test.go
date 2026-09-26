package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oleg-koval/veto/pkg/router"
	"github.com/stretchr/testify/require"
)

// TestDecideShadow checks request construction, candidate mapping, probabilities, and measured telemetry.
func TestDecideShadow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		var request systemOneRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "jev-test", request.Model)
		require.Len(t, request.State.Candidates, 2)
		require.Len(t, request.Questions, 3)
		writeJSON(t, w, validResponse("candidate_2"))
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, "test-key", "jev-test", server.Client())
	require.NoError(t, err)
	times := []time.Time{time.Unix(0, 0), time.Unix(0, 123*int64(time.Millisecond))}
	client.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	prediction, err := client.DecideShadow(t.Context(), decisionRequest())
	require.NoError(t, err)
	require.Equal(t, "model-b", prediction.SelectedCandidate)
	require.Equal(t, router.DecisionProbability{Known: true, Value: .73}, prediction.Probability)
	require.Equal(t, router.DecisionProbability{Known: true, Value: .91}, prediction.Confidence)
	require.Equal(t, 120, prediction.Telemetry.InputTokens)
	require.Equal(t, 4, prediction.Telemetry.OutputTokens)
	require.Equal(t, 124, prediction.Telemetry.TotalTokens)
	require.True(t, prediction.Telemetry.UsageKnown)
	require.Equal(t, int64(123), prediction.Telemetry.LatencyMs)
	require.False(t, prediction.Telemetry.CostKnown)
}

// TestDecideShadowNoSelection checks that explicit nonselection retains confidence but no success probability.
func TestDecideShadowNoSelection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := validResponse(noSelectionChoice)
		writeJSON(t, w, response)
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, "key", "", server.Client())
	require.NoError(t, err)
	prediction, err := client.DecideShadow(t.Context(), decisionRequest())
	require.NoError(t, err)
	require.Empty(t, prediction.SelectedCandidate)
	require.False(t, prediction.Probability.Known)
	require.True(t, prediction.Confidence.Known)
}

// TestDecideShadowRequiresKeyWithoutRequest checks that a missing key prevents HTTP traffic.
func TestDecideShadowRequiresKeyWithoutRequest(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP request must not be made")
		return nil, nil
	})
	client, err := New(DefaultBaseURL, "", DefaultModel, &http.Client{Transport: transport})
	require.NoError(t, err)
	_, err = client.DecideShadow(t.Context(), decisionRequest())
	require.ErrorIs(t, err, router.ErrShadowUnavailable)
}

// TestParseResponseRejectsMalformedData checks rejection of invalid usage, selections, and typed answers.
func TestParseResponseRejectsMalformedData(t *testing.T) {
	keys := map[string]string{"candidate_1": "model-a", "candidate_2": "model-b"}
	tests := map[string]func(*systemOneResponse){
		"missing model":  func(r *systemOneResponse) { r.Model = "" },
		"missing usage":  func(r *systemOneResponse) { r.Usage = nil },
		"negative usage": func(r *systemOneResponse) { r.Usage.InputTokens = intValue(-1) },
		"usage overflow": func(r *systemOneResponse) {
			r.Usage.InputTokens, r.Usage.OutputTokens = intValue(math.MaxInt), intValue(1)
		},
		"missing answer": func(r *systemOneResponse) { delete(r.Answers, "success_candidate_1") },
		"unknown choice": func(r *systemOneResponse) {
			r.Answers[selectionQuestion] = raw(choiceAnswer{Type: "choice", Choice: "other", Confidence: floatValue(.9), Probabilities: map[string]float64{"candidate_1": .3, "candidate_2": .3, "none": .4}})
		},
		"missing confidence": func(r *systemOneResponse) {
			r.Answers[selectionQuestion] = raw(choiceAnswer{Type: "choice", Choice: "candidate_1", Probabilities: map[string]float64{"candidate_1": .3, "candidate_2": .3, "none": .4}})
		},
		"bad confidence": func(r *systemOneResponse) {
			r.Answers[selectionQuestion] = raw(choiceAnswer{Type: "choice", Choice: "candidate_1", Confidence: floatValue(2), Probabilities: map[string]float64{"candidate_1": .3, "candidate_2": .3, "none": .4}})
		},
		"bad probability sum": func(r *systemOneResponse) {
			r.Answers[selectionQuestion] = raw(choiceAnswer{Type: "choice", Choice: "candidate_1", Confidence: floatValue(.9), Probabilities: map[string]float64{"candidate_1": .1, "candidate_2": .1, "none": .1}})
		},
		"missing noul": func(r *systemOneResponse) {
			r.Answers["success_candidate_1"] = raw(noulAnswer{Type: "noul"})
		},
		"wrong success type": func(r *systemOneResponse) {
			r.Answers["success_candidate_1"] = raw(map[string]any{"type": "choice", "noul": .5})
		},
		"extra field": func(r *systemOneResponse) {
			r.Answers["success_candidate_1"] = raw(map[string]any{"type": "noul", "noul": .5, "detail": "not accepted"})
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			response := validResponse("candidate_1")
			mutate(&response)
			_, err := parseResponse(response, keys)
			require.ErrorIs(t, err, router.ErrShadowMalformed)
		})
	}
}

// TestDecideShadowHTTPFailureDoesNotLeakBody checks that HTTP error bodies are absent from returned errors.
func TestDecideShadowHTTPFailureDoesNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"detail":"secret echoed state"}`)
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, "key", "", server.Client())
	require.NoError(t, err)
	_, err = client.DecideShadow(t.Context(), decisionRequest())
	require.ErrorContains(t, err, "HTTP status 401")
	require.NotContains(t, err.Error(), "secret")
}

// TestDecideShadowRejectsOversizedResponse checks the response size limit.
func TestDecideShadowRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, "key", "", server.Client())
	require.NoError(t, err)
	_, err = client.DecideShadow(t.Context(), decisionRequest())
	require.ErrorIs(t, err, router.ErrShadowMalformed)
}

// TestDecideShadowRejectsRedirect checks that redirects never reach their target.
func TestDecideShadowRejectsRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("redirect target must not be called")
	}))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, "key", "", server.Client())
	require.NoError(t, err)
	_, err = client.DecideShadow(t.Context(), decisionRequest())
	require.ErrorContains(t, err, "redirects are not allowed")
}

// TestDecideShadowHonorsCancellation checks propagation of context cancellation.
func TestDecideShadowHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, "key", "", server.Client())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.DecideShadow(ctx, decisionRequest())
	require.True(t, errors.Is(err, context.Canceled))
}

// decisionRequest builds a valid two-candidate request for adapter tests.
func decisionRequest() router.DecisionRequest {
	return router.DecisionRequest{
		Version: router.DecisionVersion,
		Task:    router.TaskSpec{ID: "task", Kind: router.KindPlan, Risk: router.RiskMedium, Complexity: router.ComplexityModerate, Objective: "design a router", RequiredTools: []string{"shell"}},
		Candidates: []router.DecisionCandidate{
			{Model: router.ModelCapabilities{Name: "model-a", Provider: "provider-a", Tier: "mid"}, Tools: router.ToolCapabilities{Tools: []string{"shell"}}},
			{Model: router.ModelCapabilities{Name: "model-b", Provider: "provider-b", Tier: "large"}},
		},
	}
}

// validResponse builds a typed response fixture for the requested choice.
func validResponse(choice string) systemOneResponse {
	return systemOneResponse{
		Model: "jev-2026-09-15", Usage: &usage{InputTokens: intValue(120), OutputTokens: intValue(4)},
		Answers: map[string]json.RawMessage{
			selectionQuestion:     raw(choiceAnswer{Type: "choice", Choice: choice, Confidence: floatValue(.91), Probabilities: map[string]float64{"candidate_1": .2, "candidate_2": .7, "none": .1}}),
			"success_candidate_1": raw(noulAnswer{Type: "noul", Noul: floatValue(.4)}),
			"success_candidate_2": raw(noulAnswer{Type: "noul", Noul: floatValue(.73)}),
		},
	}
}

// intValue returns a pointer to an integer fixture value.
func intValue(value int) *int { return &value }

// floatValue returns a pointer to a floating-point fixture value.
func floatValue(value float64) *float64 { return &value }

// raw marshals fixture data to raw JSON, panicking on invalid test input.
func raw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

// writeJSON writes a JSON response and fails the test on encoding errors.
func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(value))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates HTTP transport behavior to the test function.
func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
