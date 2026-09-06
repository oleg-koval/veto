package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/oleg-koval/veto/pkg/dispatch"
	"github.com/oleg-koval/veto/pkg/ledger"
)

func cmdUnavailable(args []string) int {
	return runUnavailable(args, os.Stdout, os.Stderr, dispatch.NewAvailabilityStore(availabilityPath()))
}

func runUnavailable(args []string, output, diagnostics io.Writer, store *dispatch.AvailabilityStore) int {
	fs := flag.NewFlagSet("unavailable", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	duration := fs.Duration("for", 0, "duration such as 30m or 2h")
	clear := fs.Bool("clear", false, "remove temporary unavailability")
	agentArg := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		agentArg = args[0]
		flagArgs = args[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if agentArg == "" && fs.NArg() == 0 {
		entries, err := store.List()
		if err != nil {
			fmt.Fprintln(diagnostics, "warning: availability state unreadable:", err)
			return 1
		}
		if len(entries) == 0 {
			fmt.Fprintln(output, "No native agents are temporarily unavailable.")
			return 0
		}
		for _, entry := range entries {
			fmt.Fprintf(output, "%s unavailable until %s (%s remaining)\n", entry.Agent, entry.Expires.Local().Format(time.RFC3339), time.Until(entry.Expires).Round(time.Minute))
		}
		return 0
	}
	agent := strings.ToLower(strings.TrimSpace(agentArg))
	if agent == "" {
		agent = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	if *clear {
		if err := store.Clear(agent); err != nil {
			fmt.Fprintln(diagnostics, "error:", err)
			return 1
		}
		fmt.Fprintf(output, "%s is available for dispatch.\n", agent)
		return 0
	}
	if *duration <= 0 {
		fmt.Fprintln(diagnostics, "error: --for is required and must be positive")
		return 2
	}
	entry, err := store.Set(agent, *duration)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return 1
	}
	logNativeUnavailable(agent, entry.Expires)
	fmt.Fprintf(output, "%s unavailable until %s.\n", agent, entry.Expires.Local().Format(time.RFC3339))
	return 0
}

func logNativeUnavailable(agent string, expires time.Time) {
	setupExperimentLogger()
	if experimentLedger == nil {
		return
	}
	runID := currentRunID("availability")
	_ = experimentLedger.Append(ledger.Event{RunID: runID, Type: ledger.EventUnavailableMarked, FinalAgent: agent, Detail: "temporary unavailability recorded"})
}
