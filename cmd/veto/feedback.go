package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	feedbackSchemaVersion = 1
	maxFeedbackURLLength  = 6000
	githubRepository      = "https://github.com/oleg-koval/veto"
	githubLoginURL        = "https://github.com/login"
	githubSignupURL       = "https://github.com/signup"
)

var feedbackPathOverride string

type FeedbackMetadata struct {
	VetoVersion   string `json:"veto_version"`
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	Command       string `json:"command"`
	TaskKind      string `json:"task_kind"`
	Risk          string `json:"risk"`
	ProviderModel string `json:"provider_model,omitempty"`
}

type FeedbackReport struct {
	SchemaVersion       int              `json:"schema_version"`
	Kind                string           `json:"kind"`
	Summary             string           `json:"summary"`
	Reproduction        string           `json:"reproduction_context"`
	ExpectedBehavior    string           `json:"expected_behavior"`
	ActualBehavior      string           `json:"actual_behavior"`
	Scope               string           `json:"scope"`
	AcceptanceCriteria  []string         `json:"acceptance_criteria"`
	BaselinePerformance string           `json:"baseline_performance,omitempty"`
	TargetPerformance   string           `json:"target_performance,omitempty"`
	RegressionStatus    string           `json:"regression_status"`
	Evidence            string           `json:"evidence,omitempty"`
	Metadata            FeedbackMetadata `json:"metadata"`
}

type feedbackCommandResult struct {
	Report         FeedbackReport `json:"report"`
	SavedPath      string         `json:"saved_path"`
	IssueURL       string         `json:"issue_url,omitempty"`
	URLTruncated   bool           `json:"url_truncated"`
	BrowserOpened  bool           `json:"browser_opened"`
	BrowserError   string         `json:"browser_error,omitempty"`
	GitHubAuthNote string         `json:"github_auth_note"`
}

func cmdFeedback(args []string) {
	result, err := runFeedback(args, os.Stdin, os.Stdout, os.Stderr, func(rawURL string) error {
		return openBrowserURL(rawURL)
	})
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "feedback failed:", err)
		os.Exit(1)
	}
	if hasFeedbackFlag(args, "json") {
		_ = json.NewEncoder(os.Stdout).Encode(result)
		return
	}
	if result.SavedPath != "" {
		fmt.Printf("  Saved redacted report: %s\n", result.SavedPath)
	}
	if result.IssueURL != "" {
		fmt.Printf("  GitHub issue URL: %s\n", result.IssueURL)
	}
	if result.URLTruncated {
		fmt.Printf("  The browser payload was shortened; attach the saved report from %s.\n", result.SavedPath)
	}
	if result.BrowserError != "" {
		fmt.Printf("  Browser handoff failed; copy the URL above manually: %s\n", result.BrowserError)
	}
	fmt.Printf("  %s\n", result.GitHubAuthNote)
}

func hasFeedbackFlag(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == "--"+wanted || arg == "--"+wanted+"=true" {
			return true
		}
	}
	return false
}

