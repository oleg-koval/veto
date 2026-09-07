package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/internal/eval"
	"github.com/oleg-koval/veto/internal/tui"
	opencodert "github.com/oleg-koval/veto/pkg/opencode"
	"github.com/oleg-koval/veto/pkg/router"
)

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	reduceMotion := fs.Bool("reduce-motion", false, "disable non-essential animation")
	noColor := fs.Bool("no-color", false, "disable styling and ANSI colors")
	noMouse := fs.Bool("no-mouse", false, "disable mouse reporting")
	screenReader := fs.Bool("screen-reader", false, "use a stable text-only layout for assistive technology")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("tui does not accept positional arguments")
	}
	// The TUI runs the same router and executor as the CLI, so it must also
	// initialize the local ledger. Without this, a failed interactive mission
	// leaves no attributable routing or execution evidence to diagnose.
	setupLogger()
	runningExecutable, _ := os.Executable()

	model := tui.NewModel(controlplane.DefaultCatalog(), tui.Options{
		Motion:       !*reduceMotion && !*screenReader,
		NoColor:      *noColor || *screenReader || os.Getenv("NO_COLOR") != "",
		Mouse:        !*noMouse && !*screenReader,
		ScreenReader: *screenReader,
		Version:      resolvedVersion(),
		Executable:   runningExecutable,
		ServiceFactory: func() (controlplane.Service, error) {
			reg, mgr, store, err := prepareTUIRouting()
			if err != nil {
				return nil, fmt.Errorf("prepare routing: %w", err)
			}
			service := application.NewControlServiceWithSnapshot(newApplicationRunner(reg, mgr), mgr, loadTUISnapshot)
			service.SetOutputWriter(writeOutputFile)
			service.SetHistorySaver(store.Save)
			service.SetRouteEventRecorder(func(event router.ProgressEvent) {
				logEvent("", "", "", event)
			})
			service.SetSkillResolver(func(ctx context.Context, task router.TaskSpec) []string {
				_, bodies := resolveSkills(ctx, reg, mgr, task)
				return bodies
			})
			service.SetReviewer(func(ctx context.Context, task router.TaskSpec, output, model string) (bool, error) {
				result, err := reviewOutput(ctx, reg, mgr, task, output, model)
				return result.Passed, err
			})
			service.RegisterHandler("doctor", func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				return runTUIDoctor(ctx, request)
			})
			service.RegisterHandler("benchmark", func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				corpus := request.Arguments["corpus"]
				if corpus == "" {
					corpus = defaultBenchmarkCorpus
				}
				loaded, loadErr := eval.LoadFile(corpus)
				if loadErr != nil {
					return controlplane.ActionResult{ActionID: "benchmark"}, loadErr
				}
				var output strings.Builder
				if writeErr := eval.WriteJSON(&output, eval.Evaluate(loaded)); writeErr != nil {
					return controlplane.ActionResult{ActionID: "benchmark"}, writeErr
				}
				return controlplane.ActionResult{ActionID: "benchmark", Summary: "benchmark complete", Output: output.String()}, nil
			})
			service.RegisterHandler("version", func(context.Context, controlplane.ActionRequest) (controlplane.ActionResult, error) {
				summary := "veto " + resolvedVersion()
				return controlplane.ActionResult{ActionID: "version", Summary: summary, Output: summary}, nil
			})
			service.SetRoutingRefresher(func() error {
				return refreshTUIRouting(reg, mgr)
			})
			registerTUIActionHandlers(service, func() error {
				return refreshTUIRouting(reg, mgr)
			})
			service.RegisterHandler("setup", runTUISetup)
			service.RegisterHandler("exec", func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
				return runTUIExec(ctx, request, service, reg, mgr)
			})
			return tuiRunLoggingService{Service: service}, nil
		},
	})
	_, err := tea.NewProgram(model).Run()
	return err
}

type tuiRunLoggingService struct {
	controlplane.Service
}

func (s tuiRunLoggingService) Execute(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	if request.ActionID == "run" || request.ActionID == "route" || request.ActionID == "exec" {
		runID := beginLoggedRun()
		kind := request.Arguments["kind"]
		objective := request.Arguments["objective"]
		if request.ActionID == "exec" {
			kind = "exec"
			objective = "Plan: " + request.Arguments["plan"]
		}
		_ = saveTUIMission(tuiMissionRecord{
			RunID: runID, TaskID: request.Arguments["task-id"], Kind: kind,
			Risk: request.Arguments["risk"], CreatedAt: time.Now(), Objective: objective,
		})
	}
	return s.Service.Execute(ctx, request)
}

