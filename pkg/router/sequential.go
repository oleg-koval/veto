package router

import (
	"context"
	"math"
	"time"
)

// SequentialAdmissionEngine asks the offered candidates in order and stops at
// the first acceptance. The gate retains admission parsing and policy checks.
type SequentialAdmissionEngine struct{ gate *AdmissionGate }

// NewSequentialAdmissionEngine wraps the existing admission gate.
func NewSequentialAdmissionEngine(gate *AdmissionGate) *SequentialAdmissionEngine {
	return &SequentialAdmissionEngine{gate: gate}
}

// sequentialAdmissionOptions keeps legacy live side effects and request-local
// deadlines private to the router, rather than adding them to every engine's API.
type sequentialAdmissionOptions struct {
	timeout time.Duration
	emit    func(ProgressEvent)
	log     func(string, AdmissionDecision)
}

// Decide validates the request and asks non-skipped candidates in order until
// one accepts, preserving admission logging, progress events, and deadlines.
// It returns an empty selection when candidates are exhausted, or an error
// for an invalid request or cancellation observed during admission.
func (e *SequentialAdmissionEngine) Decide(ctx context.Context, request DecisionRequest) (DecisionOutcome, error) {
	if err := request.Validate(); err != nil {
		return DecisionOutcome{}, err
	}
	outcome := DecisionOutcome{Version: DecisionVersion, Mode: DecisionModeSequentialAdmission}
	task := request.Task
	skipSet := make(map[string]bool, len(task.SkipModels))
	for _, name := range task.SkipModels {
		skipSet[name] = true
	}
	emit := func(event ProgressEvent) {
		if request.admission.emit != nil {
			request.admission.emit(event)
		}
	}
	log := func(model string, decision AdmissionDecision) {
		if request.admission.log != nil {
			request.admission.log(model, decision)
		}
	}
	// Every admission call consumes the per-run budget, including transport
	// failures. Do not suppress sibling models that share a runtime identity:
	// provider APIs can return model-specific failures, and another alias may
	// still be viable.
	attempts := 0
	for _, candidate := range request.Candidates {
		model := candidate.Model
		if skipSet[model.Name] {
			continue
		}
		if attempts >= MaxDecisionCandidates {
			break
		}
		if ctx.Err() != nil {
			return DecisionOutcome{}, ctx.Err()
		}
		emit(ProgressEvent{Kind: EventAskStart, Model: model.Name})
		attempts++

		decision, telemetry, err := e.gate.AskWithTimeoutMeasured(ctx, task, model, request.admission.timeout)
		mergeDecisionTelemetry(&outcome.Telemetry, telemetry, attempts == 1)
		if err != nil {
			if ctx.Err() != nil {
				return DecisionOutcome{}, ctx.Err()
			}
			// exec/parse failure — log, show the real error, skip
			log(model.Name, AdmissionDecision{
				Accept:      false,
				ReasonCodes: []string{ReasonParseFailure},
			})
			emit(ProgressEvent{Kind: EventAskError, Model: model.Name,
				Detail: err.Error()})
			continue
		}
		log(model.Name, decision)
		if decision.Accept {
			emit(ProgressEvent{
				Kind:       EventAskAccept,
				Model:      model.Name,
				Confidence: decision.Confidence,
				EstTokens:  decision.EstimatedTokens,
				EstCost:    decision.EstimatedCostUSD,
			})
			outcome.SelectedCandidate = model.Name
			outcome.Admission = &decision
			// Preserve permissive legacy parsing in Admission, without exporting
			// out-of-range values as normalized probability estimates.
			if decision.Confidence >= 0 && decision.Confidence <= 1 {
				outcome.Confidence = DecisionProbability{Value: decision.Confidence, Known: true}
			}
			return outcome, nil
		}
		emit(ProgressEvent{Kind: EventAskReject, Model: model.Name,
			Reasons: decision.ReasonCodes})
	}
	return outcome, nil
}

func mergeDecisionTelemetry(total *DecisionTelemetry, current DecisionTelemetry, first bool) {
	if first {
		*total = current
		return
	}
	if total.UsageKnown && current.UsageKnown {
		input, inputOK := addNonnegativeInt(total.InputTokens, current.InputTokens)
		output, outputOK := addNonnegativeInt(total.OutputTokens, current.OutputTokens)
		combined, totalOK := addNonnegativeInt(total.TotalTokens, current.TotalTokens)
		if inputOK && outputOK && totalOK {
			total.InputTokens, total.OutputTokens, total.TotalTokens = input, output, combined
		} else {
			total.InputTokens, total.OutputTokens, total.TotalTokens, total.UsageKnown = 0, 0, 0, false
		}
	} else {
		total.InputTokens, total.OutputTokens, total.TotalTokens, total.UsageKnown = 0, 0, 0, false
	}
	if total.CachedInputKnown && current.CachedInputKnown {
		if value, ok := addNonnegativeInt(total.CachedInputTokens, current.CachedInputTokens); ok {
			total.CachedInputTokens = value
		} else {
			total.CachedInputTokens, total.CachedInputKnown = 0, false
		}
	} else {
		total.CachedInputTokens, total.CachedInputKnown = 0, false
	}
	if total.CostKnown && current.CostKnown {
		total.CostUSD += current.CostUSD
		if math.IsInf(total.CostUSD, 0) || math.IsNaN(total.CostUSD) {
			total.CostUSD, total.CostKnown = 0, false
		}
	} else {
		total.CostUSD, total.CostKnown = 0, false
	}
	if total.LatencyKnown && current.LatencyKnown {
		if current.LatencyMs <= math.MaxInt64-total.LatencyMs {
			total.LatencyMs += current.LatencyMs
		} else {
			total.LatencyMs, total.LatencyKnown = 0, false
		}
	} else {
		total.LatencyMs, total.LatencyKnown = 0, false
	}
}

func addNonnegativeInt(left, right int) (int, bool) {
	if left < 0 || right < 0 || right > math.MaxInt-left {
		return 0, false
	}
	return left + right, true
}
