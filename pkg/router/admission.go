package router

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const admissionModelTimeout = 20 * time.Second

//go:generate moq -out mocks/executor.go -pkg mocks -skip-ensure -fmt goimports . Executor

// AdmissionResult is the small result needed by the admission boundary.
// Runtime-specific execution telemetry stays in the execution contract and
// concrete adapter packages.
type AdmissionResult struct {
	Output string
	Error  error
}

// ToolCapabilities describes the tools available to an admission probe.
// Known distinguishes a known empty list from a runtime that has not yet
// discovered its project/session-specific tools.
type ToolCapabilities struct {
	Tools []string
	Known bool
}

// Executor is the interface a model provider must satisfy for admission.
// Defined at the consumer side so the router has no dependency on a runtime
// implementation package.
type Executor interface {
	Run(ctx context.Context, prompt string) AdmissionResult
}

// ToolProvider optionally reports the tools available to the admission probe.
// Runtimes that do not implement it are treated as text-only.
type ToolProvider interface {
	AdmissionTools() ToolCapabilities
}

// ExecutorFactory returns the right admission executor for a given model name.
// Concrete runtime adapters satisfy this contract at the composition edge.
type ExecutorFactory interface {
	For(modelName string) (Executor, bool)
}

// AdmissionGate asks a candidate model whether it accepts a task.
// The model must respond with JSON only — any other format is treated as rejection.
type AdmissionGate struct {
	factory ExecutorFactory
	timeout time.Duration
}

// NewAdmissionGate creates a gate using the same executor for all models.
// ponytail: single-executor path for tests and single-provider setups.
func NewAdmissionGate(exec Executor) *AdmissionGate {
	return &AdmissionGate{factory: singleFactory{exec: exec}, timeout: admissionModelTimeout}
}

// NewAdmissionGateWithFactory creates a gate that selects executors per model name.
func NewAdmissionGateWithFactory(factory ExecutorFactory) *AdmissionGate {
	return &AdmissionGate{factory: factory, timeout: admissionModelTimeout}
}

// SetTimeout changes the per-model admission deadline. Non-positive values
// retain the default deadline.
func (g *AdmissionGate) SetTimeout(timeout time.Duration) {
	if timeout > 0 {
		g.timeout = timeout
	}
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
	// per-model cap: a hung model shouldn't block routing indefinitely
	timeout := g.timeout
	if timeout <= 0 {
		timeout = admissionModelTimeout
	}
	mCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// use the executor's actual tool list, not the model's theoretical capability.
	// HTTP executors are text-only — no tools pass through a plain chat completion.
	// Only CLIExecutor (claude -p) runs with real tool access.
	effectiveTools := model.SupportsTools
	toolsKnown := true
	if tp, ok := exec.(ToolProvider); ok {
		capabilities := tp.AdmissionTools()
		effectiveTools = capabilities.Tools
		toolsKnown = capabilities.Known
	} else {
		effectiveTools = nil // text-only: advertise no tools so model can reject accurately
	}

	prompt := buildAdmissionPromptWithToolStatus(task, model, effectiveTools, toolsKnown)
	result := exec.Run(mCtx, prompt)
	if result.Error != nil {
		return AdmissionDecision{Accept: false, ReasonCodes: []string{ReasonParseFailure}},
			result.Error
	}

	if decision, ok := parseAdmissionJSON(result.Output); ok {
		if decision.Accept && task.MaxCostUSD > 0 && decision.EstimatedCostUSD > task.MaxCostUSD {
			return AdmissionDecision{Accept: false, Confidence: decision.Confidence,
				ReasonCodes: []string{ReasonCostCeiling}, EstimatedCostUSD: decision.EstimatedCostUSD}, nil
		}
		if decision.Accept && decision.Confidence < 0.7 {
			return AdmissionDecision{Accept: false, ReasonCodes: []string{ReasonLowConfidence}}, nil
		}
		return decision, nil
	}
	return AdmissionDecision{Accept: false, ReasonCodes: []string{ReasonParseFailure}}, nil
}

// buildAdmissionPrompt constructs the prompt sent to the candidate model.
// effectiveTools is what the executor can actually provide — nil means text-only
// (the executor is a plain HTTP chat API with no tool definitions passed).
// the prompt explicitly forbids prose — JSON only.
func buildAdmissionPrompt(task TaskSpec, model ModelCapabilities, effectiveTools []string) string {
	return buildAdmissionPromptWithToolStatus(task, model, effectiveTools, true)
}

func buildAdmissionPromptWithToolStatus(task TaskSpec, model ModelCapabilities, effectiveTools []string, toolsKnown bool) string {
	constraints := strings.Join(task.Constraints, ", ")
	if constraints == "" {
		constraints = "none"
	}
	required := strings.Join(task.RequiredTools, ", ")
	if required == "" && task.RequiresExecutableTools {
		required = "an executable agent runtime (project-specific tools are resolved at execution)"
	} else if required == "" {
		required = "none"
	}

	var toolsLine string
	if !toolsKnown {
		toolsLine = "unknown (the agent runtime resolves project-specific tools and permissions when execution starts; do not assume shell, file, browser, or network access)"
	} else if len(effectiveTools) == 0 {
		toolsLine = "none (text-output mode — you will receive the task and must respond with the answer directly; you cannot execute code, read files, or write files)"
	} else {
		toolsLine = strings.Join(effectiveTools, ", ")
	}
	contextLine := "unknown"
	if model.MaxContextTokens > 0 {
		contextLine = fmt.Sprintf("%d", model.MaxContextTokens)
	}

	return fmt.Sprintf(`You are the %s model (%s tier). A task router has selected you as a candidate for the following task.

This is admission only. Do not inspect or act on the current workspace. Decide
whether you can complete the task later from the caller's workspace using the
declared execution tools. Treat the objective as user-authorized within its
stated scope; do not require confirmation already present in the objective.

TASK:
  kind: %s
  objective: %s
  constraints: %s
  required tools: %s
  risk: %s

YOUR PROFILE:
  max context tokens: %s
  available tools (actual, not theoretical): %s

Decide whether you accept this task given the tools actually available to you.
Respond with ONLY valid JSON — no prose, no explanation, no markdown.
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
		task.Kind, task.Objective, constraints, required, task.Risk,
		contextLine,
		toolsLine,
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
	if start == -1 {
		return AdmissionDecision{}, false
	}
	// Use Decoder so trailing prose (common in Llama/Mistral responses) is ignored.
	var j admissionJSON
	if err := json.NewDecoder(strings.NewReader(output[start:])).Decode(&j); err != nil {
		return AdmissionDecision{}, false
	}
	return AdmissionDecision(j), true
}
