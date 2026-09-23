package router

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func decisionRequestForTest(names ...string) DecisionRequest {
	r := DecisionRequest{Version: DecisionVersion, Task: TaskSpec{Objective: "test decision"}}
	for _, name := range names {
		r.Candidates = append(r.Candidates, DecisionCandidate{Model: ModelCapabilities{Name: name, Runtime: "shared"}})
	}
	return r
}

func TestDecisionRequestValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		names   []string
		version int
		wantErr string
	}{
		{"single", []string{"a"}, DecisionVersion, ""},
		{"batch", []string{"a", "b", "c"}, DecisionVersion, ""},
		{"exact keys", []string{"a", "A", " a "}, DecisionVersion, ""},
		{"empty", nil, DecisionVersion, "candidate count"},
		{"duplicate", []string{"a", "b", "a"}, DecisionVersion, "duplicate candidate"},
		{"oversized", []string{"a", "b", "c", "d"}, DecisionVersion, "candidate count"},
		{"empty name", []string{""}, DecisionVersion, "name is empty"},
		{"blank name", []string{" \t"}, DecisionVersion, "name is empty"},
		{"missing version", []string{"a"}, 0, "unsupported version"},
		{"future version", []string{"a"}, DecisionVersion + 1, "unsupported version"},
		{"negative version", []string{"a"}, -1, "unsupported version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := decisionRequestForTest(tc.names...)
			r.Version = tc.version
			checkDecisionError(t, r.Validate(), tc.wantErr)
		})
	}
}

func TestDecisionOutcomeValidate(t *testing.T) {
	t.Parallel()
	// All parallel cases read the same request, exercising read-only validation.
	request := decisionRequestForTest("a", "b", "c")
	for _, tc := range []struct {
		name    string
		change  func(*DecisionOutcome)
		wantErr string
	}{
		{"batch selects last", func(o *DecisionOutcome) { o.SelectedCandidate = "c" }, ""},
		{"unknown estimates and telemetry", func(o *DecisionOutcome) {}, ""},
		{"accepted admission", func(o *DecisionOutcome) { o.Admission = &AdmissionDecision{Accept: true} }, ""},
		{"rejected admission", func(o *DecisionOutcome) { o.Admission = &AdmissionDecision{} }, "admission requires"},
		{"admission without selection", func(o *DecisionOutcome) {
			o.SelectedCandidate = ""
			o.Admission = &AdmissionDecision{Accept: true}
		}, "admission requires"},
		{"known zero", func(o *DecisionOutcome) {
			o.Probability.Known, o.Confidence.Known = true, true
			o.Telemetry = DecisionTelemetry{UsageKnown: true, CachedInputKnown: true, CostKnown: true, LatencyKnown: true}
		}, ""},
		{"upper bounds", func(o *DecisionOutcome) {
			o.Probability = DecisionProbability{Value: 1, Known: true}
			o.Confidence = DecisionProbability{Value: 1, Known: true}
		}, ""},
		{"measured telemetry", func(o *DecisionOutcome) {
			o.Telemetry = DecisionTelemetry{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, UsageKnown: true, CachedInputTokens: 2, CachedInputKnown: true, CostUSD: .01, CostKnown: true, LatencyMs: 3, LatencyKnown: true}
		}, ""},
		{"cached independent of usage", func(o *DecisionOutcome) {
			o.Telemetry = DecisionTelemetry{CachedInputTokens: 2, CachedInputKnown: true}
		}, ""},
		{"no selection", func(o *DecisionOutcome) {
			o.SelectedCandidate = ""
			o.Confidence = DecisionProbability{Value: .8, Known: true}
		}, ""},
		{"no selection known probability", func(o *DecisionOutcome) { o.SelectedCandidate = ""; o.Probability.Known = true }, "probability requires"},
		{"no selection invalid telemetry", func(o *DecisionOutcome) { o.SelectedCandidate = ""; o.Telemetry.CostUSD = 1 }, "unknown cost"},
		{"unknown selection", func(o *DecisionOutcome) { o.SelectedCandidate = "d" }, "unknown selected candidate"},
		{"case mismatch", func(o *DecisionOutcome) { o.SelectedCandidate = "A" }, "unknown selected candidate"},
		{"whitespace mismatch", func(o *DecisionOutcome) { o.SelectedCandidate = " a " }, "unknown selected candidate"},
		{"missing version", func(o *DecisionOutcome) { o.Version = 0 }, "unsupported version"},
		{"future version", func(o *DecisionOutcome) { o.Version++ }, "unsupported version"},
		{"negative version", func(o *DecisionOutcome) { o.Version = -1 }, "unsupported version"},
		{"missing mode", func(o *DecisionOutcome) { o.Mode = "" }, "unsupported mode"},
		{"unknown mode", func(o *DecisionOutcome) { o.Mode = "future" }, "unsupported mode"},
		{"reasons", func(o *DecisionOutcome) {
			o.Reasons = []DecisionReason{{Code: ReasonLowConfidence}, {Code: "CUSTOM", Detail: "explanation"}}
		}, ""},
		{"empty reason", func(o *DecisionOutcome) { o.Reasons = []DecisionReason{{Detail: "missing code"}} }, "reason code is empty"},
		{"blank reason without selection", func(o *DecisionOutcome) { o.SelectedCandidate = ""; o.Reasons = []DecisionReason{{Code: " \t"}} }, "reason code is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission, SelectedCandidate: "b"}
			tc.change(&o)
			before := o
			before.Reasons = append([]DecisionReason(nil), o.Reasons...)
			checkDecisionError(t, o.Validate(request), tc.wantErr)
			if !reflect.DeepEqual(o, before) {
				t.Fatal("validation mutated outcome")
			}
			if !reflect.DeepEqual(request, decisionRequestForTest("a", "b", "c")) {
				t.Fatal("validation mutated request")
			}
		})
	}
	t.Run("invalid request", func(t *testing.T) {
		o := DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission}
		checkDecisionError(t, o.Validate(DecisionRequest{}), "decision request: unsupported version")
	})
}