// registerTUIActionHandlers keeps command-specific parsing in the existing
// CLI functions while giving the TUI a real, redacted execution path. Actions
// that mutate credentials or integration files are invoked only after the
// model's explicit confirmation overlay.
func registerTUIActionHandlers(service *application.ControlService, refreshPreferences func() error) {
	service.RegisterHandler("login", func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		result, err := runTUILogin(ctx, request)
		if err == nil && refreshPreferences != nil {
			if refreshErr := refreshPreferences(); refreshErr != nil {
				return result, fmt.Errorf("refresh routing after login: %w", refreshErr)
			}
		}
		return result, err
	})
	service.RegisterHandler("logout", func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		result, err := runTUILogout(ctx, request)
		if err == nil && refreshPreferences != nil {
			if refreshErr := refreshPreferences(); refreshErr != nil {
				return result, fmt.Errorf("refresh routing after logout: %w", refreshErr)
			}
		}
		return result, err
	})
	service.RegisterHandler("disable", func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		result, err := runTUIDisable(ctx, request)
		if err == nil && refreshPreferences != nil {
			if refreshErr := refreshPreferences(); refreshErr != nil {
				return result, fmt.Errorf("refresh routing after disable: %w", refreshErr)
			}
		}
		return result, err
	})
	service.RegisterHandler("enable", func(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		result, err := runTUIEnable(ctx, request)
		if err == nil && refreshPreferences != nil {
			if refreshErr := refreshPreferences(); refreshErr != nil {
				return result, fmt.Errorf("refresh routing after enable: %w", refreshErr)
			}
		}
		return result, err
	})
	service.RegisterHandler("install-git-hook", runTUIInstallGitHook)
	service.RegisterHandler("analytics", func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		subcommand := request.Arguments["subcommand"]
		if subcommand == "" {
			subcommand = "status"
		}
		if subcommand != "status" && subcommand != "enable" && subcommand != "disable" {
			return controlplane.ActionResult{ActionID: "analytics"}, fmt.Errorf("unsupported analytics operation %q", subcommand)
		}
		args := append([]string{subcommand}, tuiFlagArguments(request)...)
		return runTUICommand("analytics", args, func(arguments []string, output, diagnostics *strings.Builder) int {
			return runAnalyticsCommand(arguments, output, diagnostics)
		})
	})
	service.RegisterHandler("opencode", func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		subcommand := request.Arguments["subcommand"]
		if subcommand == "" {
			subcommand = "status"
		}
		args := []string{subcommand}
		if subcommand == "plugin" {
			operation := request.Arguments["operation"]
			if operation == "" {
				operation = "status"
			}
			args = append(args, operation)
			args = append(args, tuiFlagArgumentsFor(request, "config-dir", "force")...)
		} else if subcommand == "connect" {
			args = append(args, tuiFlagArgumentsFor(request, "server", "managed", "cli")...)
		} else if subcommand == "status" && request.Arguments["json"] == "true" {
			args = append(args, "--json")
		}
		return runTUICommand("opencode", args, func(arguments []string, output, diagnostics *strings.Builder) int {
			return runOpenCodeCommand(arguments, output, diagnostics, opencodert.DefaultDependencies(), vetoCfgPath())
		})
	})
	service.RegisterHandler("hermes", func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		subcommand := request.Arguments["subcommand"]
		if subcommand == "" {
			subcommand = "api"
		}
		args := []string{subcommand}
		if subcommand == "plugin" {
			operation := request.Arguments["operation"]
			if operation == "" {
				operation = "status"
			}
			args = append(args, operation)
			args = append(args, tuiFlagArgumentsFor(request, "home", "force")...)
		} else if subcommand == "api" && request.Arguments["json"] == "true" {
			args = append(args, "--json")
		}
		return runTUICommand("hermes", args, func(arguments []string, output, diagnostics *strings.Builder) int {
			return runHermesCommand(arguments, output, diagnostics)
		})
	})
	service.RegisterHandler("impeccable", func(ctx context.Context, _ controlplane.ActionRequest) (controlplane.ActionResult, error) {
		return runTUIImpeccableInstall(ctx, exec.LookPath, runTUIExternalCommand)
	})
	service.RegisterHandler("feedback", runTUIFeedback)
	service.RegisterHandler("verify-models", runTUIVerifyModels)
	service.RegisterHandler("models", runTUIModels)
	service.RegisterHandler("providers", func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		var output strings.Builder
		if code := runProvidersCommand(&output); code != 0 {
			return controlplane.ActionResult{ActionID: "providers", Output: output.String()}, fmt.Errorf("providers exited with status %d", code)
		}
		return controlplane.ActionResult{ActionID: "providers", Summary: "providers inspected", Output: output.String()}, nil
	})
}

