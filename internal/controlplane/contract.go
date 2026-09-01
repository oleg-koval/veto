// Package controlplane defines the in-process boundary shared by CLI and TUI.
package controlplane

import "context"

// FlagSpec describes a CLI-compatible argument exposed by an action.
type FlagSpec struct {
	Name        string
	Value       string
	Default     string
	Description string
	Required    bool
}

// ActionSpec is the stable command catalog entry rendered by the TUI.
type ActionSpec struct {
	ID          string
	Label       string
	Command     string
	Category    string
	Description string
	Flags       []FlagSpec
}

// ActionRequest is the normalized request passed from a client to the service.
type ActionRequest struct {
	ActionID  string
	Arguments map[string]string
}

// ActionResult contains a completion summary and ephemeral output. Output is
// never persisted by the control plane; clients decide how to render it.
type ActionResult struct {
	ActionID string
	Summary  string
	Model    string
	Output   string
}

// Snapshot is the read-only state needed to render the shell.
type Snapshot struct {
	ActiveAction string
	Status       string
	Provider     string
	Model        string
}

// Event is an ephemeral, non-sensitive update for an active operation.
type Event struct {
	ActionID string
	Kind     string
	Message  string
}

// Service is the in-process control-plane boundary. Implementations must not
// expose credentials or raw prompts in snapshots/events.
type Service interface {
	Snapshot(context.Context) (Snapshot, error)
	Execute(context.Context, ActionRequest) (ActionResult, error)
	Subscribe(context.Context) <-chan Event
	Cancel(context.Context, string) error
}
