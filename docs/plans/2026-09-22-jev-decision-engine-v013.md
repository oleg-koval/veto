# Veto v0.13: Provider-Neutral Decision Boundary

## Status

Approved implementation slice derived from the owner-approved Jev decision
engine roadmap. This file tracks local implementation progress; checked boxes
are not evidence of release, deployment, real-provider validation, or user
acceptance.

## Overview

Introduce a batch-capable, provider-neutral `DecisionEngine` boundary while
preserving v0.12 routing behavior exactly. This release does not integrate
TypeSafe, read `TYPESAFE_API_KEY`, add configuration, or make new network calls.

Target flow:

```text
hard filter -> adaptive rank -> bounded candidates -> DecisionEngine -> execution
                                                   |
                                                   +-> SequentialAdmissionEngine
                                                       (v0.13 default)
```

## Invariants

- Deterministic filters and user preferences remain authoritative.
- Sequential self-admission remains the only runtime behavior in v0.13.
- Existing three-attempt cap, checkpoint semantics, timeout behavior, store
  writes, error propagation, and event output remain compatible.
- The manager validates every engine selection against its eligible shortlist.
- Unknown telemetry is distinct from known zero.
- No TypeSafe code, key lookup, configuration, or request is added in v0.13.
- No tag, push, release, deployment, or external issue publication.

## Validation Commands

Run after each task as relevant and before completion:

```bash
go test -race -timeout 120s ./pkg/router ./internal/application ./cmd/veto
go test -race -timeout 120s ./...
go vet ./...
go build ./cmd/veto
go run ./cmd/veto benchmark --corpus internal/eval/testdata/routing_corpus.json
VETO_BINARY="$PWD/veto-v013" ./scripts/onboarding-smoke.sh
git diff --check
```

The fresh binary may be built as `go build -o ./veto-v013 ./cmd/veto` for the
smoke test and removed afterward; do not commit it.

### Task 1: Record the architecture decision

- [x] Add an ADR superseding ADR-001's rejection of a separate router model while preserving ADR-001 as historical context.
- [x] Document the provider-neutral batch boundary, legacy default/fallback, explicit future opt-in, and evidence limitations.
- [x] Update architecture and README references without claiming TypeSafe support is implemented.
- [x] Verify documentation links and run `git diff --check`.

### Task 2: Add decision-engine contracts

- [x] Add consumer-owned `DecisionEngine`, `DecisionRequest`, normalized candidate, outcome, probability, telemetry, mode, and reason contracts under `pkg/router`.
- [x] Validate empty, duplicate, and oversized candidate sets plus unknown selections, invalid versions, probabilities, and confidence.
- [x] Preserve explicit known/unknown telemetry semantics.
- [x] Add focused table-driven and race-safe contract tests.

### Task 3: Preserve self-admission behind a sequential engine

- [x] Implement `SequentialAdmissionEngine` using the existing `AdmissionGate`.
- [x] Refactor `Manager` to filter/rank, build the bounded engine request, call the engine, and validate the selected candidate.
- [x] Preserve ordered attempts, maximum three admission calls, skip/checkpoint behavior, store logging, events, timeouts, errors, and cancellation.
- [x] Add parity tests covering accept, reject, low confidence, parse failure, transport failure, timeout, cancellation, invalid selection, and no candidate.

### Task 4: Wire every composition path and version decision events

- [ ] Centralize or consistently update CLI, TUI, control-plane, plan, review, and test manager construction to use the sequential engine.
- [ ] Add additive versioned decision events and redacted ledger mappings while preserving legacy admission events and output compatibility.
- [ ] Verify normal, quiet, and JSON routing behavior plus old ledger/history compatibility.
- [ ] Update event-ledger and architecture documentation to match actual behavior.

### Task 5: Prove v0.13 parity and collect dogfood feedback

- [ ] Run the full race suite, vet, build, offline benchmark, onboarding smoke, and `git diff --check`.
- [ ] Compare the benchmark output and critical CLI behavior with the v0.12 baseline; explain any difference instead of silently accepting it.
- [ ] Inspect the complete branch diff for scope, privacy, compatibility, and absence of TypeSafe runtime behavior.
- [ ] Prepare local redacted Veto feedback reports for verified bugs, feature gaps, or optimization opportunities found during dogfooding; do not publish them.

## Completion Criteria

- [ ] All five tasks are committed on the feature branch.
- [ ] Required validation is green or honestly reported as unavailable/failed.
- [ ] Reviews find no unresolved critical or major defects.
- [ ] The main checkout branch and files remain untouched by implementation.
- [ ] No branch is pushed and no issue, tag, or release is published.
