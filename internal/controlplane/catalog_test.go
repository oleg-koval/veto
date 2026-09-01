package controlplane

import "testing"

func TestDefaultCatalogCoversCLICommands(t *testing.T) {
	t.Parallel()

	want := []string{
		"login", "logout", "setup", "run", "exec", "route", "benchmark",
		"verify-models", "doctor", "feedback", "analytics", "opencode", "hermes",
		"models", "providers", "disable", "enable", "version", "install-git-hook",
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

func hasFlag(action ActionSpec, name string) bool {
	for _, flag := range action.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}
