# Recovered Veto TUI requirements

## Status

Recovered from the approved [Veto Control Plane plan](veto-control-plane.md)
and the existing CLI/event architecture on 2026-08-31. This is a requirements
record, not an implementation. No full-screen TUI code was found in the
current tree, historical branches, or retained worktrees.

## Product goal

Veto should become a local-first routing control plane that users can operate
without memorizing commands. The TUI is the first full interface after the
control service and versioned event stream are stable. The existing CLI and
browser routing dashboard remain supported surfaces.

## Launch and compatibility

- `veto tui` launches the interface.
- The approved plan also targets launching it from `veto` with no arguments.
- Existing scripted and piped CLI behavior must not change.
- The TUI must work without a daemon by running the same service in-process.
- State comes from the control service plus event snapshot/replay, not a second
  routing implementation.

## Required screens

### Shell, navigation, and accessibility

- Clear navigation, theme, and readable status hierarchy.
- Keyboard and mouse navigation.
- Narrow-terminal layouts, no-color mode, and reduced-motion mode.
- Screen-reader-friendly text fallbacks for every visual state.

### Provider onboarding and model explorer

- Connect and disconnect providers and runtimes without memorizing commands.
- Show source, runtime, price, context, tools, status, pins, and exclusions.
- Mask secrets. Never place credentials in events, screen captures, or logs.

### Task composer and live routing graph

- Compose objective, task kind, risk, budget, required tools, output, and
  acceptance criteria from one screen.
- Show filtering, shortlist, admissions, winner, execution, review, and failure
  as they happen.
- Make every visual state available as stable text backed by the event stream.
- Support cancellation, empty states, loading states, and provider failures.

### Monitoring, history, health, and settings

- Monitor active sessions, tools, approvals, cost, tokens, latency, artifacts,
  goals, and provider health.
- Inspect redacted history with bounded retention.
- Link doctor findings to safe repairs without broad automatic mutation.
- Expose analytics preference and local-data controls clearly.

## Data and safety requirements

- Loopback-only control API by default with a per-instance bearer token.
- No credential-returning endpoint.
- Versioned request and event schemas.
- Bounded requests, timeouts, client counts, and event retention.
- Local and remote analytics consent must remain separate from task execution
  approvals and from feedback submission.
- Text-only runtimes must not display or imply file, shell, or browser access.

## Verification gate

- Unit tests for model/update state transitions.
- PTY smoke tests, resize tests, keyboard tests, and cancellation tests.
- Linux, macOS, and Windows builds.
- Keyboard-only, screen-reader fallback, no-color, reduced-motion, and
  small-terminal manual QA.
- Event replay tests prove that a saved session renders the same state.
- Corrupt-ledger recovery, retention, permissions, and doctor-repair tests.
- Human trials before calling the TUI beta-ready.

## Explicitly not recovered as requirements

- A desktop GUI.
- A hosted analytics service.
- A Veto-owned browser engine.
- A replacement for the existing CLI or local browser dashboard.
- A framework choice beyond the plan's recommendation to evaluate Bubble Tea,
  Bubbles, and Lip Gloss after dependency and license review.
