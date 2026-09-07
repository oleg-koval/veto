package main

import (
	"strings"
	"testing"

	"github.com/oleg-koval/veto/internal/controlplane"
)

func TestTUICatalogMatchesRootCLICommandInventory(t *testing.T) {
	var usage strings.Builder
	printUsage(&usage)

	cliCommands := make(map[string]struct{})
	inCommands := false
	for _, line := range strings.Split(usage.String(), "\n") {
		if line == "COMMANDS" {
			inCommands = true
			continue
		}
		if inCommands && strings.TrimSpace(line) == "" {
			break
		}
		if !inCommands || !strings.HasPrefix(line, "  ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "tui" {
			continue
		}
		cliCommands[fields[0]] = struct{}{}
	}

	// Commands dispatched by main.go but intentionally omitted from the
	// printed COMMANDS usage text (they're advanced native-dispatch
	// controls, not primary entry points) still belong in the TUI catalog.
	hiddenFromUsage := map[string]struct{}{
		"start":       {},
		"unavailable": {},
		"experiment":  {},
	}

	tuiCommands := make(map[string]struct{})
	for _, action := range controlplane.DefaultCatalog().Commands() {
		if _, hidden := hiddenFromUsage[action.Command]; hidden {
			continue
		}
		tuiCommands[action.Command] = struct{}{}
	}
	if len(cliCommands) != len(tuiCommands) {
		t.Fatalf("CLI commands=%v, TUI commands=%v", sortedKeys(cliCommands), sortedKeys(tuiCommands))
	}
	for command := range cliCommands {
		if _, ok := tuiCommands[command]; !ok {
			t.Errorf("CLI command %q is missing from the TUI catalog", command)
		}
	}
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	// The diagnostic only needs deterministic output; the command list is
	// small enough that a simple insertion sort keeps this test dependency-free.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
