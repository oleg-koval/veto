// Package controlplane defines the in-process boundary shared by CLI and TUI.
package controlplane

import (
	"context"
	"time"
)

const SchemaVersion = 1

// FlagSpec describes a CLI-compatible argument exposed by an action.
type FlagSpec struct {
	Name        string
	Value       string
	Default     string
	Description string
	Required    bool
	Secret      bool
}

// ActionSpec is the stable command catalog entry rendered by the TUI.
type ActionSpec struct {
	ID          string
	Label       string
	Command     string
	Category    string
	Description string
	Flags       []FlagSpec
	Subcommands []string
}

// ActionRequest is the normalized request passed from a client to the service.
type ActionRequest struct {
	Version   int
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
	Monitor      MonitorSnapshot
	Providers    []ProviderSnapshot
	Models       []ModelSnapshot
	History      []HistorySnapshot
	Plans        []PlanSnapshot
	Health       []HealthSnapshot
	Analytics    AnalyticsSnapshot
	Integrations []IntegrationSnapshot
}

// MonitorSnapshot contains bounded, non-sensitive operational counters for
// active and recently completed work.
type MonitorSnapshot struct {
	ActiveSessions   int
	ActiveTools      int
	PendingApprovals int
	Artifacts        int
	TotalTokens      int
	TokensKnown      bool
	CostUSD          float64
	CostKnown        bool
	LatencyMs        int64
	LatencyKnown     bool
}

// ProviderSnapshot contains safe availability metadata only.
type ProviderSnapshot struct {
	Name       string
	Configured bool
	ModelCount int
}

// ModelSnapshot contains safe catalog metadata only; credentials and prompt
// content are deliberately absent.
type ModelSnapshot struct {
	Name                 string
	Source               string
	Provider             string
	Runtime              string
	Tier                 string
	ContextTokens        int
	Tools                []string
	ToolsKnown           bool
	CostPer1kInputUSD    float64
	CostPer1kOutputUSD   float64
	CostPer1kInputKnown  bool
	CostPer1kOutputKnown bool
	Status               string
	Pinned               bool
	Favorite             bool
	Excluded             bool
}

// HistorySnapshot is a redacted ledger entry suitable for local rendering.
type HistorySnapshot struct {
	Timestamp time.Time
	Type      string
	Model     string
	Runtime   string
	Status    string
}

type PlanSnapshot struct {
	Name string
}

type HealthSnapshot struct {
	ID      string
	Status  string
	Message string
}

type AnalyticsSnapshot struct {
	LocalCollection       bool
	LocalPath             string
	RetentionDays         int
	RemoteSharing         string
	RemoteTransportActive bool
}

type IntegrationSnapshot struct {
	Name   string
	Status string
	Detail string
}

// Event is an ephemeral, non-sensitive update for an active operation.
type Event struct {
	Version  int
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
