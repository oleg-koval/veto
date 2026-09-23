# Jev shadow evidence

Veto v0.14 uses an append-only JSONL stream for experimental decision-engine
comparisons. Schema version 1 has two events: `route_comparison`, followed
optionally by one or more `execution_label` events. The offline materializer
uses the latest label for a route/candidate pair so a corrected label does not
require rewriting history.

The persisted Go types deliberately cannot represent task objectives,
constraints, credentials, provider response bodies, free-form explanations,
filesystem paths, or account identifiers. Routes contain opaque bounded
candidate keys, task kind, risk, normalized decisions, machine error codes,
optional bounded strategy identifiers, and known/unknown telemetry. Labels contain only normalized outcome and
telemetry values. Unknown values use their explicit `known: false` form and a
canonical zero value; a measured zero uses `known: true`.

Readers accept additive unknown JSON fields within schema version 1 so newer
writers can add optional data. They reject unknown schema versions, event
types, invalid enums, non-canonical unknown values, duplicate routes, orphaned
labels, and labels for candidates that were not offered. A future incompatible
shape requires a new schema version and an explicit reader migration.

`pkg/shadow/testdata/shadow_v1.jsonl` is a synthetic, labeled replay fixture.
It is safe to evaluate offline and proves agreement, success, latency, cost,
calibration, availability, and simulated-fallback metrics without loading
credentials or contacting a provider. Synthetic results validate the harness,
not Jev quality and not production readiness.
