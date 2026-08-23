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

## Record without secrets

- Minutes to first successful route and first successful run.
- Commands whose purpose was unclear.
- Error messages that did not suggest a recovery action.
- Whether stdout was usable in a shell pipeline.
- Whether estimated versus actual/unknown cost was understood.
- Any step requiring observer intervention.
- Participant confidence from 1 to 5 after completing the flow.

## Exit criteria for beta

- At least 3 completed trials across at least 2 supported operating systems.
- At least 80% complete first route and first run without observer intervention.
- Median time to first successful run is under 10 minutes, excluding provider account creation or model download.
- No credential disclosure, unexpected file write, or unexplained external network request occurs.
- Every blocking failure has either a code fix or a documented recovery step before release.

The automated `scripts/onboarding-smoke.sh` test validates deterministic mechanics only. It does not replace these human trials.