func runFeedback(args []string, input io.Reader, output, diagnostics io.Writer, browser func(string) error) (feedbackCommandResult, error) {
	fs := flag.NewFlagSet("feedback", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	kind := fs.String("kind", "", "bug|feature|optimization (or success for post-run feedback)")
	summary := fs.String("summary", "", "concise summary")
	reproduction := fs.String("reproduction", "", "reproduction steps or current context")
	expected := fs.String("expected", "", "expected behavior")
	actual := fs.String("actual", "", "actual behavior")
	scope := fs.String("scope", "", "scope and safe environment context")
	criteria := fs.String("acceptance-criteria", "", "acceptance criteria separated by newlines or semicolons")
	baseline := fs.String("baseline", "", "baseline performance or cost")
	target := fs.String("target", "", "target performance or cost")
	regression := fs.String("regression", "", "regression assessment")
	evidence := fs.String("evidence", "", "optional safe benchmark or performance evidence")
	risk := fs.String("risk", "", "risk level: low|medium|high")
	command := fs.String("command", "", "relevant command name, without arguments")
	provider := fs.String("provider", "", "provider/model name to include only with --include-provider")
	includeProvider := fs.Bool("include-provider", false, "confirm that the provider/model name is safe to include")
	stdinJSON := fs.Bool("stdin", false, "read report fields as JSON from stdin")
	jsonOutput := fs.Bool("json", false, "emit one machine-readable result object")
	noBrowser := fs.Bool("no-browser", false, "prepare the URL without opening a browser")
	if err := fs.Parse(args); err != nil {
		return feedbackCommandResult{}, err
	}

	draft := FeedbackReport{}
	if *stdinJSON {
		if err := json.NewDecoder(input).Decode(&draft); err != nil {
			return feedbackCommandResult{}, fmt.Errorf("read stdin JSON: %w", err)
		}
	}
	applyFeedbackFlags(&draft, *kind, *summary, *reproduction, *expected, *actual, *scope, *criteria, *baseline, *target, *regression, *evidence, *risk, *command)
	interactive := !*stdinJSON && !*jsonOutput && feedbackIsTTY()
	if interactive {
		var err error
		draft, err = collectInteractiveFeedback(input, output, draft)
		if err != nil {
			return feedbackCommandResult{}, err
		}
	}
	if draft.Kind == "" {
		return feedbackCommandResult{}, errors.New("kind is required; use --kind or interactive mode")
	}
	if err := validateFeedbackDraft(draft); err != nil {
		return feedbackCommandResult{}, err
	}
	if *includeProvider {
		draft.Metadata.ProviderModel = *provider
	} else {
		draft.Metadata.ProviderModel = ""
	}
	draft = normalizeFeedbackReport(draft)
	savedPath, err := writeFeedbackReport(draft)
	if err != nil {
		return feedbackCommandResult{}, err
	}
	result := feedbackCommandResult{
		Report:         draft,
		SavedPath:      savedPath,
		GitHubAuthNote: "GitHub sign-in is required for attribution; create an account at " + githubSignupURL + " or sign in at " + githubLoginURL + ".",
	}
	if draft.Kind != "success" {
		result.IssueURL, result.URLTruncated = buildFeedbackIssueURL(draft)
		if !*noBrowser && browser != nil {
			if err := browser(result.IssueURL); err != nil {
				result.BrowserError = err.Error()
			} else {
				result.BrowserOpened = true
			}
		}
	}
	return result, nil
}

func applyFeedbackFlags(draft *FeedbackReport, kind, summary, reproduction, expected, actual, scope, criteria, baseline, target, regression, evidence, risk, command string) {
	if kind != "" {
		draft.Kind = kind
	}
	if summary != "" {
		draft.Summary = summary
	}
	if reproduction != "" {
		draft.Reproduction = reproduction
	}
	if expected != "" {
		draft.ExpectedBehavior = expected
	}
	if actual != "" {
		draft.ActualBehavior = actual
	}
	if scope != "" {
		draft.Scope = scope
	}
	if criteria != "" {
		draft.AcceptanceCriteria = splitFeedbackCriteria(criteria)
	}
	if baseline != "" {
		draft.BaselinePerformance = baseline
	}
	if target != "" {
		draft.TargetPerformance = target
	}
	if regression != "" {
		draft.RegressionStatus = regression
	}
	if evidence != "" {
		draft.Evidence = evidence
	}
	if risk != "" {
		draft.Metadata.Risk = risk
	}
	if command != "" {
		draft.Metadata.Command = command
	}
}

func validateFeedbackDraft(report FeedbackReport) error {
	switch report.Kind {
	case "bug", "feature", "optimization", "success":
	default:
		return fmt.Errorf("invalid kind %q: use bug, feature, optimization, or success", report.Kind)
	}
	if strings.TrimSpace(report.Summary) == "" {
		return errors.New("summary is required")
	}
	if report.Kind == "success" {
		return nil
	}
	fields := []struct {
		name  string
		value string
	}{
		{"reproduction", report.Reproduction},
		{"expected", report.ExpectedBehavior},
		{"actual", report.ActualBehavior},
		{"scope", report.Scope},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if len(report.AcceptanceCriteria) == 0 {
		return errors.New("at least one acceptance criterion is required")
	}
	if report.Kind == "optimization" && (strings.TrimSpace(report.BaselinePerformance) == "" || strings.TrimSpace(report.TargetPerformance) == "") {
		return errors.New("optimization reports require baseline and target performance")
	}
	return nil
}

func normalizeFeedbackReport(report FeedbackReport) FeedbackReport {
	report.SchemaVersion = feedbackSchemaVersion
	report.Kind = strings.ToLower(strings.TrimSpace(report.Kind))
	report.Summary = redactFeedbackText(strings.TrimSpace(report.Summary))
	report.Reproduction = redactFeedbackText(strings.TrimSpace(report.Reproduction))
	report.ExpectedBehavior = redactFeedbackText(strings.TrimSpace(report.ExpectedBehavior))
	report.ActualBehavior = redactFeedbackText(strings.TrimSpace(report.ActualBehavior))
	report.Scope = redactFeedbackText(strings.TrimSpace(report.Scope))
	report.BaselinePerformance = redactFeedbackText(strings.TrimSpace(report.BaselinePerformance))
	report.TargetPerformance = redactFeedbackText(strings.TrimSpace(report.TargetPerformance))
	report.RegressionStatus = redactFeedbackText(strings.TrimSpace(report.RegressionStatus))
	report.Evidence = redactFeedbackText(strings.TrimSpace(report.Evidence))
	for i, criterion := range report.AcceptanceCriteria {
		report.AcceptanceCriteria[i] = redactFeedbackText(strings.TrimSpace(criterion))
	}
	report.Metadata = FeedbackMetadata{
		VetoVersion:   version,
		OS:            runtime.GOOS,
		Architecture:  runtime.GOARCH,
		Command:       safeCommandName(report.Metadata.Command),
		TaskKind:      report.Kind,
		Risk:          normalizeRisk(report.Metadata.Risk),
		ProviderModel: redactFeedbackText(strings.TrimSpace(report.Metadata.ProviderModel)),
	}
	return report
}

func redactFeedbackReport(report FeedbackReport) FeedbackReport {
	redacted := normalizeFeedbackReport(report)
	redacted.Metadata.ProviderModel = ""
	return redacted
}

func normalizeRisk(risk string) string {
	risk = strings.ToLower(strings.TrimSpace(risk))
	if risk == "low" || risk == "medium" || risk == "high" {
		return risk
	}
	return "medium"
}

func safeCommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "feedback"
	}
	if fields := strings.Fields(command); len(fields) > 0 {
		return filepath.Base(strings.Trim(fields[0], "\"'"))
	}
	return "feedback"
}

