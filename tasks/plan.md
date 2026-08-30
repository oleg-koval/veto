# Implementation Plan: Veto User-Ready Harness

## Overview

Harden veto from a routing prototype into an honest, measurable user harness. Work proceeds risk-first: correct execution semantics, then adaptive routing and telemetry, then safety and quality gates, then evaluation, onboarding, and beta distribution. Existing uncommitted model-catalog changes are preserved.

## Architecture Decisions

- Keep `Executor.Run` as the short admission probe and add a distinct execution contract with an explicit output-token budget.
- Derive usable tools from the active executor transport before hard filtering; HTTP chat transports are text-only until a real tool loop exists.
- Use persisted, task-kind-aware store signals for ranking, with backward-compatible loading of old history.
- Treat cost ceilings as estimates unless a provider supplies usage and enforces the requested output limit; never describe them as absolute guarantees.
- Centralize execution/result recording so `run`, `exec`, review, plan conversion, and skill generation cannot drift.
- Default quality gates to fail closed and file writes to explicit, overwrite-safe behavior.
- Keep CI evaluation deterministic and offline; real-provider calibration remains opt-in and cannot be claimed from synthetic data.
- Do not publish a tag or GitHub release without explicit user approval.

## Dependency Graph

Execution contract -> provider execution -> CLI execution -> execution telemetry

History schema -> kind-aware signals -> adaptive ranking -> offline evaluation

Safe CLI foundations -> onboarding smoke test -> beta release workflow

Authoritative catalog -> documentation consistency -> beta release

## Task List

### Phase 1: Truthful execution foundation

- [x] Task 1: Add separate admission and execution contracts with output metadata.
- [x] Task 2: Implement bounded full execution for Anthropic, OpenAI-compatible, and CLI transports.
- [x] Task 3: Route all task execution through the new contract and expose `--max-output-tokens`.
- [x] Task 4: Derive effective tool capabilities from executor transports before filtering.

### Checkpoint: Execution

- [x] Admission remains capped at 512 tokens.
- [x] API/local execution supports outputs above 512 tokens and reports truncation.
- [x] Tool-required tasks exclude text-only transports before admission.
- [x] Race tests, vet, and build pass.

### Phase 2: Adaptive routing and observability

- [x] Task 5: Extend history with kind-aware execution metrics and backward-compatible persistence.
- [x] Task 6: Rank using persisted history and record every execution outcome exactly once.
- [x] Task 7: Parse provider usage, calculate known actual cost, and report estimated/actual/unknown cost honestly.

### Checkpoint: Adaptive routing

- [x] A saved history file changes ranking in a fresh manager.
- [x] Unknown usage/cost is distinct from known zero.
- [x] Success, error, timeout, latency, usage, cost, and optional review score round-trip.
- [x] Race tests, vet, and build pass.

### Phase 3: Safety and trustworthy quality gates

- [x] Task 8: Make root help conventional and side-effect free.
- [x] Task 9: Replace objective-inferred writes with explicit `--output` and overwrite protection.
- [x] Task 10: Make requested acceptance-criteria reviews fail closed and schema-consistent.
- [x] Task 11: Remove remote `curl | sh` installation behavior.

### Checkpoint: Safety

- [x] Help exits zero without loading credentials or contacting providers.
- [x] Objective text alone cannot write a file; explicit writes cannot overwrite without `--force`.
- [x] Missing, malformed, or incomplete requested reviews fail non-zero.
- [x] No remote script is piped into a shell.

### Phase 4: Evidence and onboarding

- [x] Task 12: Add a deterministic offline corpus comparing cheapest, strongest, static, and adaptive policies.
- [x] Task 13: Add a fresh-home onboarding smoke harness with fake local providers and no real credentials.
- [x] Task 14: Centralize model metadata and add drift/consistency tests.

### Checkpoint: Evidence

