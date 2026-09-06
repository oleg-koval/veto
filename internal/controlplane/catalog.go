package controlplane

import (
	"strconv"

	"github.com/oleg-koval/veto/pkg/execution"
)

// Catalog is the command inventory used by both palette and help surfaces.
type Catalog struct {
	actions []ActionSpec
}

// DefaultCatalog mirrors the top-level commands printed by veto help. Flags
// are added here as the typed parity contract, keeping the TUI from inventing
// a second command router.
func DefaultCatalog() Catalog {
	return Catalog{actions: []ActionSpec{
		{ID: "login", Label: "Login", Command: "login", Category: "Providers", Description: "Connect a provider with masked key input.", Flags: loginFlags()},
		{ID: "logout", Label: "Logout", Command: "logout", Category: "Providers", Description: "Remove a configured provider or local model.", Flags: []FlagSpec{{Name: "target", Value: "string", Required: true, Description: "Provider environment key, OpenCode, subscription, or local model name."}}},
		{ID: "setup", Label: "Setup", Command: "setup", Category: "Workspace", Description: "Discover and approve skills.", Flags: []FlagSpec{{Name: "auto-approve", Value: "bool", Description: "Approve all discovered skills in the selected directory."}, {Name: "approved-files", Value: "string", Description: "Comma-separated discovered skill paths to approve individually."}, {Name: "directory", Value: "path", Description: "Skill directory to scan; defaults to ~/.claude/skills."}}},
		{ID: "run", Label: "Run", Command: "run", Category: "Execution", Description: "Route a task and execute the response.", Flags: runFlags()},
		{ID: "exec", Label: "Execute plan", Command: "exec", Category: "Execution", Description: "Execute a veto plan step by step.", Flags: append([]FlagSpec{{Name: "plan", Value: "path", Required: true, Description: "Path to a veto plan markdown file."}}, execFlags()...)},
		{ID: "route", Label: "Route", Command: "route", Category: "Routing", Description: "Choose the best available model without execution.", Flags: routeFlags()},
		{ID: "benchmark", Label: "Benchmark", Command: "benchmark", Category: "Diagnostics", Description: "Replay an offline routing corpus and emit metrics.", Flags: []FlagSpec{{Name: "corpus", Value: "path", Default: "internal/eval/testdata/routing_corpus.json", Description: "Offline routing corpus JSON file."}}},
		{ID: "verify-models", Label: "Verify models", Command: "verify-models", Category: "Diagnostics", Description: "Verify catalog IDs against a provider account.", Flags: []FlagSpec{{Name: "provider", Value: "string", Default: "openai", Description: "Provider to verify."}, {Name: "endpoint", Value: "url", Description: "Override the provider model-list URL."}, {Name: "artifacts-dir", Value: "path", Default: "artifacts/http", Description: "Directory for raw response artifacts."}, {Name: "timeout", Value: "duration", Default: "20s", Description: "HTTP request timeout."}, {Name: "json", Value: "bool", Description: "Emit one JSON result line."}}},
		{ID: "doctor", Label: "Doctor", Command: "doctor", Category: "Diagnostics", Description: "Diagnose installation and ~/.veto integrity.", Flags: []FlagSpec{{Name: "fix", Value: "bool", Description: "Repair only safe filesystem and official-binary integrity findings."}, {Name: "offline", Value: "bool", Default: "true", Description: "Skip release-integrity network checks."}, {Name: "json", Value: "bool", Description: "Emit a machine-readable diagnostic report."}}},
		{ID: "feedback", Label: "Feedback", Command: "feedback", Category: "Workspace", Description: "Prepare a redacted bug, feature, or optimization report.", Flags: feedbackFlags()},
		{ID: "analytics", Label: "Analytics", Command: "analytics", Category: "Diagnostics", Description: "View local diagnostics and sharing preference.", Subcommands: []string{"status", "enable", "disable"}, Flags: []FlagSpec{{Name: "json", Value: "bool", Description: "Emit one machine-readable status object."}}},
		{ID: "opencode", Label: "OpenCode", Command: "opencode", Category: "Integrations", Description: "Connect or install the OpenCode integration.", Subcommands: []string{"connect", "status", "disconnect", "plugin"}, Flags: []FlagSpec{{Name: "operation", Value: "string", Default: "status", Description: "Nested plugin operation: install, status, or uninstall."}, {Name: "server", Value: "url", Description: "Explicit loopback server URL."}, {Name: "managed", Value: "bool", Description: "Start a managed local server."}, {Name: "cli", Value: "bool", Description: "Use the OpenCode CLI fallback."}, {Name: "config-dir", Value: "path", Description: "OpenCode configuration directory."}, {Name: "force", Value: "bool", Description: "Replace conflicting integration files."}, {Name: "json", Value: "bool", Description: "Emit machine-readable status for the status subcommand."}}},
		{ID: "hermes", Label: "Hermes", Command: "hermes", Category: "Integrations", Description: "Install or diagnose the native Hermes integration.", Subcommands: []string{"api", "plugin"}, Flags: []FlagSpec{{Name: "operation", Value: "string", Default: "status", Description: "Nested plugin operation: install, status, or uninstall."}, {Name: "home", Value: "path", Description: "Hermes home directory."}, {Name: "force", Value: "bool", Description: "Replace conflicting plugin files."}, {Name: "json", Value: "bool", Description: "Emit the API handshake as JSON."}}},
		{ID: "models", Label: "Models", Command: "models", Category: "Providers", Description: "List models, runtimes, capabilities, and costs.", Flags: []FlagSpec{{Name: "json", Value: "bool", Description: "Emit a stable machine-readable model list."}, {Name: "offline", Value: "bool", Description: "Use built-in and cached metadata without catalog network access."}}},
		{ID: "providers", Label: "Providers", Command: "providers", Category: "Providers", Description: "Show configured providers."},
		{ID: "disable", Label: "Disable", Command: "disable", Category: "Workspace", Description: "Exclude one or more models from routing.", Flags: []FlagSpec{{Name: "model", Value: "string", Required: true, Description: "Model name(s) to exclude, comma-separated."}}},
		{ID: "enable", Label: "Enable", Command: "enable", Category: "Workspace", Description: "Restore one or more models to routing eligibility.", Flags: []FlagSpec{{Name: "model", Value: "string", Required: true, Description: "Model name(s) to re-enable, comma-separated."}}},
		{ID: "version", Label: "Version", Command: "version", Category: "Workspace", Description: "Print the Veto version."},
		{ID: "install-git-hook", Label: "Install git hook", Command: "install-git-hook", Category: "Workspace", Description: "Add Veto to the git workflow.", Flags: []FlagSpec{{Name: "force", Value: "bool", Description: "Overwrite an existing prepare-commit-msg hook."}}},
		{ID: "start", Label: "Start native task", Command: "start", Category: "Execution", Description: "Launch Claude Code or Codex with a transparent manual or experimental choice.", Flags: startFlags()},
		{ID: "unavailable", Label: "Temporary availability", Command: "unavailable", Category: "Providers", Description: "Temporarily exclude a native agent from dispatch.", Flags: []FlagSpec{{Name: "agent", Value: "string", Description: "claude or codex; omit to inspect all entries."}, {Name: "for", Value: "duration", Description: "Duration such as 30m or 2h."}, {Name: "clear", Value: "bool", Description: "Remove the agent's temporary unavailability."}}},
		{ID: "experiment", Label: "Experiment log", Command: "experiment", Category: "Diagnostics", Description: "Inspect or delete local native-dispatch events.", Flags: []FlagSpec{{Name: "clear", Value: "bool", Description: "Delete the local experiment log."}}},
	}}
}