type tuiExternalCommand func(context.Context, string, ...string) ([]byte, error)

func runTUIExternalCommand(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).CombinedOutput()
}

func runTUIImpeccableInstall(ctx context.Context, lookPath func(string) (string, error), run tuiExternalCommand) (controlplane.ActionResult, error) {
	executable, err := lookPath("impeccable")
	args := []string{"install", "--providers=veto", "--scope=global"}
	if err != nil {
		executable, err = lookPath("npx")
		if err != nil {
			return controlplane.ActionResult{ActionID: "impeccable"}, errors.New("Impeccable installation requires the impeccable CLI or npx")
		}
		args = append([]string{"--yes", "impeccable"}, args...)
	}
	installCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	output, runErr := run(installCtx, executable, args...)
	result := controlplane.ActionResult{ActionID: "impeccable", Output: strings.TrimSpace(string(output))}
	if runErr != nil {
		return result, fmt.Errorf("install Impeccable integration: %w", runErr)
	}
	result.Summary = "Impeccable installed for Veto"
	return result, nil
}

func runTUIDoctor(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	offline := request.Arguments["offline"] != "false"
	report := runDoctor(doctorOptions{ctx: ctx, fix: request.Arguments["fix"] == "true", offline: offline}, defaultDoctorDeps())
	result := controlplane.ActionResult{ActionID: "doctor", Summary: fmt.Sprintf("%d pass, %d warn, %d fail, %d fixed", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.Summary.Fixed)}
	if request.Arguments["json"] == "true" {
		var output strings.Builder
		if err := writeDoctorReport(&output, report, true); err != nil {
			return result, err
		}
		result.Output = output.String()
	}
	if !report.OK {
		return result, fmt.Errorf("doctor found %d failing check(s)", report.Summary.Fail)
	}
	return result, nil
}

type tuiCommandRunner func([]string, *strings.Builder, *strings.Builder) int

func runTUICommand(actionID string, args []string, run tuiCommandRunner) (controlplane.ActionResult, error) {
	var output, diagnostics strings.Builder
	if code := run(args, &output, &diagnostics); code != 0 {
		message := strings.TrimSpace(diagnostics.String())
		if message == "" {
			message = fmt.Sprintf("%s exited with status %d", actionID, code)
		}
		return controlplane.ActionResult{ActionID: actionID, Output: output.String()}, errors.New(message)
	}
	return controlplane.ActionResult{ActionID: actionID, Summary: actionID + " complete", Output: output.String()}, nil
}

func tuiFlagArguments(request controlplane.ActionRequest) []string {
	return tuiFlagArgumentsFor(request)
}

func tuiFlagArgumentsFor(request controlplane.ActionRequest, allowed ...string) []string {
	allowAll := len(allowed) == 0
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	keys := make([]string, 0, len(request.Arguments))
	for key, value := range request.Arguments {
		if key == "objective" || key == "task" || key == "subcommand" || key == "operation" || value == "" || (!allowAll && !containsTUIFlag(allowedSet, key)) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys))
	for _, key := range keys {
		value := request.Arguments[key]
		if value == "true" {
			args = append(args, "--"+key)
		} else {
			args = append(args, "--"+key+"="+value)
		}
	}
	return args
}

func containsTUIFlag(allowed map[string]struct{}, key string) bool {
	_, ok := allowed[key]
	return ok
}

