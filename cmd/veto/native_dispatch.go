package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/pkg/dispatch"
	"github.com/oleg-koval/veto/pkg/executor"
	"github.com/oleg-koval/veto/pkg/ledger"
)

func cmdStart(args []string) int {
	return runStart(args, os.Stdin, os.Stdout, os.Stderr, executor.NewNativeLauncher(), dispatch.NewAvailabilityStore(availabilityPath()))
}

func runStart(args []string, input io.Reader, output, diagnostics io.Writer, launcher *executor.NativeLauncher, availability *dispatch.AvailabilityStore) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	agent := fs.String("agent", "", "native agent: claude or codex")
	choose := fs.String("choose", "", "experimental choice: agent or model")
	model := fs.String("model", "", "explicit model where supported")
	kind := fs.String("kind", "", "task kind; inferred when omitted")
	risk := fs.String("risk", "medium", "risk level: low, medium, or high")
	overrideAgent := fs.String("override-agent", "", "override an automatic agent proposal")
	overrideModel := fs.String("override-model", "", "override an automatic model proposal")
	noFeedback := fs.Bool("no-feedback", false, "skip the post-run usefulness prompt")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	objective := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if objective == "" {
		fmt.Fprintln(diagnostics, "error: provide a task objective")
		return 2
	}
	mode := dispatch.ModeManual
	switch strings.ToLower(strings.TrimSpace(*choose)) {
	case "":
	case "agent":
		mode = dispatch.ModeChooseAgent
	case "model":
		mode = dispatch.ModeChooseModel
	default:
		fmt.Fprintln(diagnostics, "error: --choose must be agent or model")
		return 2
	}
	if *kind == "" {
		*kind = inferKind(objective)
	}
	statuses := nativeAgentStatuses(availability)
	request := dispatch.Request{Mode: mode, Agent: *agent, Model: *model, Kind: *kind, Risk: *risk}
	proposal, err := dispatch.Decide(request, statuses, nativeModels())
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return 1
	}
	if proposal.Agent == "" {
		fmt.Fprintln(diagnostics, "error: no native agent selected")
		return 1
	}
	runID, _ := ledger.NewRunID()
	if runID == "" {
		runID = currentRunID("native-dispatch")
	}
	printDecision(output, proposal)
	final := proposal
	requestedOverrideAgent := strings.TrimSpace(*overrideAgent)
	if mode == dispatch.ModeChooseAgent && requestedOverrideAgent == "" && strings.TrimSpace(*agent) != "" {
		requestedOverrideAgent = *agent
	}
	requestedOverrideModel := strings.TrimSpace(*overrideModel)
	if mode == dispatch.ModeChooseModel && requestedOverrideModel == "" && strings.TrimSpace(*model) != "" {
		requestedOverrideModel = *model
	}
	if requestedOverrideAgent != "" || requestedOverrideModel != "" {
		final, err = dispatch.Decide(dispatch.Request{Mode: dispatch.ModeManual, Agent: valueOr(proposal.Agent, requestedOverrideAgent), Model: valueOr(proposal.Model, requestedOverrideModel), Kind: *kind, Risk: *risk}, statuses, nativeModels())
		if err != nil {
			fmt.Fprintln(diagnostics, "error: override:", err)
			return 1
		}
		final.Reason = "the user overrode Veto's proposal immediately"
		final.Constraints = append(final.Constraints, "automatic proposal overridden")
		logNativeEventWithRun(runID, ledger.EventChoiceOverridden, proposal, final, true, final.Explanation())
		fmt.Fprintln(output, "Override: "+final.Explanation())
	}

	logNativeEventWithRun(runID, ledger.EventLaunchRequested, proposal, final, false, "native dispatch requested")
	logNativeEventWithRun(runID, ledger.EventChoiceProposed, proposal, final, false, proposal.Explanation())
	cmd, err := launcher.Command(context.Background(), final.Agent, final.Model, objective)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return 1
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, output, diagnostics
	started := time.Now()
	logNativeEventWithRun(runID, ledger.EventNativeStarted, proposal, final, false, "startup")
	err = cmd.Run()
	logNativeEventWithRun(runID, ledger.EventNativeExited, proposal, final, false, fmt.Sprintf("duration_ms=%d exit=%d", time.Since(started).Milliseconds(), processExitCode(err)))
	if err != nil {
		return processExitCode(err)
	}
	if !*noFeedback && isInteractiveTerminal(os.Stdin) && isInteractiveTerminal(os.Stdout) {
		if outcome := askNativeOutcome(input, output); outcome != "" {
			logNativeEventWithOutcome(runID, proposal, final, outcome)
		}
	}
	return 0
}

