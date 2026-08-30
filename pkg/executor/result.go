// Package executor defines the result type returned by model CLI executors.
package executor

import (
	"context"
	"io"
)

// DefaultExecutionMaxTokens is the bounded output budget used when a caller
// does not provide one for a task execution. Admission probes use their own,
// much smaller fixed budget and never use this value.
const DefaultExecutionMaxTokens = 8192

// ExecutionOptions controls a full task execution. MaxOutputTokens <= 0 uses
// DefaultExecutionMaxTokens so callers cannot accidentally request an
// unbounded provider response.
type ExecutionOptions struct {
	MaxOutputTokens int
}

// Usage contains provider-reported token counts. Known distinguishes an
// omitted usage object from a provider that explicitly reported zero values.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Known        bool
}

// Result holds the output of a single model invocation.
type Result struct {
	Output       string
	Error        error
	Usage        Usage
	CostUSD      float64
	CostKnown    bool
	FinishReason string
	Truncated    bool
}

// RuntimeEventKind identifies safe, structured lifecycle events emitted by an
// agent runtime. Runtime adapters must not include prompts, tool arguments,
// tool output, credentials, or file contents in these events.
type RuntimeEventKind string

const (
	RuntimeToolStarted       RuntimeEventKind = "tool.started"
	RuntimeToolCompleted     RuntimeEventKind = "tool.completed"
	RuntimeToolError         RuntimeEventKind = "tool.error"
	RuntimeApprovalRequested RuntimeEventKind = "approval.requested"
	RuntimeApprovalGranted   RuntimeEventKind = "approval.granted"
	RuntimeApprovalDenied    RuntimeEventKind = "approval.denied"
	RuntimeArtifactCreated   RuntimeEventKind = "artifact.created"
)

// RuntimeEvent is the allowlisted event bridge from an agent runtime into
// Veto's ledger. Name is a validated tool or artifact kind, never arguments,
// output, paths, or file contents.
type RuntimeEvent struct {
	Kind   RuntimeEventKind
	Name   string
	Status string
	Count  int
}

// EventTaskExecutor streams task text while reporting structured runtime
// lifecycle events and returning complete usage/cost telemetry.
type EventTaskExecutor interface {
	ExecuteWithEvents(context.Context, string, ExecutionOptions, io.Writer, func(RuntimeEvent)) Result
}

// TaskExecutor is implemented by transports that can perform a full task
// execution. Run remains the short admission-probe method for compatibility.
type TaskExecutor interface {
	Execute(ctx context.Context, prompt string, options ExecutionOptions) Result
}

// RuntimeAdapter is the complete transport contract used by veto. Admission
// and execution remain separate methods with independent output budgets.
type RuntimeAdapter interface {
	Run(ctx context.Context, prompt string) Result
	TaskExecutor
	ToolProvider
	RuntimeID() string
}

func (o ExecutionOptions) maxOutputTokens() int {
	if o.MaxOutputTokens <= 0 {
		return DefaultExecutionMaxTokens
	}
	return o.MaxOutputTokens
}

// ToolProvider reports tools the transport can actually invoke. Text-only
// runtimes return nil so model-declared capabilities never grant tool access.
type ToolProvider interface {
	EffectiveTools() []string
}

// ToolCapabilityStatus distinguishes a known empty tool list from a runtime
// whose project/session-specific tools have not been discovered yet.
type ToolCapabilityStatus interface {
	EffectiveToolsKnown() bool
}
