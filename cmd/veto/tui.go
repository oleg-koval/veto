package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/internal/eval"
	"github.com/oleg-koval/veto/internal/tui"
	opencodert "github.com/oleg-koval/veto/pkg/opencode"
)

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	reduceMotion := fs.Bool("reduce-motion", false, "disable non-essential animation")
	noColor := fs.Bool("no-color", false, "disable styling and ANSI colors")
	noMouse := fs.Bool("no-mouse", false, "disable mouse reporting")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("tui does not accept positional arguments")
	}

	model := tui.NewModel(controlplane.DefaultCatalog(), tui.Options{
		Motion:  !*reduceMotion,
		NoColor: *noColor || os.Getenv("NO_COLOR") != "",
		Mouse:   !*noMouse,
		ServiceFactory: func() (controlplane.Service, error) {
			reg, mgr, _, err := prepareRouting()
			if err != nil {
				return nil, fmt.Errorf("prepare routing: %w", err)
			}
			service := application.NewControlServiceWithSnapshot(newApplicationRunner(reg, mgr), mgr, loadTUISnapshot)
			service.RegisterHandler("doctor", func(context.Context, controlplane.ActionRequest) (controlplane.ActionResult, error) {
				report := runDoctor(doctorOptions{offline: true}, defaultDoctorDeps())
				return controlplane.ActionResult{ActionID: "doctor", Summary: fmt.Sprintf("%d pass, %d warn, %d fail", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail)}, nil
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
				return controlplane.ActionResult{ActionID: "version", Summary: "veto " + resolvedVersion()}, nil
			})
			registerTUIReadOnlyHandlers(service)
			return service, nil
		},
	})
	_, err := tea.NewProgram(model).Run()
	return err
}

// registerTUIReadOnlyHandlers keeps command-specific parsing in the existing
// CLI functions while giving the TUI a real, redacted execution path. Actions
// that mutate credentials or integration files remain behind explicit CLI
// flows until the confirmation overlay is implemented.
func registerTUIReadOnlyHandlers(service *application.ControlService) {
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
		if subcommand != "status" {
			return controlplane.ActionResult{ActionID: "opencode"}, fmt.Errorf("%s changes integration state; use the explicit CLI confirmation flow", subcommand)
		}
		return runTUICommand("opencode", []string{subcommand}, func(arguments []string, output, diagnostics *strings.Builder) int {
			return runOpenCodeCommand(arguments, output, diagnostics, opencodert.DefaultDependencies(), vetoCfgPath())
		})
	})
	service.RegisterHandler("hermes", func(_ context.Context, request controlplane.ActionRequest) (controlplane.ActionResult, error) {
		subcommand := request.Arguments["subcommand"]
		if subcommand == "" {
			subcommand = "api"
		}
		if subcommand != "api" {
			return controlplane.ActionResult{ActionID: "hermes"}, fmt.Errorf("%s changes integration state; use the explicit CLI confirmation flow", subcommand)
		}
		return runTUICommand("hermes", []string{subcommand}, func(arguments []string, output, diagnostics *strings.Builder) int {
			return runHermesCommand(arguments, output, diagnostics)
		})
	})
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
	keys := make([]string, 0, len(request.Arguments))
	for key, value := range request.Arguments {
		if key == "objective" || key == "task" || key == "subcommand" || value == "" {
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
