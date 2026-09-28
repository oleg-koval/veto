# Private paired-replay provenance slice

## Decision

Use an explicit route-time witness plus a separately supplied private manifest.
Post-hoc manual key mapping cannot prove which model an opaque candidate key
represented because the router's HMAC secret is process-local. Automatic raw
task capture would cross the current privacy boundary. The witness records only
the route's derived key, key-to-model-identity bindings with the known tool
snapshot, and a keyed task fingerprint;
it is not emitted to the redacted JSONL. The manifest holds the consented
frozen task outside Git.

## Completed local gates

- [x] Add an optional private witness recorder to the shadow engine; leave default production composition unchanged.
- [x] Bind route-time candidate keys to provider/model/runtime identity and the known tool snapshot, plus task kind/risk, observation timestamp, and replayable task fingerprint.
- [x] Validate the manifest against a materialized route before executing either candidate.
- [x] Save and load bounded private manifests with 0600 files, trusted stable private directories, no overwrite, and repository-path rejection. Pin the checked directory; leave failed partial writes for manual cleanup.
- [x] Prove the route-to-manifest-to-label flow with fake decision engines, fake runner, and blind fake grader.

## Still required before real paired evidence

- [x] Add a dedicated, explicitly opted-in local capture workflow for route-time witnesses and replayable task manifests, with private permissions and bounded retention. A manifest validator cannot authenticate a manually forged witness.
- [x] Review the local capture boundary before any pilot: capture is explicitly enabled, warned at startup, limited to direct user runs with criteria and output budgets; raw objectives/criteria remain local, private-permissioned, and bounded by retention. Route-time identity and known-tool snapshots are replay inputs, and the replay contract requires an injected runner to verify them.
- [ ] Resolve pre-pilot gaps: there is no authorized egress-restricted provider runner, no established independent grader implementation, and the operator must choose/authorize endpoints and a spending cap. The grader interface is blind by contract, but independence is not proven by that interface alone.
- [ ] Authorize model endpoints and a spending cap, then add an egress-restricted runner; no provider calls are authorized by this slice.
- [ ] Run a small labeled pilot before scaling to promotion policy v2. Keep Jev non-authoritative until every gate passes.

Raw objectives are not captured by default; only the explicit private-capture
opt-in stores eligible direct-user task text. No TypeSafe account or credential
setting is changed, and no hybrid routing, tag, or release is part of this
slice.