func runTUILogin(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	provider := strings.ToLower(strings.TrimSpace(request.Arguments["provider"]))
	mode := strings.ToLower(strings.TrimSpace(request.Arguments["mode"]))
	if mode == "" {
		mode = "api-key"
	}
	if provider == "opencode" {
		args := []string{"connect"}
		args = append(args, tuiFlagArgumentsFor(request, "server", "managed", "cli")...)
		return runTUICommand("login", args, func(arguments []string, output, diagnostics *strings.Builder) int {
			return runOpenCodeCommand(arguments, output, diagnostics, opencodert.DefaultDependencies(), vetoCfgPath())
		})
	}
	if provider == "local" {
		model := localModelFromTUIRequest(request)
		builtins := make(map[string]bool)
		for _, candidate := range router.NewRegistry().All() {
			builtins[candidate.Name] = true
		}
		if err := validateLocalModel(model, builtins); err != nil {
			return controlplane.ActionResult{ActionID: "login"}, err
		}
		if err := saveLocalModel(model); err != nil {
			return controlplane.ActionResult{ActionID: "login"}, err
		}
		return controlplane.ActionResult{ActionID: "login", Summary: "local model connected"}, nil
	}
	var providerInfo providerInfo
	for _, candidate := range knownProviders {
		if candidate.provider == provider {
			providerInfo = candidate
			break
		}
	}
	if providerInfo.provider == "" {
		return controlplane.ActionResult{ActionID: "login"}, fmt.Errorf("unsupported provider %q", provider)
	}
	switch mode {
	case "api-key":
	case "subscription":
		if provider != "anthropic" {
			return controlplane.ActionResult{ActionID: "login"}, fmt.Errorf("subscription mode is only supported for anthropic")
		}
	case "browser", "oauth":
		if provider != "openrouter" {
			return controlplane.ActionResult{ActionID: "login"}, fmt.Errorf("browser mode is only supported for openrouter")
		}
	default:
		return controlplane.ActionResult{ActionID: "login"}, fmt.Errorf("unsupported login mode %q for %s", mode, provider)
	}
	if provider == "anthropic" && mode == "subscription" {
		if _, err := exec.LookPath("claude"); err != nil {
			return controlplane.ActionResult{ActionID: "login"}, errors.New("claude CLI is not installed or not in PATH")
		}
		if err := saveCredential("CLAUDE_SUBSCRIPTION", "true"); err != nil {
			return controlplane.ActionResult{ActionID: "login"}, err
		}
		return controlplane.ActionResult{ActionID: "login", Summary: "Claude subscription connected"}, nil
	}
	if provider == "openrouter" && (mode == "browser" || mode == "oauth") {
		oauthCtx, cancel := context.WithTimeout(ctx, openRouterOAuthWait+5*time.Second)
		defer cancel()
		credential, err := authorizeOpenRouter(oauthCtx, defaultOpenRouterOAuthDeps())
		if err != nil {
			return controlplane.ActionResult{ActionID: "login"}, err
		}
		if err := saveCredential(providerInfo.envKey, credential); err != nil {
			return controlplane.ActionResult{ActionID: "login"}, err
		}
		return controlplane.ActionResult{ActionID: "login", Summary: "OpenRouter connected via browser"}, nil
	}
	credential := tuiSecretArgument(request, "api-key")
	if strings.TrimSpace(credential) == "" {
		return controlplane.ActionResult{ActionID: "login"}, fmt.Errorf("api-key is required for %s", providerInfo.name)
	}
	if err := saveCredential(providerInfo.envKey, credential); err != nil {
		return controlplane.ActionResult{ActionID: "login"}, err
	}
	return controlplane.ActionResult{ActionID: "login", Summary: providerInfo.name + " connected"}, nil
}

func tuiSecretArgument(request controlplane.ActionRequest, name string) string {
	return request.Arguments[name]
}

func localModelFromTUIRequest(request controlplane.ActionRequest) LocalModel {
	model := LocalModel{
		Name:     strings.TrimSpace(request.Arguments["name"]),
		Endpoint: strings.TrimSpace(request.Arguments["endpoint"]),
		Model:    strings.TrimSpace(request.Arguments["model"]),
	}
	model.APIKey = tuiSecretArgument(request, "api-key")
	return model
}

func runTUILogout(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	target := strings.TrimSpace(request.Arguments["target"])
	if target == "" {
		return controlplane.ActionResult{ActionID: "logout"}, errors.New("target is required")
	}
	if strings.EqualFold(target, "subscription") {
		if err := removeCredential("CLAUDE_SUBSCRIPTION"); err != nil {
			return controlplane.ActionResult{ActionID: "logout"}, err
		}
		return controlplane.ActionResult{ActionID: "logout", Summary: "subscription disconnected"}, nil
	}
	if strings.EqualFold(target, "opencode") {
		if err := removeOpenCodeConfig(vetoCfgPath()); err != nil {
			return controlplane.ActionResult{ActionID: "logout"}, err
		}
		return controlplane.ActionResult{ActionID: "logout", Summary: "OpenCode disconnected"}, nil
	}
	for _, provider := range knownProviders {
		if strings.EqualFold(target, provider.envKey) || strings.EqualFold(target, provider.provider) {
			if err := removeCredential(provider.envKey); err != nil {
				return controlplane.ActionResult{ActionID: "logout"}, err
			}
			return controlplane.ActionResult{ActionID: "logout", Summary: provider.name + " disconnected"}, nil
		}
	}
	if err := removeTUILocalModel(target); err != nil {
		return controlplane.ActionResult{ActionID: "logout"}, err
	}
	return controlplane.ActionResult{ActionID: "logout", Summary: "local model disconnected"}, nil
}

