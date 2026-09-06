# Veto TUI implementation status

The flagship TUI runs in-process from `veto tui` (and from bare `veto` on an
interactive terminal) over the existing Runner and Router.

Implemented:

- responsive Bubble Tea shell with statusline, palette, help, mouse/keyboard
  navigation, no-color, reduced motion, an explicit `--screen-reader`
  text-only mode on a stable alternate-screen frame, loading, cancellation,
  and small terminal clipping;
- typed forms for routing, execution, provider login/logout (including
  OpenRouter browser OAuth), setup (directory or individual skill approval), feedback,
  verification, analytics, integrations, model policy, and git-hook actions;
- masked secret fields and explicit confirmation for state-changing actions;
- live versioned routing/runtime event timeline with filtering, shortlist,
  admission, winner, execution, review, and failure stages;
- provider/model explorer plus redacted history, plans, doctor health,
  analytics, and integration views;
- plan selection opens the Execute-plan form with safe `~/.veto/plans` name
  resolution; doctor JSON and feedback JSON-input modes are available;
- plan validation, dry-run, step execution, deterministic `abort` or `continue`
  failure policy, per-step criteria review, and a final cross-step regression
  review through the same control service used by route/run; interactive
  `abort-ask` remains available in the normal CLI;
- animated running status with reduced-motion and no-color text fallbacks.
- live model disable/enable updates the active router without restarting the TUI.
- fresh installs open the shell without providers; login/logout reloads the
  in-process Runner/Router bindings immediately.

Verification currently includes the race-enabled Go test suite, `go vet`, native
builds, Linux/macOS/Windows cross-builds (also enforced by the CI matrix),
model/update tests, replay tests, and the integrated `scripts/tui-pty-smoke.py`
PTY smoke. The onboarding variant exercises real Route, Run, and Execute-plan
actions against a fake local provider, including live event/output assertions,
resize, keyboard/mouse input, in-flight Runner cancellation, alternate-screen
cleanup, and secret masking.
The standalone PTY smoke also runs the shell under `xterm-256color`,
`screen-256color`, `vt100`, and `dumb` TERM profiles.
It verifies both explicit `veto tui` and bare interactive `veto` launch paths.
Human keyboard-only,
screen-reader, resize, and multi-terminal trials are still required before a
beta/release claim. Use the TUI trial protocol in
`docs/onboarding-trial.md` to record those runs without collecting credentials.