var (
	secretValuePattern      = regexp.MustCompile(`(?i)(?:sk-ant-|sk-or-|sk-|xai-|ghp_|github_pat_|bearer\s+)[A-Za-z0-9._-]+`)
	secretAssignmentPattern = regexp.MustCompile(`(?i)\b(?:api[_-]?key|token|password|secret)\s*[:=]\s*[^\s,;]+`)
	unixPathPattern         = regexp.MustCompile(`(?:/Users/[^\s"']+|/home/[^\s"']+)`)
	windowsPathPattern      = regexp.MustCompile(`[A-Za-z]:\\Users\\[^\s"']+`)
	vetoPathPattern         = regexp.MustCompile(`~/.veto[^\s"']*`)
)

func redactFeedbackText(text string) string {
	text = secretValuePattern.ReplaceAllString(text, "[redacted]")
	text = secretAssignmentPattern.ReplaceAllString(text, "[redacted]")
	text = unixPathPattern.ReplaceAllString(text, "[local path redacted]")
	text = windowsPathPattern.ReplaceAllString(text, "[local path redacted]")
	text = vetoPathPattern.ReplaceAllString(text, "[veto state redacted]")
	return text
}

func collectInteractiveFeedback(input io.Reader, output io.Writer, draft FeedbackReport) (FeedbackReport, error) {
	reader := bufio.NewReader(input)
	if draft.Kind == "success" {
		if strings.TrimSpace(draft.Summary) == "" {
			var err error
			draft.Summary, err = readFeedbackPrompt(reader, output, "  What worked well? ")
			if err != nil {
				return draft, err
			}
		}
		draft = redactFeedbackReport(draft)
		fmt.Fprintln(output, "\n  Redacted report preview:")
		printFeedbackPreview(output, draft)
		fmt.Fprint(output, "  Submit this redacted report? [y/N]: ")
		answer, err := readFeedbackLine(reader)
		if err != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return draft, errors.New("submission cancelled")
		}
		return draft, nil
	}
	if draft.Kind == "" {
		fmt.Fprintln(output, "  Feedback type: 1 bug  2 feature  3 optimization")
		choice, err := readFeedbackPrompt(reader, output, "  Type [1-3]: ")
		if err != nil {
			return draft, err
		}
		draft.Kind = map[string]string{"1": "bug", "2": "feature", "3": "optimization"}[strings.TrimSpace(choice)]
		if draft.Kind == "" {
			return draft, errors.New("choose bug, feature, or optimization")
		}
	}
	var err error
	fields := []struct {
		prompt string
		target *string
	}{
		{"  Summary: ", &draft.Summary},
		{"  Reproduction/context: ", &draft.Reproduction},
		{"  Expected behavior: ", &draft.ExpectedBehavior},
		{"  Actual behavior: ", &draft.ActualBehavior},
		{"  Scope/environment: ", &draft.Scope},
		{"  Acceptance criteria (one line, or separate with ;): ", nil},
	}
	for _, field := range fields {
		prompt, target := field.prompt, field.target
		if target != nil && strings.TrimSpace(*target) != "" {
			continue
		}
		value, readErr := readFeedbackPrompt(reader, output, prompt)
		if readErr != nil {
			return draft, readErr
		}
		if target != nil {
			*target = value
		} else {
			draft.AcceptanceCriteria = splitFeedbackCriteria(value)
		}
	}
	if strings.TrimSpace(draft.BaselinePerformance) == "" && draft.Kind == "optimization" {
		draft.BaselinePerformance, err = readFeedbackPrompt(reader, output, "  Baseline performance: ")
		if err != nil {
			return draft, err
		}
		draft.TargetPerformance, err = readFeedbackPrompt(reader, output, "  Target performance: ")
		if err != nil {
			return draft, err
		}
	}
	if strings.TrimSpace(draft.RegressionStatus) == "" {
		draft.RegressionStatus, err = readFeedbackPrompt(reader, output, "  Regression assessment: ")
		if err != nil {
			return draft, err
		}
	}
	draft = redactFeedbackReport(draft)
	fmt.Fprintln(output, "\n  Redacted report preview:")
	printFeedbackPreview(output, draft)
	fmt.Fprint(output, "  Submit this redacted report? [y/N]: ")
	answer, err := readFeedbackLine(reader)
	if err != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return draft, errors.New("submission cancelled")
	}
	return draft, nil
}