- [x] Corpus emits machine-readable success, quality, cost, latency, overhead, and calibration metrics.
- [x] CI smoke evaluation is deterministic and network-free.
- [x] Fresh-home tests cover help, providers, route/run, output safety, review outcomes, and permissions.

### Phase 5: Beta distribution

- [x] Task 15: Harden release workflow with pre-publish gates, normalized versions, archives, SHA-256 checksums, and Linux arm64.
- [x] Task 16: Update README, architecture, ADR, install/upgrade/uninstall, safety, and evaluation-limit documentation.
- [x] Task 17: Prepare a manual fresh-user trial protocol; record external trials as pending until real users complete them.
- [x] Task 18: Add an opt-in account-level model verification command with redacted, reproducible response captures.

### Phase 6: Reliable agentic repository execution

- [x] Task 19: Infer executable-runtime requirements from explicit repository mutation objectives and filter text-only transports before admission.
- [x] Task 20: Make Claude subscription admission structured, customization-free, and independently timeout-bounded.
- [x] Task 21: Give agentic `run` executions a practical default deadline and cover the exact PR-fix flow with a fake CLI integration test.
- [x] Task 22: Require live inline-thread verification for pull-request review-fix executions.
- [x] Task 23: Keep batch PR-review remediation off small-model routing after demonstrated false completion.
- [x] Task 24: Allow enough wall time for repositories with mandatory test and AI-review push gates.
- [x] Task 25: Kill the complete subscription CLI process tree when a run times out.
- [x] Task 26: Register an authenticated Codex CLI as a native ChatGPT-subscription agent runtime.

### Checkpoint: Agentic execution

- [x] The reported PR-fix objective qualifies executable runtimes only.
- [x] Claude admission returns schema-valid JSON without project hooks, skills, or tools.
- [x] Codex subscription admission is isolated and full execution retains the caller's repository policy.
- [x] PR review-fix execution cannot infer zero findings from summary views that omit inline threads.
- [x] Focused tests, race suite, vet, build, onboarding smoke, and the exact live command pass.

### Checkpoint: Complete

- [x] Full race suite, vet, build, offline evaluation, and onboarding smoke tests pass.
- [x] Working tree diff is reviewed for scope and existing user changes remain intact.
- [ ] Remaining external validation and release approval are explicitly reported.
- [x] Provider verification is implemented; live account checks remain an owner-run release gate.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Shared executor interface causes broad churn | High | Keep admission interface stable; add execution capability separately. |
| Missing usage is treated as free usage | High | Persist explicit known/unknown flags. |
| Synthetic benchmark is mistaken for product proof | High | Label it mechanics validation; require real labeled trials for calibration claims. |
| Historical outcomes poison unrelated task kinds | Medium | Key statistics by model and kind with model-level legacy fallback. |
| Existing local models lose claimed tools | Medium | Document intentional correction; only advertise tools an executor can invoke. |
| Mutation inference accidentally excludes content-only tasks | Medium | Require explicit repository side-effect signals and keep ordinary code generation text-capable. |
| Claude customizations corrupt admission output | High | Use safe mode, disabled tools, no persistence, and native JSON schema only for admission. |
| Agentic execution exceeds the old two-minute deadline | High | Separate a short admission deadline from a practical total run deadline; keep both user-configurable. |
| Release workflow creates external state | High | Prepare workflow only; require explicit approval before tag/publish. |
| User-owned dirty changes are overwritten | High | Avoid reverting or replacing current catalog edits; inspect overlaps before every patch. |

## Open Questions Deferred Without Blocking Implementation

- Apache-2.0 was selected and added after owner approval; review third-party code ownership before public beta.
- Real provider model IDs and prices require live official-source verification before release.
- Confidence calibration requires labeled real executions; the repository can provide the collection and analysis harness only.
- Account-level model verification requires credentials and is intentionally opt-in; this repository cannot run or attest those checks without the owner's provider accounts.
