package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/oleg-koval/veto/pkg/ledger"
)

func cmdExperiment(args []string) int {
	return runExperiment(args, os.Stdout, os.Stderr)
}

func runExperiment(args []string, output, diagnostics io.Writer) int {
	fs := flag.NewFlagSet("experiment", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	clear := fs.Bool("clear", false, "delete the local native-dispatch experiment log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := experimentPath()
	if *clear {
		if err := resetExperimentLogger(); err != nil {
			fmt.Fprintln(diagnostics, "error:", err)
			return 1
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(diagnostics, "error:", err)
			return 1
		}
		fmt.Fprintln(output, "Native-dispatch experiment log deleted.")
		return 0
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		fmt.Fprintln(output, "No native-dispatch experiment events recorded.")
		return 0
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return 1
	}
	events, corrupt, readErr := ledger.Read(file)
	_ = file.Close()
	if readErr != nil {
		fmt.Fprintln(diagnostics, "warning: experiment log partially unreadable:", readErr)
	}
	filtered := make([]ledger.Event, 0, len(events))
	for _, event := range events {
		if strings.HasPrefix(string(event.Type), "experiment.") {
			filtered = append(filtered, event)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Timestamp.Before(filtered[j].Timestamp) })
	for _, event := range filtered {
		choice := event.FinalAgent
		if event.FinalModel != "" {
			choice += "/" + event.FinalModel
		}
		fmt.Fprintf(output, "%s %-28s mode=%s proposed=%s final=%s override=%t outcome=%s\n", event.Timestamp.Local().Format("2006-01-02 15:04:05"), event.Type, event.Mode, event.ProposedAgent, choice, event.Override, event.Outcome)
	}
	if corrupt > 0 {
		fmt.Fprintf(diagnostics, "warning: skipped %d malformed experiment record(s)\n", corrupt)
	}
	if len(filtered) == 0 {
		fmt.Fprintln(output, "No native-dispatch experiment events recorded.")
	}
	return 0
}
