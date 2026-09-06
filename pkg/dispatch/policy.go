// Package dispatch contains the small, explainable policy used by the native
// dispatch experiment. It deliberately has no model calls or historical input.
package dispatch

import (
	"fmt"
	"sort"
	"strings"
)

type Mode string

const (
	ModeManual      Mode = "manual"
	ModeChooseAgent Mode = "choose-agent"
	ModeChooseModel Mode = "choose-model"
)

type AuthState string

const (
	AuthAuthenticated   AuthState = "authenticated"
	AuthUnauthenticated AuthState = "unauthenticated"
	AuthUnknown         AuthState = "unknown"
)

type BillingMode string

const (
	BillingSubscription  BillingMode = "subscription"
	BillingAPI           BillingMode = "api"
	BillingUnknown       BillingMode = "unknown"
	BillingNotApplicable BillingMode = "not-applicable"
)

type AgentStatus struct {
	Name        string
	Installed   bool
	Auth        AuthState
	Billing     BillingMode
	Unavailable bool
	Warning     string
}

type Model struct {
	Name   string
	Agent  string
	Status string
}

type Request struct {
	Mode         Mode
	Agent        string
	Model        string
	DefaultAgent string
	Kind         string
	Risk         string
}

type Decision struct {
	Mode                 Mode
	Agent                string
	Model                string
	Reason               string
	Constraints          []string
	AlternativesExcluded []string
	Unknown              []string
	HistoricalInfluenced bool
}

func (d Decision) Explanation() string {
	selection := d.Agent
	if d.Model != "" {
		selection += " / " + d.Model
	}
	return fmt.Sprintf("%s: %s", selection, d.Reason)
}

func Decide(request Request, agents []AgentStatus, models []Model) (Decision, error) {
	request.Mode = normalizeMode(request.Mode)
	request.Agent = strings.ToLower(strings.TrimSpace(request.Agent))
	request.DefaultAgent = strings.ToLower(strings.TrimSpace(request.DefaultAgent))
	if request.Mode == ModeManual || request.Mode == ModeChooseModel {
		if request.Agent == "" {
			return Decision{}, fmt.Errorf("agent is required for %s", request.Mode)
		}
	}
	if request.Mode == ModeManual {
		status, err := agentByName(agents, request.Agent)
		if err != nil {
			return Decision{}, err
		}
		if err := ensureAvailable(status); err != nil {
			return Decision{}, err
		}
		return Decision{Mode: request.Mode, Agent: request.Agent, Model: request.Model,
			Reason:      "the user selected this native agent; Veto made no routing decision",
			Constraints: []string{"manual selection"}, Unknown: unknowns(status)}, nil
	}

	if request.Mode == ModeChooseModel {
		status, err := agentByName(agents, request.Agent)
		if err != nil {
			return Decision{}, err
		}
		if err := ensureAvailable(status); err != nil {
			return Decision{}, err
		}
		model := request.Model
		if model == "" {
			model = modelForKind(request.Kind, request.Risk, request.Agent)
		}
		if model != "" && !modelSupported(models, request.Agent, model) {
			return Decision{}, fmt.Errorf("model %q is not known to be supported by %s", model, request.Agent)
		}
		reason := "the fixed model policy maps this task kind to a deterministic model"
		unknown := unknowns(status)
		if model == "" {
			reason = "the agent's native default is retained because Veto has no safe model metadata"
			unknown = append(unknown, "model catalog/native default")
		}
		return Decision{Mode: request.Mode, Agent: request.Agent, Model: model, Reason: reason,
			Constraints: []string{"agent fixed by user", "historical data not used"}, Unknown: unique(unknown)}, nil
	}

	eligible := make([]AgentStatus, 0, len(agents))
	excluded := make([]string, 0)
	unknown := make([]string, 0)
	for _, status := range agents {
		name := strings.ToLower(strings.TrimSpace(status.Name))
		if name == "" || !status.Installed || status.Auth == AuthUnauthenticated || status.Unavailable {
			excluded = append(excluded, exclusion(status))
			continue
		}
		eligible = append(eligible, status)
		unknown = append(unknown, unknowns(status)...)
	}
	if len(eligible) == 0 {
		return Decision{}, fmt.Errorf("no available native agent (install and authenticate Claude Code or Codex, or clear temporary unavailability)")
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Name < eligible[j].Name })
	selected := eligible[0]
	if request.DefaultAgent != "" {
		for _, candidate := range eligible {
			if strings.EqualFold(candidate.Name, request.DefaultAgent) {
				selected = candidate
				break
			}
		}
	} else {
		preferred := preferredAgent(request.Kind)
		for _, candidate := range eligible {
			if strings.EqualFold(candidate.Name, preferred) {
				selected = candidate
				break
			}
		}
	}
	return Decision{Mode: request.Mode, Agent: selected.Name,
		Reason:               reasonForAgent(request, selected),
		Constraints:          []string{"installed", "authentication not known to be unavailable", "temporary unavailability excluded", "historical data not used"},
		AlternativesExcluded: unique(excluded), Unknown: unique(unknown)}, nil
}