func nativeModels() []dispatch.Model {
	return []dispatch.Model{{Agent: "claude", Name: "haiku"}, {Agent: "claude", Name: "sonnet"}, {Agent: "claude", Name: "opus"}}
}

func printDecision(w io.Writer, decision dispatch.Decision) {
	fmt.Fprintln(w, "Veto proposal: "+decision.Explanation())
	if len(decision.Constraints) > 0 {
		fmt.Fprintln(w, "  constraints: "+strings.Join(decision.Constraints, "; "))
	}
	if len(decision.AlternativesExcluded) > 0 {
		fmt.Fprintln(w, "  alternatives excluded: "+strings.Join(decision.AlternativesExcluded, "; "))
	}
	if len(decision.Unknown) > 0 {
		fmt.Fprintln(w, "  unknown: "+strings.Join(decision.Unknown, "; "))
	}
	fmt.Fprintln(w, "  historical data: not used")
}

func valueOr(primary, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return primary
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}

func askNativeOutcome(input io.Reader, output io.Writer) string {
	fmt.Fprintln(output, "\nWas this dispatch useful? [y] yes [n] no [s] skip")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return "yes"
	case "n", "no":
		return "no"
	default:
		return "skip"
	}
}

func logNativeEventWithRun(runID string, eventType ledger.EventType, proposal, final dispatch.Decision, overridden bool, detail string) {
	setupExperimentLogger()
	if experimentLedger == nil {
		return
	}
	event := ledger.Event{RunID: runID, Type: eventType, Mode: string(proposal.Mode), ProposedAgent: proposal.Agent, FinalAgent: final.Agent, ProposedModel: proposal.Model, FinalModel: final.Model, Override: overridden, Detail: detail}
	_ = experimentLedger.Append(event)
}

func logNativeEventWithOutcome(runID string, proposal, final dispatch.Decision, outcome string) {
	setupExperimentLogger()
	if experimentLedger == nil {
		return
	}
	_ = experimentLedger.Append(ledger.Event{RunID: runID, Type: ledger.EventOutcomeReported, Mode: string(proposal.Mode), ProposedAgent: proposal.Agent, FinalAgent: final.Agent, ProposedModel: proposal.Model, FinalModel: final.Model, Outcome: outcome})
}

func availabilityPath() string {
	home, _ := os.UserHomeDir()
	return home + string(os.PathSeparator) + ".veto" + string(os.PathSeparator) + "unavailable.json"
}

var experimentLedger *ledger.Writer
var experimentLoggerMu sync.Mutex

func experimentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "experiment.log")
}