func removeTUILocalModel(name string) error {
	models, err := loadLocalModels()
	if err != nil {
		return err
	}
	filtered := make([]LocalModel, 0, len(models))
	found := false
	for _, model := range models {
		if model.Name == name {
			found = true
			continue
		}
		filtered = append(filtered, model)
	}
	if !found {
		return fmt.Errorf("%q is not a configured provider or local model", name)
	}
	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	return writeLocalModelsAtomic(localModelsPath(), data)
}

func runTUIDisable(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	return runTUIModelPolicy(request, true)
}

func runTUIEnable(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	return runTUIModelPolicy(request, false)
}

func runTUIModelPolicy(request controlplane.ActionRequest, disable bool) (controlplane.ActionResult, error) {
	requested := splitTaskList(request.Arguments["model"])
	if len(requested) == 0 {
		return controlplane.ActionResult{ActionID: request.ActionID}, errors.New("model is required")
	}
	disabled := loadDisabledModels()
	if disabled == nil {
		disabled = make(map[string]bool)
	}
	if disable {
		for _, name := range requested {
			disabled[name] = true
		}
	} else {
		for _, name := range requested {
			delete(disabled, name)
		}
	}
	disabledNames := make([]string, 0, len(disabled))
	for model := range disabled {
		disabledNames = append(disabledNames, model)
	}
	sort.Strings(disabledNames)
	if err := saveDisabledModels(disabledNames); err != nil {
		return controlplane.ActionResult{ActionID: request.ActionID}, err
	}
	action := "enabled"
	if disable {
		action = "disabled"
	}
	return controlplane.ActionResult{ActionID: request.ActionID, Summary: strings.Join(requested, ", ") + " " + action}, nil
}

func runTUIInstallGitHook(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	force := request.Arguments["force"] == "true"
	path, err := installGitHookFile(force)
	if err != nil {
		return controlplane.ActionResult{ActionID: "install-git-hook"}, err
	}
	return controlplane.ActionResult{ActionID: "install-git-hook", Summary: "git hook installed", Output: path}, nil
}

func installGitHookFile(force bool) (string, error) {
	if _, err := os.Stat(".git"); errors.Is(err, os.ErrNotExist) {
		return "", errors.New("not inside a git repository")
	} else if err != nil {
		return "", err
	}
	hookPath := filepath.Join(".git", "hooks", "prepare-commit-msg")
	if existing, err := os.ReadFile(hookPath); err == nil && !force && !strings.Contains(string(existing), hookMarker) {
		return "", fmt.Errorf("%s already exists and was not installed by veto; re-run with --force to overwrite it", hookPath)
	}
	script := "#!/bin/sh\n# " + hookMarker + "\n" +
		"MODEL=$(veto route --quiet --task \"$(git diff --cached --stat)\" 2>/dev/null)\n" +
		"if [ -n \"$MODEL\" ]; then\n  printf '\\n# veto suggested model: %s\\n' \"$MODEL\" >> \"$1\"\nfi\n"
	if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
		return "", err
	}
	return hookPath, nil
}

func runTUIFeedback(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	args := tuiFlagArguments(request)
	input := strings.NewReader("")
	if request.Arguments["stdin"] == "true" {
		payload, err := json.Marshal(tuiFeedbackReport(request))
		if err != nil {
			return controlplane.ActionResult{ActionID: "feedback"}, err
		}
		args = []string{"--stdin"}
		input = strings.NewReader(string(payload))
		if request.Arguments["include-provider"] == "true" {
			args = append(args, "--include-provider", "--provider="+request.Arguments["provider"])
		}
	}
	args = append(args, "--json", "--no-browser")
	result, err := runFeedback(args, input, &strings.Builder{}, &strings.Builder{}, nil)
	if err != nil {
		return controlplane.ActionResult{ActionID: "feedback"}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return controlplane.ActionResult{ActionID: "feedback"}, err
	}
	return controlplane.ActionResult{ActionID: "feedback", Summary: "redacted feedback saved", Output: string(data)}, nil
}