func readFeedbackPrompt(input *bufio.Reader, output io.Writer, prompt string) (string, error) {
	fmt.Fprint(output, prompt)
	return readFeedbackLine(input)
}

func readFeedbackLine(input *bufio.Reader) (string, error) {
	line, err := input.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func splitFeedbackCriteria(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ';' })
	criteria := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			criteria = append(criteria, value)
		}
	}
	return criteria
}

func printFeedbackPreview(w io.Writer, report FeedbackReport) {
	b, _ := json.MarshalIndent(report, "  ", "  ")
	fmt.Fprintf(w, "  %s\n", b)
}

func feedbackIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func postRunFeedbackEnabled() bool {
	data, err := os.ReadFile(vetoCfgPath())
	if err != nil {
		return false
	}
	var cfg struct {
		PostRunFeedback bool `json:"post_run_feedback"`
	}
	return json.Unmarshal(data, &cfg) == nil && cfg.PostRunFeedback
}

func maybeOfferPostRunFeedback(command, risk, providerModel string) {
	if !postRunFeedbackEnabled() || !feedbackIsTTY() {
		return
	}
	fmt.Fprintln(os.Stderr, "\n  Share feedback about this run? 1 success  2 bug  3 feature  4 optimization  5 skip")
	fmt.Fprint(os.Stderr, "  Choice [1-5]: ")
	var choice string
	if _, err := fmt.Scanln(&choice); err != nil {
		return
	}
	kind := map[string]string{"1": "success", "2": "bug", "3": "feature", "4": "optimization"}[strings.TrimSpace(choice)]
	if kind == "" {
		return
	}
	args := []string{"--kind", kind, "--command", command, "--risk", risk}
	if providerModel != "" {
		fmt.Fprint(os.Stderr, "  Include the provider/model name? [y/N]: ")
		var include string
		if _, err := fmt.Scanln(&include); err == nil && strings.ToLower(strings.TrimSpace(include)) == "y" {
			args = append(args, "--provider", providerModel, "--include-provider")
		}
	}
	result, err := runFeedback(args, os.Stdin, os.Stdout, os.Stderr, openBrowserURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  Feedback skipped:", err)
		return
	}
	fmt.Fprintf(os.Stderr, "  Saved redacted feedback report: %s\n", result.SavedPath)
}

