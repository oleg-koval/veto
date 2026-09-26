# ADR-007: Keep Jev evidence-only in v0.14

## Status

Accepted for the v0.14 experimental branch on 2026-09-24.

## Context

Jev can answer multiple typed questions in one TypeSafe System One call and
returns choice probabilities, confidence, and token usage. That shape may make
Veto's sequential self-admission loop cheaper and faster, but launch claims and
synthetic fixtures do not establish routing quality, calibration, reliability,
or production cost for Veto workloads.

The live TypeSafe OpenAPI 0.2.0 read on 2026-09-23 documents bearer
authentication, `POST /v1/systemone`, named `choice`, `noul`, and `score`
questions, typed answers, and input/output usage. The launch article reports
70–500 ms service latency and $0.042 per million input tokens, but the API does
not return billed cost.

## Decision

Veto keeps `SequentialAdmissionEngine` authoritative. Jev implements the
separate `ShadowDecider` interface and runs beside the authority under an
independent timeout. Its prediction is recorded but cannot be returned from
`Manager.Route`. Any shadow or recorder failure is non-fatal to routing.

The experiment is disabled by default. Veto reads `TYPESAFE_API_KEY` only when
`VETO_EXPERIMENTAL_JEV_SHADOW=1` is set, never stores the key, and remains fully
usable without TypeSafe. Evidence excludes raw objectives and response bodies,
uses opaque candidate keys, and is evaluated offline under versioned promotion
policy v1.

## Consequences

- Shadow mode can add up to its bounded timeout to a fast authority decision,
  though it runs concurrently rather than serially.
- TypeSafe failures become dataset evidence rather than routing failures.
- Route-only records often remain unlabeled; execution and acceptance evidence
  is required for meaningful success/calibration metrics.
- Paired non-inferiority and calibration coverage must include at least 95% of
  authority-labeled routes, so divergent unlabeled choices cannot disappear
  from the gate. Sequential admission confidence is an explicit baseline proxy
  because the authority does not emit a task-success probability.
- Jev cost remains unknown until measured or an explicit price basis is
  represented separately from billed cost.
- v0.15 hybrid routing requires every policy-v1 gate to be evaluable and pass,
  plus an explicit later opt-in implementation. v0.14 never promotes itself.

## Sources

- TypeSafe API documentation: <https://api.typesafe.ai/docs>
- OpenAPI schema: <https://api.typesafe.ai/openapi.json>
- Launch article: <https://typesafe.ai/blog/introducing-system-one-models-and-jev>