func setupExperimentLogger() {
	experimentLoggerMu.Lock()
	defer experimentLoggerMu.Unlock()
	if experimentLedger != nil {
		return
	}
	path := experimentPath()
	if info, err := os.Stat(path); err == nil && info.Size() > 256*1024 {
		_ = os.Remove(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		experimentLedger = ledger.NewWriter(io.Discard)
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		experimentLedger = ledger.NewWriter(io.Discard)
		return
	}
	_ = file.Chmod(0600)
	experimentLedger = ledger.NewWriter(file)
}

func nativeAgentStatuses(availability *dispatch.AvailabilityStore) []dispatch.AgentStatus {
	creds, credentialErr := loadCredentials()
	apiKey := os.Getenv("ANTHROPIC_API_KEY") != "" || creds["ANTHROPIC_API_KEY"] != ""
	subscription := os.Getenv("CLAUDE_SUBSCRIPTION") == "true" || creds["CLAUDE_SUBSCRIPTION"] == "true"
	claude := dispatch.AgentStatus{Name: "claude", Installed: executableAvailable("claude"), Auth: dispatch.AuthUnknown, Billing: dispatch.BillingUnknown}
	if apiKey {
		claude.Auth = dispatch.AuthAuthenticated
		if !subscription {
			claude.Billing = dispatch.BillingAPI
		}
	}
	if subscription {
		claude.Warning = "Claude billing is UNKNOWN: native CLI subscription/API selection cannot be verified"
		if apiKey {
			claude.Warning = "Claude billing is UNKNOWN: CLAUDE_SUBSCRIPTION and ANTHROPIC_API_KEY are both present"
		}
	}
	if credentialErr != nil {
		claude.Warning = "credentials file could not be read; authentication/billing may be incomplete"
	}
	codex := dispatch.AgentStatus{Name: "codex", Installed: executableAvailable("codex"), Auth: dispatch.AuthUnauthenticated, Billing: dispatch.BillingUnknown}
	if auth := codexCLIAuthentication(); auth != codexAuthNone {
		codex.Auth = dispatch.AuthAuthenticated
		switch auth {
		case codexAuthChatGPT:
			codex.Warning = "Codex billing/capacity is UNKNOWN; ChatGPT plan status is not cost evidence"
		case codexAuthAPIKey:
			codex.Billing = dispatch.BillingAPI
		}
	}
	entries, availabilityErr := availability.List()
	if availabilityErr != nil {
		warning := "availability state could not be read; temporary unavailability may be ignored"
		claude.Warning = joinWarnings(claude.Warning, warning)
		codex.Warning = joinWarnings(codex.Warning, warning)
	} else {
		for _, entry := range entries {
			switch entry.Agent {
			case "claude":
				claude.Unavailable = true
			case "codex":
				codex.Unavailable = true
			}
		}
	}
	return []dispatch.AgentStatus{claude, codex}
}

func joinWarnings(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + "; " + second
}

func executableAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

type execNativeCommand struct{ *exec.Cmd }

func (c *execNativeCommand) SetStdin(reader io.Reader)  { c.Stdin = reader }
func (c *execNativeCommand) SetStdout(writer io.Writer) { c.Stdout = writer }
func (c *execNativeCommand) SetStderr(writer io.Writer) { c.Stderr = writer }

var _ controlplane.NativeCommand = (*execNativeCommand)(nil)

func runTUIStart(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	objective := strings.TrimSpace(request.Arguments["objective"])
	if objective == "" {
		return controlplane.ActionResult{ActionID: "start"}, fmt.Errorf("objective is required")
	}
	proposal, final, err := nativeProposal(request.Arguments, nativeAgentStatuses(dispatch.NewAvailabilityStore(availabilityPath())))
	if err != nil {
		return controlplane.ActionResult{ActionID: "start"}, err
	}
	launcher := executor.NewNativeLauncher()
	command, err := launcher.Command(context.Background(), final.Agent, final.Model, objective)
	if err != nil {
		return controlplane.ActionResult{ActionID: "start"}, err
	}
	runID, _ := ledger.NewRunID()
	if runID == "" {
		runID = currentRunID("native-dispatch")
	}
	logNativeEventWithRun(runID, ledger.EventLaunchRequested, proposal, final, proposal.Agent != final.Agent || proposal.Model != final.Model, "native dispatch requested")
	logNativeEventWithRun(runID, ledger.EventChoiceProposed, proposal, final, false, proposal.Explanation())
	if proposal.Agent != final.Agent || proposal.Model != final.Model {
		logNativeEventWithRun(runID, ledger.EventChoiceOverridden, proposal, final, true, final.Explanation())
	}
	return controlplane.ActionResult{ActionID: "start", Summary: nativeDecisionSummary(proposal, final), Model: final.Agent, Command: &loggedNativeCommand{command: &execNativeCommand{Cmd: command}, runID: runID, proposal: proposal, final: final}}, nil
}

func nativeProposal(arguments map[string]string, statuses []dispatch.AgentStatus) (dispatch.Decision, dispatch.Decision, error) {
	choose := strings.ToLower(strings.TrimSpace(arguments["choose"]))
	mode := dispatch.ModeManual
	switch choose {
	case "":
	case "agent":
		mode = dispatch.ModeChooseAgent
	case "model":
		mode = dispatch.ModeChooseModel
	default:
		return dispatch.Decision{}, dispatch.Decision{}, fmt.Errorf("choose must be agent or model")
	}
	kind := arguments["kind"]
	if kind == "" {
		kind = inferKind(arguments["objective"])
	}
	proposalRequest := dispatch.Request{Mode: mode, Agent: arguments["agent"], Kind: kind, Risk: arguments["risk"]}
	if mode == dispatch.ModeManual {
		proposalRequest.Model = arguments["model"]
	}
	proposal, err := dispatch.Decide(proposalRequest, statuses, nativeModels())
	if err != nil {
		return dispatch.Decision{}, dispatch.Decision{}, err
	}
	overrideAgent := arguments["override-agent"]
	if mode == dispatch.ModeChooseAgent && overrideAgent == "" {
		overrideAgent = arguments["agent"]
	}
	overrideModel := arguments["override-model"]
	if mode == dispatch.ModeChooseModel && overrideModel == "" {
		overrideModel = arguments["model"]
	}
	if overrideAgent == "" && overrideModel == "" {
		return proposal, proposal, nil
	}
	final, err := dispatch.Decide(dispatch.Request{Mode: dispatch.ModeManual, Agent: valueOr(proposal.Agent, overrideAgent), Model: valueOr(proposal.Model, overrideModel), Kind: kind, Risk: arguments["risk"]}, statuses, nativeModels())
	if err != nil {
		return dispatch.Decision{}, dispatch.Decision{}, err
	}
	final.Reason = "the user overrode Veto's proposal immediately"
	return proposal, final, nil
}

func nativeDecisionSummary(proposal, final dispatch.Decision) string {
	var b strings.Builder
	b.WriteString("Proposal: ")
	b.WriteString(proposal.Explanation())
	b.WriteString("\n")
	if len(proposal.Constraints) > 0 {
		b.WriteString("Constraints: ")
		b.WriteString(strings.Join(proposal.Constraints, "; "))
		b.WriteString("\n")
	}
	if len(proposal.AlternativesExcluded) > 0 {
		b.WriteString("Excluded: ")
		b.WriteString(strings.Join(proposal.AlternativesExcluded, "; "))
		b.WriteString("\n")
	}
	if len(proposal.Unknown) > 0 {
		b.WriteString("Unknown: ")
		b.WriteString(strings.Join(proposal.Unknown, "; "))
		b.WriteString("\n")
	}
	b.WriteString("History: not used")
	if proposal.Agent != final.Agent || proposal.Model != final.Model {
		b.WriteString("\nOverride: ")
		b.WriteString(final.Explanation())
	}
	return b.String()
}

type loggedNativeCommand struct {
	command  controlplane.NativeCommand
	runID    string
	proposal dispatch.Decision
	final    dispatch.Decision
}

func (c *loggedNativeCommand) Run() error {
	started := time.Now()
	logNativeEventWithRun(c.runID, ledger.EventNativeStarted, c.proposal, c.final, false, "startup")
	err := c.command.Run()
	logNativeEventWithRun(c.runID, ledger.EventNativeExited, c.proposal, c.final, false, fmt.Sprintf("duration_ms=%d exit=%d", time.Since(started).Milliseconds(), processExitCode(err)))
	return err
}

func (c *loggedNativeCommand) SetStdin(reader io.Reader)  { c.command.SetStdin(reader) }
func (c *loggedNativeCommand) SetStdout(writer io.Writer) { c.command.SetStdout(writer) }
func (c *loggedNativeCommand) SetStderr(writer io.Writer) { c.command.SetStderr(writer) }

func runTUIUnavailable(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	var output, diagnostics strings.Builder
	args := make([]string, 0, 3)
	if agent := request.Arguments["agent"]; agent != "" {
		args = append(args, agent)
	}
	if request.Arguments["for"] != "" {
		args = append(args, "--for", request.Arguments["for"])
	}
	if request.Arguments["clear"] == "true" {
		args = append(args, "--clear")
	}
	code := runUnavailable(args, &output, &diagnostics, dispatch.NewAvailabilityStore(availabilityPath()))
	if code != 0 {
		return controlplane.ActionResult{ActionID: "unavailable", Output: output.String()}, errors.New(strings.TrimSpace(diagnostics.String()))
	}
	return controlplane.ActionResult{ActionID: "unavailable", Summary: "availability inspected", Output: output.String()}, nil
}

func runTUIExperiment(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	args := []string{}
	if request.Arguments["clear"] == "true" {
		args = append(args, "--clear")
	}
	var output, diagnostics strings.Builder
	if code := runExperiment(args, &output, &diagnostics); code != 0 {
		return controlplane.ActionResult{ActionID: "experiment", Output: output.String()}, errors.New(strings.TrimSpace(diagnostics.String()))
	}
	return controlplane.ActionResult{ActionID: "experiment", Summary: "experiment log inspected", Output: output.String()}, nil
}
