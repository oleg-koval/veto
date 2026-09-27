# v0.14 follow-up: paired replay and execution preflight

## Scoped engineering slice

- [x] Filter Codex and OpenCode from `run`/`exec` admission when they cannot honor an explicit custom output-token limit; retain route-only behavior and provide a distinct no-compatible-runtime error.
- [x] Add an offline paired-replay core with explicit private trial inputs, separate temporary workspaces, randomized order, bounded contexts, injected runner/grader, and atomic redacted label output.
- [x] Test fake-runner isolation, label validation, privacy, no partial labels, and pre-admission filtering without provider calls.
- [x] Update user and architecture documentation and the dogfood issue ledger.

## Remaining gates, not authorized by this slice

- [ ] Establish an owner-approved private route-to-task and candidate-key mapping with provenance checks. Redacted shadow events cannot recreate it.
- [ ] Provide an authorized, egress-restricted provider runner and independent grader; review isolation and spending controls before any real calls.
- [ ] Collect real paired labels, TypeSafe latency/availability, and measured or explicitly based cost; apply promotion policy v2 and human acceptance.
- [ ] Consider v0.15 opt-in hybrid routing only after every promotion gate is evaluable and passes. Keep the no-TypeSafe path.

No live TypeSafe calls, automatic objective capture, account changes, v0.15 routing control, tag, or release are part of this slice.
