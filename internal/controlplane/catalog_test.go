package controlplane

import "testing"

func TestDefaultCatalogCoversCLICommands(t *testing.T) {
	t.Parallel()

	want := []string{
		"login", "logout", "setup", "run", "exec", "route", "benchmark",
		"verify-models", "doctor", "feedback", "analytics", "opencode", "hermes",
		"models", "providers", "disable", "enable", "version", "install-git-hook",
		"start", "unavailable", "experiment",
	}

	catalog := DefaultCatalog()
	if got := catalog.Commands(); len(got) != len(want) {
		t.Fatalf("catalog command count = %d, want %d", len(got), len(want))
	}
	for _, command := range want {
		if _, ok := catalog.Find(command); !ok {
			t.Errorf("catalog does not expose CLI command %q", command)
		}
	}
}

func TestDefaultCatalogHasUniqueCommandAndFlagIDs(t *testing.T) {
	t.Parallel()

	seenCommands := make(map[string]struct{})
	seenFlags := make(map[string]struct{})
	for _, action := range DefaultCatalog().Commands() {
		if _, ok := seenCommands[action.ID]; ok {
			t.Fatalf("duplicate action ID %q", action.ID)
		}
		seenCommands[action.ID] = struct{}{}
		for _, flag := range action.Flags {
			key := action.ID + ":" + flag.Name
			if _, ok := seenFlags[key]; ok {
				t.Fatalf("duplicate flag ID %q", key)
			}
			seenFlags[key] = struct{}{}
		}
	}
}

func TestDefaultCatalogPreservesRouteAndFeedbackFlags(t *testing.T) {
	t.Parallel()

	route, ok := DefaultCatalog().Find("route")
	if !ok {
		t.Fatal("route action missing")
	}
	for _, name := range []string{"task", "kind", "risk", "max-cost", "timeout", "quiet", "json", "no-resume", "runtime", "provider", "dashboard"} {
		if !hasFlag(route, name) {
			t.Errorf("route flag %q missing", name)
		}
	}

	feedback, ok := DefaultCatalog().Find("feedback")
	if !ok {
		t.Fatal("feedback action missing")
	}
	for _, name := range []string{"kind", "summary", "expected", "actual", "stdin", "json", "include-provider", "no-browser"} {
		if !hasFlag(feedback, name) {
			t.Errorf("feedback flag %q missing", name)
		}
	}
}

func TestDefaultCatalogUsesBoundedExecutionTokenDefaults(t *testing.T) {
	t.Parallel()

	for _, actionID := range []string{"run", "exec"} {
		action, ok := DefaultCatalog().Find(actionID)
		if !ok {
			t.Fatalf("action %q missing", actionID)
		}
		found := false
		for _, field := range action.Flags {
			if field.Name == "max-output-tokens" {
				found = true
				if field.Default == "" {
					t.Fatalf("%s max-output-tokens has no bounded default", actionID)
				}
			}
		}
		if !found {
			t.Fatalf("%s is missing max-output-tokens", actionID)
		}
	}
}

func TestDefaultCatalogPreservesIntegrationOutputFlags(t *testing.T) {
	t.Parallel()

	opencode, ok := DefaultCatalog().Find("opencode")
	if !ok {
		t.Fatal("opencode action missing")
	}
	if !hasFlag(opencode, "json") {
		t.Fatal("opencode json flag missing")
	}
}

func TestCatalogReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	catalog := DefaultCatalog()
	commands := catalog.Commands()
	commands[0].Flags = append(commands[0].Flags, FlagSpec{Name: "mutated"})
	commands[0].Subcommands = append(commands[0].Subcommands, "mutated")
	found, _ := catalog.Find(commands[0].ID)
	if hasFlag(found, "mutated") {
		t.Fatal("catalog flags were mutated through Commands result")
	}
	for _, subcommand := range found.Subcommands {
		if subcommand == "mutated" {
			t.Fatal("catalog subcommands were mutated through Commands result")
		}
	}
}

func hasFlag(action ActionSpec, name string) bool {
	for _, flag := range action.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}
