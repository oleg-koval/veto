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

Readers accept benign additive unknown JSON fields within schema version 1 so
newer writers can add optional data. They fail closed on sensitive field names
at any nesting depth and reject unknown schema versions, event
types, invalid enums, non-canonical unknown values, duplicate routes, orphaned
labels, and labels for candidates that were not offered. A future incompatible
shape requires a new schema version and an explicit reader migration.

`pkg/shadow/testdata/shadow_v1.jsonl` is a synthetic, labeled replay fixture.
It is safe to evaluate offline and proves agreement, success, latency, cost,
calibration, availability, and simulated-fallback metrics without loading
credentials or contacting a provider. Synthetic results validate the harness,
not Jev quality and not production readiness.

## Promotion policy v1

`veto shadow-report` returns `insufficient_data`, `not_ready`, or
`ready_for_opt_in_experiment`. Missing labels or known measurements are never a
pass. Readiness requires at least 500 labeled decisions across five task kinds
and 30 samples per represented kind; paired labels covering at least 95% of
authority-labeled routes; a paired Jev-minus-authority success-rate 95%
normal-approximation lower bound of at least -0.02; no task-kind mean worse
than -0.05; Jev Brier score no worse than authority and expected calibration
error at most 0.05; p95 latency at most 750 ms with 95% coverage; at most 1%
shadow errors/unavailability; 95% cost coverage and a shadow/authority average
cost ratio at most 0.10; and a simulated hybrid fallback rate at most 20% while
meeting the same success constraints.

Calibration uses a decision's explicit task-success probability when present.
Sequential admission does not expose one, so its accepted-decision confidence
is the documented baseline proxy; this limitation is one reason passing only
permits an experiment. The normal approximation is an engineering gate, not a scientific claim; a
later policy version may replace it without changing evidence schema v1.
Privacy and zero-influence gates are structural assertions backed by the
redacted DTO and the separate `ShadowDecider`/`DecisionEngine` types. Passing
does not enable hybrid routing automatically.

Sequential admission telemetry uses provider-reported usage/cost and observed
latency across every attempted admission call. Jev usage and latency come from
the TypeSafe response and local observation. TypeSafe's API does not return a
billed cost, so Jev cost remains unknown in this release rather than applying a
marketing price as measured spend. Consequently the cost gate remains
`insufficient_data` until an explicit cost basis is added or the API reports
cost.
