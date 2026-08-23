# Release readiness

This checklist separates what CI can prove from what requires an owner or a
real provider account. A green build is not a substitute for the external
gates.

## Automated gates

Run these from a clean checkout:

```bash
go test -race -timeout 120s ./...
go vet ./...
go build ./cmd/veto
VETO_BINARY="$PWD/veto" ./scripts/onboarding-smoke.sh
go run ./cmd/veto benchmark --corpus internal/eval/testdata/routing_corpus.json > /tmp/veto-benchmark.json
python3 -m json.tool /tmp/veto-benchmark.json >/dev/null
```

The CI and release workflows run the same gates before artifacts are created.
The onboarding smoke test is deterministic and does not claim that a human
completed onboarding.

## Provider verification

For each provider intended for release, run the account-level check with a
credential supplied through the environment or `veto login`:

```bash
veto verify-models --provider openai --json
veto verify-models --provider anthropic --json
veto verify-models --provider openrouter --json
veto verify-models --provider xai --json
```

Run one provider at a time. The command persists the raw response and
non-secret request metadata in `artifacts/http/` and exits nonzero on a failed
request or a missing catalog ID. Review the JSON output and retain the capture
only if its account-visible model inventory may be shared safely.

## Owner-gated release decisions

- [x] Select and add the [Apache-2.0 license](../LICENSE); before publishing,
  confirm that all original code is owned or properly relicensed.
- Complete at least three fresh-user trials across at least two operating
  systems using [onboarding-trial.md](onboarding-trial.md); record only the
  aggregate timings, interventions, and failures.
- Run real-provider calibration with labeled outcomes before publishing
  routing quality or savings claims. The offline benchmark reports mechanics
  and confidence metrics only.
- Confirm the exact provider model IDs and pricing visible to the release
  account. Catalog entries are configuration, not proof of account access or
  current pricing.
- Tag and publish only after the preceding gates are signed off. The release
  workflow creates checksummed archives and refuses malformed tags, but it
  cannot make the owner decisions above.