func tuiFeedbackReport(request controlplane.ActionRequest) FeedbackReport {
	return FeedbackReport{
		Kind:                request.Arguments["kind"],
		Summary:             request.Arguments["summary"],
		Reproduction:        request.Arguments["reproduction"],
		ExpectedBehavior:    request.Arguments["expected"],
		ActualBehavior:      request.Arguments["actual"],
		Scope:               request.Arguments["scope"],
		AcceptanceCriteria:  splitFeedbackCriteria(request.Arguments["acceptance-criteria"]),
		BaselinePerformance: request.Arguments["baseline"],
		TargetPerformance:   request.Arguments["target"],
		RegressionStatus:    request.Arguments["regression"],
		Evidence:            request.Arguments["evidence"],
		Metadata: FeedbackMetadata{
			Command:       request.Arguments["command"],
			Risk:          request.Arguments["risk"],
			ProviderModel: request.Arguments["provider"],
		},
	}
}

func runTUIVerifyModels(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	providerName := strings.ToLower(strings.TrimSpace(request.Arguments["provider"]))
	if providerName == "" {
		providerName = "openai"
	}
	provider, ok := modelListProviders[providerName]
	if !ok {
		return controlplane.ActionResult{ActionID: "verify-models"}, fmt.Errorf("unsupported provider %q", providerName)
	}
	creds, err := loadCredentials()
	if err != nil {
		return controlplane.ActionResult{ActionID: "verify-models"}, err
	}
	key := getKey(provider.envKey, creds)
	if key == "" {
		return controlplane.ActionResult{ActionID: "verify-models"}, fmt.Errorf("%s is not configured", provider.envKey)
	}
	endpoint := strings.TrimSpace(request.Arguments["endpoint"])
	if endpoint == "" {
		endpoint = provider.endpoint
	}
	artifactDir := request.Arguments["artifacts-dir"]
	if artifactDir == "" {
		artifactDir = "artifacts/http"
	}
	timeout := 20 * time.Second
	if raw := request.Arguments["timeout"]; raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil {
			return controlplane.ActionResult{ActionID: "verify-models"}, parseErr
		}
		timeout = parsed
	}
	result, err := verifyProviderModels(ctx, provider, key, endpoint, artifactDir, timeout)
	if err != nil {
		return controlplane.ActionResult{ActionID: "verify-models"}, err
	}
	output, err := formatTUIModelVerification(result, request.Arguments["json"] == "true")
	if err != nil {
		return controlplane.ActionResult{ActionID: "verify-models"}, err
	}
	if len(result.MissingModels) > 0 {
		return controlplane.ActionResult{ActionID: "verify-models", Output: output}, fmt.Errorf("%d catalog model(s) are unavailable", len(result.MissingModels))
	}
	return controlplane.ActionResult{ActionID: "verify-models", Summary: "models verified", Output: output}, nil
}

func formatTUIModelVerification(result modelVerification, jsonOutput bool) (string, error) {
	if jsonOutput {
		data, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	var output strings.Builder
	fmt.Fprintf(&output, "  %s: %d catalog model(s), %d available\n", result.Provider, len(result.ConfiguredModels), len(result.ConfiguredModels)-len(result.MissingModels))
	if len(result.MissingModels) > 0 {
		fmt.Fprintf(&output, "  Missing: %s\n", strings.Join(result.MissingModels, ", "))
	} else {
		output.WriteString("  All catalog model IDs are available to this account.\n")
	}
	fmt.Fprintf(&output, "  Raw response: %s", result.Artifact)
	return output.String(), nil
}

func runTUIModels(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	args := tuiFlagArguments(request)
	return runTUICommand("models", args, func(arguments []string, output, diagnostics *strings.Builder) int {
		return runModelsCommand(arguments, output, diagnostics, buildProviderRegistryWithCatalog)
	})
}

func runTUISetup(ctx context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return controlplane.ActionResult{ActionID: "setup"}, err
	}
	directory := strings.TrimSpace(request.Arguments["directory"])
	if directory == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return controlplane.ActionResult{ActionID: "setup"}, err
		}
		directory = filepath.Join(home, ".claude", "skills")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return controlplane.ActionResult{ActionID: "setup", Summary: "no external skills found", Output: directory}, nil
		}
		return controlplane.ActionResult{ActionID: "setup"}, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			files = append(files, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(files)
	if request.Arguments["auto-approve"] != "true" {
		approved := splitTaskList(request.Arguments["approved-files"])
		if len(approved) == 0 {
			return controlplane.ActionResult{ActionID: "setup", Summary: fmt.Sprintf("discovered %d skill(s); no changes made", len(files)), Output: strings.Join(files, "\n")}, nil
		}
		return approveTUISkillFiles(directory, files, approved)
	}
	cfg := loadSkillsConfig()
	if !containsStr(cfg.ApprovedDirs, directory) {
		cfg.ApprovedDirs = append(cfg.ApprovedDirs, directory)
		sort.Strings(cfg.ApprovedDirs)
	}
	cfg.AutoApproveNew = true
	if err := saveSkillsConfig(cfg); err != nil {
		return controlplane.ActionResult{ActionID: "setup"}, err
	}
	return controlplane.ActionResult{ActionID: "setup", Summary: fmt.Sprintf("approved %d skill(s)", len(files)), Output: strings.Join(files, "\n")}, nil
}