func feedbackPath() string {
	if feedbackPathOverride != "" {
		return feedbackPathOverride
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".veto", "feedback")
}

func writeFeedbackReport(report FeedbackReport) (string, error) {
	dir := feedbackPath()
	if filepath.Ext(dir) == ".json" { // test-friendly explicit path override
		if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
			return "", err
		}
		return writePrivateFeedbackFile(dir, report)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create feedback directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", fmt.Errorf("protect feedback directory: %w", err)
	}
	name := fmt.Sprintf("%s-%s.json", time.Now().UTC().Format("20060102T150405.000000000Z"), feedbackSlug(report.Summary))
	return writePrivateFeedbackFile(filepath.Join(dir, name), report)
}

func writePrivateFeedbackFile(path string, report FeedbackReport) (string, error) {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("create report: %w", err)
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return "", err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

func feedbackSlug(summary string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(summary) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 48 {
		slug = slug[:48]
	}
	if slug == "" {
		return "report"
	}
	return slug
}

func buildFeedbackIssueURL(report FeedbackReport) (string, bool) {
	template := map[string]string{"bug": "bug_report.yml", "feature": "feature_request.yml", "optimization": "optimization_proposal.yml"}[report.Kind]
	label := map[string]string{"bug": "bug", "feature": "enhancement", "optimization": "performance"}[report.Kind]
	values := url.Values{}
	values.Set("template", template)
	values.Set("title", "["+feedbackTitleKind(report.Kind)+"] "+clipFeedbackText(report.Summary, 120))
	values.Set("labels", label)
	values.Set("body", feedbackIssueBody(report))
	issueURL := githubRepository + "/issues/new?" + values.Encode()
	if len(issueURL) <= maxFeedbackURLLength {
		return issueURL, false
	}
	values.Set("body", "The full redacted report was saved locally. Please attach it after opening this issue.\n\nSummary: "+clipFeedbackText(report.Summary, 240)+"\nScope: "+clipFeedbackText(report.Scope, 240))
	return githubRepository + "/issues/new?" + values.Encode(), true
}

func clipFeedbackText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit-3] + "..."
}

func feedbackTitleKind(kind string) string {
	return map[string]string{"bug": "Bug", "feature": "Feature", "optimization": "Optimization"}[kind]
}

func feedbackIssueBody(report FeedbackReport) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "### Summary\n%s\n\n", report.Summary)
	fmt.Fprintf(&b, "### Reproduction or context\n%s\n\n", report.Reproduction)
	fmt.Fprintf(&b, "### Expected behavior\n%s\n\n", report.ExpectedBehavior)
	fmt.Fprintf(&b, "### Actual behavior\n%s\n\n", report.ActualBehavior)
	fmt.Fprintf(&b, "### Scope and environment\n%s\n\n", report.Scope)
	if report.BaselinePerformance != "" {
		fmt.Fprintf(&b, "### Baseline performance\n%s\n\n", report.BaselinePerformance)
	}
	if report.TargetPerformance != "" {
		fmt.Fprintf(&b, "### Target performance\n%s\n\n", report.TargetPerformance)
	}
	fmt.Fprintln(&b, "### Acceptance criteria")
	for _, criterion := range report.AcceptanceCriteria {
		fmt.Fprintf(&b, "- [ ] %s\n", criterion)
	}
	fmt.Fprintf(&b, "\n### Regression status\n%s\n", report.RegressionStatus)
	if report.Evidence != "" {
		fmt.Fprintf(&b, "\n### Evidence\n%s\n", report.Evidence)
	}
	return b.String()
}
