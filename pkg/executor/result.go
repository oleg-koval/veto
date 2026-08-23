// Package executor defines the result type returned by model CLI executors.
package executor

import "context"

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
	FinishReason string
	Truncated    bool
}

// TaskExecutor is implemented by transports that can perform a full task
// execution. Run remains the short admission-probe method for compatibility.
type TaskExecutor interface {
	Execute(ctx context.Context, prompt string, options ExecutionOptions) Result
}

func (o ExecutionOptions) maxOutputTokens() int {
	if o.MaxOutputTokens <= 0 {
		return DefaultExecutionMaxTokens
	}
	return o.MaxOutputTokens
}

// ToolProvider is implemented by executors that can actually invoke tools
// (bash, read, write, edit) during execution. HTTP-only executors are
// text-only and do not implement this — the model receives no tool definitions
// and can only return text.
type ToolProvider interface {
	EffectiveTools() []string
}
