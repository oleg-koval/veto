# Fresh-User Onboarding Trial

Use this protocol with at least three people who have not contributed to veto. Do not ask participants to share credentials, terminal history, or `~/.veto` contents.

## Participant setup

- Record operating system, architecture, installation method, and whether the participant already uses an AI provider CLI.
- Start timing before the participant opens the installation instructions.
- The observer may answer product questions but must not type commands for the participant.

## Tasks

1. Install veto and run `veto --help` and `veto version`.
2. Run `veto providers` and explain the displayed state.
3. Configure one provider or a local test model using the documented path.
4. Run one `veto route --json` task and identify the selected model.
5. Run one `veto run --quiet` task.
6. Save output with `--output result.txt`, then confirm overwrite protection and retry with `--force`.
7. Run a task with two acceptance criteria and interpret a failed review.
8. Remove the configured provider or local model with `veto logout`.

## TUI trial tasks

Run these after the CLI flow in each selected terminal:

1. Launch `veto tui --no-color --reduce-motion` and confirm the shell, statusline, and command rail are readable. For assistive-technology review, use `veto tui --screen-reader` to force stable text-only output.
2. Complete the same flow keyboard-only: open help, open the palette, launch the Run form, edit a boolean flag, and cancel without executing.
3. With a configured or fake local provider, submit one Route and one Run task; confirm filtering, admissions, winner, output, and completion events appear in the live timeline.
4. Open Models, Providers, Plans, History, Doctor, Analytics, and Integrations; confirm each screen has useful text when data is empty and populated.
5. Resize to a narrow terminal and confirm no content is lost beyond the documented clipping hint; repeat with mouse navigation enabled.
6. Read the `--no-color` output without relying on color, and verify secrets remain masked in the provider form.

## Record without secrets

- Minutes to first successful route and first successful run.
- Commands whose purpose was unclear.
- Error messages that did not suggest a recovery action.
- Whether stdout was usable in a shell pipeline.
- Whether estimated versus actual/unknown cost was understood.
- Any step requiring observer intervention.
- Participant confidence from 1 to 5 after completing the flow.
- TUI terminal dimensions, keyboard-only success, screen-reader/text-fallback issues, and resize behavior.

## Exit criteria for beta

- At least 3 completed trials across at least 2 supported operating systems.
- At least 80% complete first route and first run without observer intervention.
- Median time to first successful run is under 10 minutes, excluding provider account creation or model download.
- No credential disclosure, unexpected file write, or unexplained external network request occurs.
- Every blocking failure has either a code fix or a documented recovery step before release.

The automated `scripts/onboarding-smoke.sh` test validates deterministic mechanics
only: it drives Route, Run, and Execute-plan through a PTY against a fake local
provider and checks live events, output visibility, resize, in-flight cancellation,
and secret masking.
The standalone shell pass also covers common `TERM` profiles (`xterm-256color`,
`screen-256color`, `vt100`, and `dumb`).
It does not replace these human trials.