func startFlags() []FlagSpec {
	return []FlagSpec{{Name: "agent", Value: "string", Description: "claude or codex; required for manual/model choice."}, {Name: "choose", Value: "string", Description: "agent or model; omit for manual selection."}, {Name: "model", Value: "string", Description: "Explicit model override where supported."}, {Name: "kind", Value: "string", Description: "Task kind; auto-detected when omitted."}, {Name: "risk", Value: "string", Default: "medium", Description: "Risk level: low, medium, or high."}, {Name: "override-agent", Value: "string", Description: "Override an automatic agent proposal."}, {Name: "override-model", Value: "string", Description: "Override an automatic model proposal."}, {Name: "no-feedback", Value: "bool", Description: "Skip the post-run usefulness prompt."}}
}

func loginFlags() []FlagSpec {
	return []FlagSpec{
		{Name: "provider", Value: "string", Required: true, Description: "anthropic, openai, openrouter, xai, local, or opencode."},
		{Name: "mode", Value: "string", Default: "api-key", Description: "api-key; browser for OpenRouter OAuth; subscription for Anthropic."},
		{Name: "api-key", Value: "string", Description: "Provider key; rendered and handled as secret.", Secret: true},
		{Name: "name", Value: "string", Description: "Routing name for a local model."},
		{Name: "endpoint", Value: "url", Description: "OpenAI-compatible local endpoint."},
		{Name: "model", Value: "string", Description: "Model ID for a local runtime."},
	}
}

// Commands returns a copy so a view cannot mutate the shared catalog.
func (c Catalog) Commands() []ActionSpec {
	commands := make([]ActionSpec, len(c.actions))
	for index, action := range c.actions {
		commands[index] = action
		commands[index].Flags = append([]FlagSpec(nil), action.Flags...)
		commands[index].Subcommands = append([]string(nil), action.Subcommands...)
	}
	return commands
}

// Find returns an action by its stable ID.
func (c Catalog) Find(id string) (ActionSpec, bool) {
	for _, action := range c.actions {
		if action.ID == id {
			action.Flags = append([]FlagSpec(nil), action.Flags...)
			action.Subcommands = append([]string(nil), action.Subcommands...)
			return action, true
		}
	}
	return ActionSpec{}, false
}

