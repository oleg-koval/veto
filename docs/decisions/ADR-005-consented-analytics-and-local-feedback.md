# ADR-005: Local-first diagnostics and explicit opt-in remote analytics

## Status

Accepted

## Date

2026-08-31

## Context

Veto needs useful evidence about routing quality and failures, but it handles
developer tasks and provider integrations where prompts, repository details,
credentials, and provider output may be sensitive. Veto already has a local
redacted event ledger and a user-initiated feedback flow. It has no analytics
backend or published remote data contract.

## Decision

- Keep the event ledger local, bounded, redacted, and separate from product
  analytics.
- Keep `veto feedback` as a separate user-initiated issue-reporting path.
- Default future remote sharing to opt-out (`not set` behaves as opt-out).
- Provide `veto analytics enable|disable|status` as the explicit user control.
- Do not add a network transport until its endpoint, payload, retention,
  deletion, network-metadata, and legal-review contracts are documented.
- Require a future transport to fail closed unless the stored opt-in matches the
  current policy version.
- Use the existing event schema as the source for a future TUI, rather than
  adding a second private state stream.

## Alternatives considered

### Collect anonymous analytics by default

Rejected: a command-line tool handles private developer context, and network
metadata can still identify a user even when the JSON payload has no name or
email. Default collection would also make the user's approval too late.

### Send raw local ledger files after opt-in

Rejected: the ledger is redacted for local diagnostics, not designed as a
remote-sharing contract. It contains event-level correlations and model data
that should be aggregated and reviewed before export.

### Use feedback reports as product analytics

Rejected: feedback is intentional, contextual user communication. Treating it
as silent usage measurement would violate the user's expectation and mix two
different purposes.

## Consequences

Users get useful local diagnostics now and a visible preference control without
surprise network traffic. Veto cannot claim global usage or routing-calibration
data until a reviewed transport exists and users opt in. The future TUI can
replay the same redacted events used by the logger, keeping presentation and
data contracts aligned.