func normalizeMode(mode Mode) Mode {
	switch mode {
	case ModeManual, ModeChooseAgent, ModeChooseModel:
		return mode
	default:
		return ModeManual
	}
}

func agentByName(agents []AgentStatus, name string) (AgentStatus, error) {
	for _, agent := range agents {
		if strings.EqualFold(agent.Name, name) {
			return agent, nil
		}
	}
	return AgentStatus{}, fmt.Errorf("unknown native agent %q", name)
}

func ensureAvailable(status AgentStatus) error {
	if !status.Installed {
		return fmt.Errorf("%s executable is not installed or not in PATH", status.Name)
	}
	if status.Auth == AuthUnauthenticated {
		return fmt.Errorf("%s is not authenticated; authenticate the native CLI first", status.Name)
	}
	if status.Unavailable {
		return fmt.Errorf("%s is temporarily unavailable", status.Name)
	}
	return nil
}

func unknowns(status AgentStatus) []string {
	unknown := make([]string, 0, 2)
	if status.Auth == AuthUnknown {
		unknown = append(unknown, status.Name+" authentication")
	}
	if status.Billing == BillingUnknown {
		unknown = append(unknown, status.Name+" billing/cost")
	}
	if status.Warning != "" {
		unknown = append(unknown, status.Warning)
	}
	return unknown
}

func exclusion(status AgentStatus) string {
	switch {
	case !status.Installed:
		return status.Name + " excluded: executable missing"
	case status.Auth == AuthUnauthenticated:
		return status.Name + " excluded: not authenticated"
	case status.Unavailable:
		return status.Name + " excluded: temporarily unavailable"
	default:
		return status.Name + " excluded"
	}
}

func preferredAgent(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "code-change", "debug", "refactor":
		return "codex"
	default:
		return "claude"
	}
}

func reasonForAgent(request Request, selected AgentStatus) string {
	if request.DefaultAgent != "" && strings.EqualFold(request.DefaultAgent, selected.Name) {
		return "the configured default agent is available and satisfies the fixed constraints"
	}
	return fmt.Sprintf("the fixed policy prefers %s for %s tasks; no historical score or model self-admission was used", selected.Name, valueOrDefault(request.Kind, "general"))
}

func modelForKind(kind, risk, agent string) string {
	if agent != "claude" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "extract", "summarize":
		return "haiku"
	case "debug", "plan":
		return "opus"
	case "code-change", "review", "refactor":
		return "sonnet"
	default:
		if strings.EqualFold(risk, "high") {
			return "opus"
		}
		return "sonnet"
	}
}

func modelSupported(models []Model, agent, model string) bool {
	for _, candidate := range models {
		if strings.EqualFold(candidate.Agent, agent) && strings.EqualFold(candidate.Name, model) && candidate.Status != "unsupported" {
			return true
		}
	}
	// Claude aliases are documented CLI inputs even when local metadata is stale.
	if agent == "claude" {
		return model == "haiku" || model == "sonnet" || model == "opus"
	}
	// Codex accepts an explicit --model and remains responsible for validating
	// the model against its own current catalog and account capabilities.
	return agent == "codex"
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
