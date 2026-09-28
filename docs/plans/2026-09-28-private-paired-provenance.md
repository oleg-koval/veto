# Private paired-replay provenance slice

## Decision

Use an explicit route-time witness plus a separately supplied private manifest.
Post-hoc manual key mapping cannot prove which model an opaque candidate key
represented because the router's HMAC secret is process-local. Automatic raw
task capture would cross the current privacy boundary. The witness records only
the route's derived key, key-to-model bindings, and a keyed task fingerprint;
it is not emitted to the redacted JSONL. The manifest holds the consented
frozen task outside Git.

## Completed local gates

- [x] Add an optional private witness recorder to the shadow engine; leave default production composition unchanged.
- [x] Bind route-time candidate keys, task kind/risk, observation timestamp, and replayable task fingerprint.
- [x] Validate the manifest against a materialized route before executing either candidate.
- [x] Save and load bounded private manifests with 0600 files, trusted stable private directories, no overwrite, and repository-path rejection. Pin the checked directory; leave failed partial writes for manual cleanup.
- [x] Prove the route-to-manifest-to-label flow with fake decision engines, fake runner, and blind fake grader.

## Still required before real paired evidence

- [ ] Add a dedicated, owner-approved route-time witness storage workflow. A manifest validator cannot authenticate a manually forged witness.
- [ ] Review consent, private retention, tool parity, and grader independence for a frozen corpus.
- [ ] Authorize model endpoints and a spending cap, then add an egress-restricted runner; no provider calls are authorized by this slice.
- [ ] Run a small labeled pilot before scaling to promotion policy v2. Keep Jev non-authoritative until every gate passes.

No raw objective is captured automatically by the CLI, no TypeSafe account or
credential setting is changed, and no hybrid routing, tag, or release is part
of this slice.