func routeFlags() []FlagSpec {
	return []FlagSpec{
		{Name: "task", Value: "string", Description: "Task objective (or positional argument)."},
		{Name: "kind", Value: "string", Description: "Task kind; auto-detected when omitted."},
		{Name: "risk", Value: "string", Default: "medium", Description: "Risk level: low, medium, or high."},
		{Name: "required-tools", Value: "string", Description: "Comma-separated capabilities required by the task."},
		{Name: "requires-executable-tools", Value: "bool", Description: "Require a runtime that exposes executable tools."},
		{Name: "max-cost", Value: "float", Description: "Estimated preflight ceiling in USD."},
		{Name: "timeout", Value: "duration", Default: "30s", Description: "Per-model admission timeout."},
		{Name: "quiet", Value: "bool", Description: "Suppress routing animation."},
		{Name: "json", Value: "bool", Description: "Emit one machine-readable result line."},
		{Name: "no-resume", Value: "bool", Description: "Ignore a saved checkpoint."},
		{Name: "runtime", Value: "string", Description: "Route through one runtime adapter."},
		{Name: "provider", Value: "string", Description: "Route through one configured provider."},
		{Name: "dashboard", Value: "bool", Description: "Open a live routing view in a browser."},
	}
}

func feedbackFlags() []FlagSpec {
	return []FlagSpec{
		{Name: "kind", Value: "string", Required: true, Description: "bug, feature, optimization, or success."},
		{Name: "summary", Value: "string", Description: "Concise report summary."},
		{Name: "expected", Value: "string", Description: "Expected behavior."},
		{Name: "actual", Value: "string", Description: "Actual behavior."},
		{Name: "reproduction", Value: "string", Description: "Reproduction steps or current context."},
		{Name: "scope", Value: "string", Description: "Affected scope and safe environment context."},
		{Name: "acceptance-criteria", Value: "string", Description: "Acceptance criteria separated by newlines or semicolons."},
		{Name: "baseline", Value: "string", Description: "Baseline performance or cost."},
		{Name: "target", Value: "string", Description: "Target performance or cost."},
		{Name: "regression", Value: "string", Description: "Regression assessment."},
		{Name: "evidence", Value: "string", Description: "Safe benchmark or performance evidence."},
		{Name: "risk", Value: "string", Description: "Risk level: low, medium, or high."},
		{Name: "command", Value: "string", Description: "Relevant command name without arguments."},
		{Name: "provider", Value: "string", Description: "Provider/model name, only with explicit consent."},
		{Name: "stdin", Value: "bool", Description: "Read report fields as JSON from stdin."},
		{Name: "json", Value: "bool", Description: "Emit one machine-readable result object."},
		{Name: "include-provider", Value: "bool", Description: "Include provider/model name explicitly."},
		{Name: "no-browser", Value: "bool", Description: "Prepare the issue URL without opening a browser."},
	}
}

func runFlags() []FlagSpec {
	return []FlagSpec{{Name: "task", Value: "string", Description: "Task objective (or positional argument)."}, {Name: "kind", Value: "string", Description: "Task kind; auto-detected when omitted."}, {Name: "risk", Value: "string", Default: "medium", Description: "Risk level: low, medium, or high."}, {Name: "required-tools", Value: "string", Description: "Comma-separated capabilities required by the task."}, {Name: "requires-executable-tools", Value: "bool", Description: "Require a runtime that exposes executable tools."}, {Name: "max-cost", Value: "float", Description: "Estimated preflight ceiling in USD."}, {Name: "timeout", Value: "duration", Default: "2h0m0s", Description: "Total routing and execution timeout."}, {Name: "admission-timeout", Value: "duration", Default: "1m0s", Description: "Timeout for each model admission decision."}, {Name: "quiet", Value: "bool", Description: "Suppress routing pipeline."}, {Name: "criteria", Value: "string", Description: "Comma-separated acceptance criteria."}, {Name: "max-output-tokens", Value: "int", Default: strconv.Itoa(execution.DefaultExecutionMaxTokens), Description: "Maximum output tokens for task execution."}, {Name: "output", Value: "path", Description: "Write task output to a relative file path."}, {Name: "force", Value: "bool", Description: "Overwrite an existing output file."}, {Name: "no-feedback", Value: "bool", Description: "Disable the opt-in post-run feedback prompt."}}
}

func execFlags() []FlagSpec {
	return []FlagSpec{{Name: "quiet", Value: "bool", Description: "Suppress routing pipeline."}, {Name: "dry-run", Value: "bool", Description: "Print steps without executing."}, {Name: "timeout", Value: "duration", Default: "60s", Description: "Per-step timeout."}, {Name: "max-output-tokens", Value: "int", Default: strconv.Itoa(execution.DefaultExecutionMaxTokens), Description: "Maximum output tokens per step."}, {Name: "on-failure", Value: "string", Description: "abort-ask, abort, or continue."}, {Name: "no-feedback", Value: "bool", Description: "Disable the opt-in post-run feedback prompt."}}
}
