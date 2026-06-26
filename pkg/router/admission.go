package router

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oleg-koval/veto/pkg/executor"
)

//go:generate moq -out mocks/executor.go -pkg mocks -skip-ensure -fmt goimports . Executor

// Executor is the interface a model provider must satisfy.
// Defined at consumer side so tests can inject a mock without importing pkg/executor.
type Executor interface {
	Run(ctx context.Context, prompt string) executor.Result
}

// ExecutorFactory returns the right executor for a given model name.
// Concrete implementations live in pkg/executor; they satisfy this via duck typing.
type ExecutorFactory interface {
	For(modelName string) (Executor, bool)
}

// AdmissionGate asks a candidate model whether it accepts a task.
// The model must respond with JSON only — any other format is treated as rejection.
type AdmissionGate struct {
	factory ExecutorFactory
}

// NewAdmissionGate creates a gate using the same executor for all models.
// ponytail: single-executor path for tests and single-provider setups.
func NewAdmissionGate(exec Executor) *AdmissionGate {
	return &AdmissionGate{factory: singleFactory{exec: exec}}
}

// NewAdmissionGateWithFactory creates a gate that selects executors per model name.
func NewAdmissionGateWithFactory(factory ExecutorFactory) *AdmissionGate {
	return &AdmissionGate{factory: factory}
}

type singleFactory struct{ exec Executor }

func (f singleFactory) For(_ string) (Executor, bool) { return f.exec, true }

// Ask runs an admission check: sends a structured prompt to the model and parses
// the JSON response into an AdmissionDecision.
// on any failure (exec error, parse error, no JSON found) the gate rejects — never silently accepts.
func (g *AdmissionGate) Ask(ctx context.Context, task TaskSpec, model ModelCapabilities) (AdmissionDecision, error) {
	exec, ok := g.factory.For(model.Name)
	if !ok {
		return AdmissionDecision{Accept: false, ReasonCodes: []string{ReasonParseFailure}},
			fmt.Errorf("no executor registered for model %q", model.Name)
	}
	prompt := buildAdmissionPrompt(task, model)
	result := exec.Run(ctx, prompt)
	if result.Error != nil {
		return AdmissionDecision{
			Accept:      false,
			ReasonCodes: []string{ReasonParseFailure},
		}, fmt.Errorf("admission gate executor: %w", result.Error)
	}

	if decision, ok := parseAdmissionJSON(result.Output); ok {
		if decision.Accept && decision.Confidence < 0.7 {
			return AdmissionDecision{Accept: false, ReasonCodes: []string{ReasonLowConfidence}}, nil
		}
		return decision, nil
	}
	return AdmissionDecision{Accept: false, ReasonCodes: []string{ReasonParseFailure}}, nil
}

// buildAdmissionPrompt constructs the prompt sent to the candidate model.
// the prompt explicitly forbids prose — JSON only.
func buildAdmissionPrompt(task TaskSpec, model ModelCapabilities) string {
	constraints := strings.Join(task.Constraints, ", ")
	if constraints == "" {
		constraints = "none"
	}
	tools := strings.Join(task.RequiredTools, ", ")
	if tools == "" {
		tools = "none"
	}

	return fmt.Sprintf(`You are the %s model (%s tier). A task router has selected you as a candidate for the following task.

TASK:
  kind: %s
  objective: %s
  constraints: %s
  required tools: %s
  risk: %s

YOUR PROFILE:
  max context tokens: %d
  supported tools: %s

Decide whether you accept this task. Respond with ONLY valid JSON — no prose, no explanation, no markdown.
The JSON must match this exact schema:

{
  "accept": <bool>,
  "confidence": <float 0.0-1.0>,
  "reason_codes": [<string>, ...],
  "estimated_tokens": <int>,
  "estimated_cost_usd": <float>,
  "suggested_alternative_model": <string or "">,
  "required_task_changes": [<string>, ...]
}

Valid reason_codes when rejecting:
  MISSING_REQUIRED_TOOL, CONTEXT_TOO_LARGE, COST_CEILING_EXCEEDED,
  TASK_KIND_OUTSIDE_STRENGTHS, RISK_TOO_HIGH

If accepting: set "accept": true, "confidence" >= 0.7, "reason_codes": [].
If rejecting: set "accept": false, populate "reason_codes" with at least one code.

Respond with JSON only. Nothing before or after the JSON object.`,
		model.Name, model.Tier,
		task.Kind, task.Objective, constraints, tools, task.Risk,
		model.MaxContextTokens,
		strings.Join(model.SupportsTools, ", "),
	)
}

// admissionJSON is the wire format that maps the model's response to AdmissionDecision.
type admissionJSON struct {
	Accept                    bool     `json:"accept"`
	Confidence                float64  `json:"confidence"`
	ReasonCodes               []string `json:"reason_codes"`
	EstimatedTokens           int      `json:"estimated_tokens"`
	EstimatedCostUSD          float64  `json:"estimated_cost_usd"`
	SuggestedAlternativeModel string   `json:"suggested_alternative_model"`
	RequiredTaskChanges       []string `json:"required_task_changes"`
}

// parseAdmissionJSON extracts a JSON object from model output and maps it to AdmissionDecision.
// returns (decision, true) on success; (zero, false) when no valid JSON is found.
// the model may prepend/append prose — we find the first '{' and last '}'.
func parseAdmissionJSON(output string) (AdmissionDecision, bool) {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start == -1 || end == -1 || end <= start {
		return AdmissionDecision{}, false
	}

	raw := output[start : end+1]
	var j admissionJSON
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return AdmissionDecision{}, false
	}

	return AdmissionDecision(j), true
}
