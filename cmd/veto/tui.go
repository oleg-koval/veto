package main

import (
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

	reg, mgr, _, err := prepareRouting()
	if err != nil {
		return fmt.Errorf("prepare routing: %w", err)
	}
	service := application.NewControlService(newApplicationRunner(reg, mgr), mgr)
	model := tui.NewModel(controlplane.DefaultCatalog(), tui.Options{
		Motion:  !*reduceMotion,
		NoColor: *noColor || os.Getenv("NO_COLOR") != "",
		Mouse:   !*noMouse,
		Service: service,
	})
	_, err = tea.NewProgram(model).Run()
	return err
}
