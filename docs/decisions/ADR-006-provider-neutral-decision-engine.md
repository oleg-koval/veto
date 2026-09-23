# ADR-006: Provider-neutral decision-engine boundary

## Status

Accepted. Supersedes the narrow "separate router model" rejection in
[ADR-001](ADR-001-self-admitting-receivers.md); ADR-001 otherwise stands and is
retained as historical context. The decision is accepted; implementation of the
boundary is planned, not present in the current runtime.

## Date

2026-09-23

## Context

Through v0.12, the `Manager` hard-codes one selection strategy: ask each
surviving candidate for a self-admission decision, one at a time, in rank order,
and take the first accept at ≥ 70% confidence (ADR-001). The strategy preserves
rank order, but it is inseparable from the manager — there is no seam at which
a different selection strategy could be introduced or evaluated.

The owner-approved decision-engine roadmap, sliced for v0.13 in
[`docs/plans/2026-09-22-jev-decision-engine-v013.md`](../plans/2026-09-22-jev-decision-engine-v013.md),
wants that seam: a boundary where a bounded, already-filtered candidate set is
handed to a decision engine that returns one selection. The boundary must be
batch-capable (the engine sees the whole eligible shortlist at once rather than a
stream) and provider-neutral (it depends on normalized candidate and outcome
contracts, not on any provider transport or a specific future engine).

ADR-001 rejected a "separate router model" — a second LLM consulted on every
routing call to decide which model should do the work. That rejection was aimed
at an unconditional LLM oracle: extra latency, extra cost, and a second model's
failure modes on every call. It was not a decision to fuse the one supported
strategy into the manager forever. A pluggable boundary that still defaults to
self-admission does not reintroduce the oracle ADR-001 rejected.

## Decision

Plan a consumer-owned `DecisionEngine` boundary in `pkg/router`, consistent
with [ADR-002](ADR-002-multi-provider-executor-interface.md). The manager will
apply deterministic filters and user preferences, rank candidates as before,
build a bounded, normalized decision request from the eligible shortlist, hand
it to the engine, and validate that the engine's selection is one of the
candidates it offered. Provider transports stay outside this contract.

The planned v0.13 implementation is `SequentialAdmissionEngine`, which will wrap
the existing `AdmissionGate` and preserve ADR-001 behavior exactly: ordered
attempts, the first accept at ≥ 70% confidence, at most three admission calls
including transport failures, and the existing skip, checkpoint, timeout, error,
and event semantics. Sequential self-admission remains current behavior and is
required to remain the default and only runtime behavior in the v0.13 slice.

The planned request contract is batch-capable so a future engine can evaluate
the whole shortlist in one decision request; it does not imply parallel
admission calls. The sequential engine will still ask in order and stop at the
first accept. Legacy self-admission is the default/fallback strategy when no
alternative engine is explicitly enabled. Any alternative engine requires
explicit future opt-in and later wiring; v0.13 adds no engine-selection
configuration or automatic fallback from a new engine. There is no Jev engine
or TypeSafe integration in the current runtime, and neither is part of this
v0.13 slice.

## What this supersedes

This ADR narrows ADR-001's "A separate 'router model'" rejection: a pluggable
decision boundary is now permitted, provided the default remains self-admission
and the manager validates every selection against the shortlist it offered.
ADR-001's reasoning still governs any attempt to make a second LLM the
unconditional, always-on router — that remains rejected. ADR-001 is unchanged
otherwise and kept as historical context.

## Alternatives considered

### Keep the strategy fused into the manager

Rejected: leaves no seam to introduce or evaluate an alternative selection
strategy without editing the manager's core loop, which is exactly the coupling
the roadmap sets out to remove.

### Build the future engine (Jev / TypeSafe) now

Rejected for v0.13: it has no validation, would add configuration, a key lookup,
and new network calls, and is out of scope for a release whose goal is only to
introduce the boundary while preserving v0.12 behavior exactly. The boundary
is planned first; an alternative engine behind it is a separate, later, opt-in
decision.

### Reintroduce a router LLM as the default

Rejected: this is the oracle ADR-001 rejected — per-call latency and cost and a
second model's failure modes on every route. The boundary keeps self-admission
as the default precisely to avoid that.

## Expected consequences

**Positive:**

- The planned boundary will provide a provider-neutral seam for a future
  decision engine without touching provider transports or the filter/rank stages.
- The v0.13 implementation must preserve ADR-001 behavior; parity remains to
  be demonstrated by the later implementation and validation tasks.
- The manager must validate the engine's selection against the eligible shortlist,
  so a misbehaving engine cannot select a candidate that was never offered.
- Normalized telemetry must keep known, unknown, and zero usage/cost distinct
  across the boundary, matching the rest of Veto.

**Negative:**

- One more interface and one request/outcome mapping to maintain, for a boundary
  with only one planned v0.13 implementation.
- A future batch engine could regress the deterministic rank-order guarantee if
  it ignored rank; the manager's selection-validation and the ranked request
  bound this but do not by themselves force rank-order selection.

## Evidence limitations

The checked-in offline corpus validates routing mechanics only; there is no
real-provider or batch-engine validation behind this boundary. The batch
decision boundary and sequential wrapper are not yet implemented.
Jev is not implemented, and the approved v0.13 scope excludes TypeSafe code,
key lookup (including `TYPESAFE_API_KEY`), configuration, and requests. Sequential
self-admission remains the current runtime behavior. Checked boxes in the v0.13
plan track local implementation progress, not release, deployment, real-provider
validation, or user acceptance.