func TestDecisionOutcomeProbabilityValidation(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"probability", "confidence"} {
		for _, known := range []bool{false, true} {
			for _, tc := range []struct {
				name  string
				value float64
			}{
				{"negative", -.1}, {"above one", 1.1}, {"nan", math.NaN()},
				{"positive infinity", math.Inf(1)}, {"negative infinity", math.Inf(-1)},
				{"fraction", .5}, {"zero", 0},
			} {
				name := field + "/unknown/" + tc.name
				if known {
					name = field + "/known/" + tc.name
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					o := DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission, SelectedCandidate: "a"}
					p := DecisionProbability{Value: tc.value, Known: known}
					if field == "probability" {
						o.Probability = p
					} else {
						o.Confidence = p
					}
					wantErr := field
					if tc.value == 0 || (known && tc.value == .5) {
						wantErr = ""
					}
					checkDecisionError(t, o.Validate(decisionRequestForTest("a")), wantErr)
				})
			}
		}
	}
}

func TestDecisionOutcomeTelemetryValidation(t *testing.T) {
	t.Parallel()
	for _, field := range []struct {
		name     string
		set      func(*DecisionTelemetry, float64, bool)
		floating bool
	}{
		{"input", func(m *DecisionTelemetry, v float64, k bool) { m.InputTokens, m.UsageKnown = int(v), k }, false},
		{"output", func(m *DecisionTelemetry, v float64, k bool) { m.OutputTokens, m.UsageKnown = int(v), k }, false},
		{"total", func(m *DecisionTelemetry, v float64, k bool) { m.TotalTokens, m.UsageKnown = int(v), k }, false},
		{"cached", func(m *DecisionTelemetry, v float64, k bool) { m.CachedInputTokens, m.CachedInputKnown = int(v), k }, false},
		{"cost", func(m *DecisionTelemetry, v float64, k bool) { m.CostUSD, m.CostKnown = v, k }, true},
		{"latency", func(m *DecisionTelemetry, v float64, k bool) { m.LatencyMs, m.LatencyKnown = int64(v), k }, false},
	} {
		for _, known := range []bool{false, true} {
			values := []struct {
				name  string
				value float64
			}{{"negative", -1}, {"zero", 0}, {"positive", 1}}
			if field.floating {
				values = append(values, struct {
					name  string
					value float64
				}{"nan", math.NaN()}, struct {
					name  string
					value float64
				}{"positive infinity", math.Inf(1)}, struct {
					name  string
					value float64
				}{"negative infinity", math.Inf(-1)})
			}
			for _, tc := range values {
				name := field.name + "/unknown/" + tc.name
				if known {
					name = field.name + "/known/" + tc.name
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					o := DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission, SelectedCandidate: "a"}
					field.set(&o.Telemetry, tc.value, known)
					before := o.Telemetry
					wantErr := "decision outcome:"
					if tc.value == 0 || (known && tc.value == 1) {
						wantErr = ""
					}
					checkDecisionError(t, o.Validate(decisionRequestForTest("a")), wantErr)
					// DeepEqual cannot compare NaN; successful validation must preserve all flags and values.
					if wantErr == "" && o.Telemetry != before {
						t.Fatal("validation changed telemetry known/unknown state")
					}
				})
			}
		}
	}
}

func checkDecisionError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	} else if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}
