// Package executor contains concrete provider and CLI transports.
package executor

import "github.com/oleg-koval/veto/pkg/execution"

// DefaultExecutionMaxTokens is the bounded output budget used when a caller
// does not provide one for a task execution. Admission probes use their own,
// much smaller fixed budget and never use this value.
const DefaultExecutionMaxTokens = execution.DefaultExecutionMaxTokens

// ExecutionOptions controls a full task execution. MaxOutputTokens <= 0 uses
// DefaultExecutionMaxTokens so callers cannot accidentally request an
// unbounded provider response.
type ExecutionOptions = execution.ExecutionOptions

// Usage contains provider-reported token counts. Known distinguishes an
// omitted usage object from a provider that explicitly reported zero values.
type Usage = execution.Usage

// Result holds the output of a single model invocation.
type Result = execution.Result

// RuntimeEventKind identifies safe, structured lifecycle events emitted by an
// agent runtime. Runtime adapters must not include prompts, tool arguments,
// tool output, credentials, or file contents in these events.
type RuntimeEventKind = execution.RuntimeEventKind

const (
	RuntimeToolStarted       = execution.RuntimeToolStarted
	RuntimeToolCompleted     = execution.RuntimeToolCompleted
	RuntimeToolError         = execution.RuntimeToolError
	RuntimeApprovalRequested = execution.RuntimeApprovalRequested
	RuntimeApprovalGranted   = execution.RuntimeApprovalGranted
	RuntimeApprovalDenied    = execution.RuntimeApprovalDenied
	RuntimeArtifactCreated   = execution.RuntimeArtifactCreated
)

// RuntimeEvent is the allowlisted event bridge from an agent runtime into
// Veto's ledger. Name is a validated tool or artifact kind, never arguments,
// output, paths, or file contents.
type RuntimeEvent = execution.RuntimeEvent

// EventTaskExecutor streams task text while reporting structured runtime
// lifecycle events and returning complete usage/cost telemetry.
type EventTaskExecutor = execution.EventTaskExecutor

// TaskExecutor is implemented by transports that can perform a full task
// execution. Run remains the short admission-probe method for compatibility.
type TaskExecutor = execution.TaskExecutor

type ToolProvider = execution.ToolProvider

// ToolCapabilityStatus distinguishes a known empty tool list from a runtime
// whose project/session-specific tools have not been discovered yet.
type ToolCapabilityStatus = execution.ToolCapabilityStatus

type RuntimeAdapter = execution.RuntimeAdapter