func approveTUISkillFiles(directory string, discovered, requested []string) (controlplane.ActionResult, error) {
	allowed := make(map[string]struct{}, len(discovered))
	for _, path := range discovered {
		allowed[path] = struct{}{}
	}
	cfg := loadSkillsConfig()
	approved := make([]string, 0, len(requested))
	for _, path := range requested {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(directory, path)
		}
		if _, ok := allowed[path]; !ok {
			return controlplane.ActionResult{ActionID: "setup"}, fmt.Errorf("approved skill %q was not discovered in %s", path, directory)
		}
		if !containsStr(cfg.ApprovedFiles, path) {
			cfg.ApprovedFiles = append(cfg.ApprovedFiles, path)
		}
		approved = append(approved, path)
	}
	if len(approved) == 0 {
		return controlplane.ActionResult{ActionID: "setup", Summary: "no skill files selected"}, nil
	}
	sort.Strings(cfg.ApprovedFiles)
	if err := saveSkillsConfig(cfg); err != nil {
		return controlplane.ActionResult{ActionID: "setup"}, err
	}
	return controlplane.ActionResult{ActionID: "setup", Summary: fmt.Sprintf("approved %d selected skill(s)", len(approved)), Output: strings.Join(approved, "\n")}, nil
}

func runTUIExec(ctx context.Context, request controlplane.ActionRequest, service *application.ControlService, reg *providerRegistry, mgr *router.Manager) (controlplane.ActionResult, error) {
	planPath := strings.TrimSpace(request.Arguments["plan"])
	if planPath == "" {
		return controlplane.ActionResult{ActionID: "exec"}, errors.New("plan is required")
	}
	planPath, err := resolveTUIPlanPath(planPath)
	if err != nil {
		return controlplane.ActionResult{ActionID: "exec"}, err
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		return controlplane.ActionResult{ActionID: "exec"}, err
	}
	plan, parseErr := ParsePlan(data)
	if parseErr != nil {
		return controlplane.ActionResult{ActionID: "exec"}, fmt.Errorf("plan validation failed: %w", parseErr)
	}
	if violations := ValidatePlan(plan); len(violations) > 0 {
		return controlplane.ActionResult{ActionID: "exec"}, fmt.Errorf("plan validation failed: %s", strings.Join(violations, "; "))
	}
	if request.Arguments["dry-run"] == "true" {
		lines := make([]string, 0, len(plan.Steps)+1)
		lines = append(lines, fmt.Sprintf("Plan: %s (%d step(s))", plan.Title, len(plan.Steps)))
		for index, step := range plan.Steps {
			lines = append(lines, fmt.Sprintf("%2d  %-12s %-6s %s", index+1, step.Kind, step.Risk, strings.TrimSpace(step.Task)))
		}
		return controlplane.ActionResult{ActionID: "exec", Summary: "plan validated", Output: strings.Join(lines, "\n")}, nil
	}
	failureMode := request.Arguments["on-failure"]
	if failureMode == "" {
		failureMode = resolveOnFailure("")
	}
	if failureMode != "abort" && failureMode != "continue" && failureMode != "abort-ask" {
		return controlplane.ActionResult{ActionID: "exec"}, fmt.Errorf("invalid on-failure mode %q", failureMode)
	}
	if failureMode == "abort-ask" {
		// No interactive confirmation is wired up yet; fail visibly rather than
		// silently behaving like "abort" while claiming to have asked.
		return controlplane.ActionResult{ActionID: "exec"}, fmt.Errorf("on-failure mode %q is not yet supported (no confirmation flow implemented); use \"abort\" or \"continue\"", failureMode)
	}
	stepTimeout := 60 * time.Second
	if raw := request.Arguments["timeout"]; raw != "" {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil || parsed <= 0 {
			return controlplane.ActionResult{ActionID: "exec"}, fmt.Errorf("invalid timeout %q", raw)
		}
		stepTimeout = parsed
	}
	maxTokens := request.Arguments["max-output-tokens"]
	outputs := make([]string, 0, len(plan.Steps))
	allCriteria := make([]string, 0)
	failed := make([]int, 0)
	for index, step := range plan.Steps {
		if err := ctx.Err(); err != nil {
			return controlplane.ActionResult{ActionID: "exec", Output: strings.Join(outputs, "\n\n---\n\n")}, err
		}
		stepCtx, cancel := context.WithTimeout(ctx, stepTimeout)
		arguments := map[string]string{"objective": step.Task, "kind": step.Kind, "risk": step.Risk}
		if maxTokens != "" {
			arguments["max-output-tokens"] = maxTokens
		}
		result, runErr := service.Execute(stepCtx, controlplane.ActionRequest{ActionID: "run", Arguments: arguments})
		cancel()
		if runErr != nil {
			failed = append(failed, index+1)
			if failureMode != "continue" {
				return controlplane.ActionResult{ActionID: "exec", Output: strings.Join(outputs, "\n\n---\n\n")}, fmt.Errorf("step %d failed: %w", index+1, runErr)
			}
			continue
		}
		outputs = append(outputs, result.Output)
		criteria := splitCriteria(step.SuccessCriteria)
		allCriteria = append(allCriteria, criteria...)
		if len(criteria) == 0 {
			continue
		}
		spec := router.TaskSpec{ID: taskHash(step.Task, step.Kind, step.Risk, 0), Kind: router.TaskKind(step.Kind), Complexity: router.InferComplexity(step.Task, router.TaskKind(step.Kind)), Objective: step.Task, Risk: router.Risk(step.Risk), SuccessCriteria: criteria}
		reviewCtx, reviewCancel := context.WithTimeout(ctx, stepTimeout)
		review, reviewErr := reviewOutput(reviewCtx, reg, mgr, spec, result.Output, result.Model)
		reviewCancel()
		if reviewErr != nil || !review.Passed {
			failed = append(failed, index+1)
			if failureMode != "continue" {
				if reviewErr != nil {
					return controlplane.ActionResult{ActionID: "exec", Output: strings.Join(outputs, "\n\n---\n\n")}, fmt.Errorf("step %d review failed: %w", index+1, reviewErr)
				}
				return controlplane.ActionResult{ActionID: "exec", Output: strings.Join(outputs, "\n\n---\n\n")}, fmt.Errorf("step %d review failed: acceptance criteria not met", index+1)
			}
		}
	}
	if len(allCriteria) > 0 && len(outputs) > 0 && len(failed) == 0 {
		allCriteria = append(allCriteria, "no step undid another step's work (no regression introduced)")
		finalSpec := router.TaskSpec{
			ID:              taskHash(fmt.Sprintf("%s|steps=%d", plan.Title, len(plan.Steps)), "review", "medium", 0),
			Kind:            router.KindReview,
			Objective:       fmt.Sprintf("Plan: %s\n\nAll %d step(s) completed.", plan.Title, len(plan.Steps)),
			Risk:            router.RiskMedium,
			SuccessCriteria: allCriteria,
		}
		combinedOutput := strings.Join(outputs, "\n\n---\n\n")
		reviewCtx, reviewCancel := context.WithTimeout(ctx, stepTimeout)
		finalReview, reviewErr := reviewOutput(reviewCtx, reg, mgr, finalSpec, combinedOutput, "")
		reviewCancel()
		if reviewErr != nil {
			return controlplane.ActionResult{ActionID: "exec", Output: combinedOutput}, fmt.Errorf("final plan review failed: %w", reviewErr)
		}
		if !finalReview.Passed {
			return controlplane.ActionResult{ActionID: "exec", Output: combinedOutput}, errors.New("final plan review failed: acceptance criteria not fully met")
		}
	}
	output := strings.Join(outputs, "\n\n---\n\n")
	summary := fmt.Sprintf("%d step(s) completed", len(plan.Steps)-len(failed))
	if len(failed) > 0 {
		summary = fmt.Sprintf("%d step(s) completed, %d failed", len(plan.Steps)-len(failed), len(failed))
	}
	if len(failed) > 0 {
		return controlplane.ActionResult{ActionID: "exec", Summary: summary, Output: output}, fmt.Errorf("plan failed on step(s) %v", failed)
	}
	return controlplane.ActionResult{ActionID: "exec", Summary: summary, Output: output}, nil
}

func resolveTUIPlanPath(planPath string) (string, error) {
	if strings.HasPrefix(planPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		planPath = filepath.Join(home, strings.TrimPrefix(planPath, "~/"))
	}
	if filepath.IsAbs(planPath) || strings.ContainsRune(planPath, filepath.Separator) || strings.Contains(planPath, "/") {
		return planPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".veto", "plans", planPath), nil
}
