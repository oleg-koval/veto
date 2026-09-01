# Veto TUI implementation status

The flagship TUI runs in-process from `veto tui` (and from bare `veto` on an
interactive terminal) over the existing Runner and Router.

Implemented:

- responsive Bubble Tea shell with statusline, palette, help, mouse/keyboard
  navigation, no-color, reduced motion, loading, cancellation, and small
  terminal clipping;
- typed forms for routing, execution, provider login/logout, setup, feedback,
  verification, analytics, integrations, model policy, and git-hook actions;
- masked secret fields and explicit confirmation for state-changing actions;
- live versioned routing/runtime event timeline with bounded monitor counters;
- provider/model explorer plus redacted history, plans, doctor health,
  analytics, and integration views;
- plan validation, dry-run, step execution, failure policy, and criteria review
  through the same control service used by route/run.

Verification currently includes the race-enabled Go test suite, `go vet`, native
builds, Linux/macOS/Windows cross-builds, model/update tests, replay tests, and
local PTY smoke. Human keyboard-only, screen-reader, resize, and multi-terminal
trials are still required before a beta/release claim.
