package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	hermesintegration "github.com/oleg-koval/veto/integrations/hermes"
)

const hermesPluginAPIVersion = 1

func cmdHermes(args []string) {
	code := runHermesCommand(args, os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func runHermesCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHermesUsage(stderr)
		return 2
	}
	switch args[0] {
	case "api":
		flags := flag.NewFlagSet("hermes api", flag.ContinueOnError)
		flags.SetOutput(stderr)
		jsonOutput := flags.Bool("json", false, "emit the native plugin API handshake")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if flags.NArg() != 0 {
			fmt.Fprintln(stderr, "unexpected Hermes API arguments")
			return 2
		}
		if *jsonOutput {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"api_version": hermesPluginAPIVersion,
				"version":     resolvedVersion(),
			})
		} else {
			fmt.Fprintf(stdout, "Veto Hermes plugin API %d (veto %s)\n", hermesPluginAPIVersion, resolvedVersion())
		}
		return 0
	case "plugin":
		return runHermesPlugin(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		printHermesUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown hermes command: %s\n", args[0])
		printHermesUsage(stderr)
		return 2
	}
}

func runHermesPlugin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: veto hermes plugin <install|status|uninstall> [--home PATH] [--force]")
		return 2
	}
	flags := flag.NewFlagSet("hermes plugin "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeFlag := flags.String("home", "", "Hermes home directory")
	force := flags.Bool("force", false, "replace conflicting plugin files (install only)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*force && args[0] != "install") {
		fmt.Fprintln(stderr, "unexpected Hermes plugin arguments")
		return 2
	}
	home, err := hermesHome(*homeFlag)
	if err != nil {
		fmt.Fprintf(stderr, "resolve Hermes home: %v\n", err)
		return 1
	}
	var state hermesintegration.State
	switch args[0] {
	case "install":
		state, err = hermesintegration.Install(home, *force)
	case "status":
		state, err = hermesintegration.Status(home)
	case "uninstall":
		state, err = hermesintegration.Uninstall(home)
	default:
		fmt.Fprintf(stderr, "unknown Hermes plugin command: %s\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "Hermes plugin %s: %v\n", args[0], err)
		return 1
	}
	dir := filepath.Join(home, "plugins", "veto")
	switch args[0] {
	case "install":
		fmt.Fprintf(stdout, "Veto Hermes plugin installed in %s (%d files).\n", dir, state.Installed)
		fmt.Fprintln(stdout, "Validate and enable it explicitly:")
		fmt.Fprintln(stdout, "  hermes plugins doctor veto --ci")
		fmt.Fprintln(stdout, "  hermes plugins enable veto --no-allow-tool-override")
	case "uninstall":
		fmt.Fprintf(stdout, "Veto Hermes plugin files removed from %s. Hermes configuration was not changed.\n", dir)
	default:
		if state.Missing == 0 && state.Modified == 0 {
			fmt.Fprintf(stdout, "Veto Hermes plugin is installed and current in %s (%d files).\n", dir, state.Installed)
		} else {
			fmt.Fprintf(stdout, "Veto Hermes plugin is not current in %s (installed=%d missing=%d modified=%d).\n", dir, state.Installed, state.Missing, state.Modified)
			return 1
		}
	}
	return 0
}

func hermesHome(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	if configured := os.Getenv("HERMES_HOME"); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hermes"), nil
}

func printHermesUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: veto hermes <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  plugin install      install the native plugin without enabling it")
	fmt.Fprintln(w, "  plugin status       compare installed files with this Veto build")
	fmt.Fprintln(w, "  plugin uninstall    remove unchanged Veto-owned plugin files")
	fmt.Fprintln(w, "  api --json          print the plugin compatibility handshake")
}
