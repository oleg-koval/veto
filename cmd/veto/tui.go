package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/oleg-koval/veto/internal/application"
	"github.com/oleg-koval/veto/internal/controlplane"
	"github.com/oleg-koval/veto/internal/tui"
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
			return service, nil
		},
	})
	_, err := tea.NewProgram(model).Run()
	return err
}
