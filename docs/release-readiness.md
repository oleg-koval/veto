# Release readiness

This checklist separates automated release mechanics from post-release provider
and human validation. A green build proves artifact mechanics, not routing
quality, provider access, or production readiness.

## Automated gates

Run these from a clean checkout:

```bash
go test -race -timeout 120s ./...
go vet ./...
go build ./cmd/veto
VETO_BINARY="$PWD/veto" ./scripts/onboarding-smoke.sh
go run ./cmd/veto benchmark --corpus internal/eval/testdata/routing_corpus.json > /tmp/veto-benchmark.json
python3 -m json.tool /tmp/veto-benchmark.json >/dev/null
release_dist=$(mktemp -d)
./scripts/package-release.sh v0.0.0 "${release_dist}"
./scripts/render-homebrew-formula.sh v0.0.0 "${release_dist}/SHA256SUMS" "${release_dist}/veto.rb"
```

The CI and release workflows run the same gates before artifacts are created.
The onboarding smoke test is deterministic and does not claim that a human
completed onboarding. The packaging dry run must produce exactly six platform
archives, `SHA256SUMS`, and `BINARY_SHA256SUMS`, then validate the native
artifact's version and offline doctor result. CI also renders the Homebrew
formula from the archive checksum manifest.

## Current beta status

The `v0.6.3` beta was published with eight archives, `SHA256SUMS`,
`BINARY_SHA256SUMS`, native official binaries, and tagged Go installation
support. The release workflow, checksums, and Homebrew formula update were
verified on 2026-08-31. Provider access, calibration, and fresh-user
onboarding remain owner-gated beta validation.

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

## Post-release beta validation

- Complete at least three fresh-user trials across at least two operating
  systems using [onboarding-trial.md](onboarding-trial.md); record only the
  aggregate timings, interventions, and failures.
- Run real-provider calibration with labeled outcomes before publishing
  routing quality or savings claims. The offline benchmark reports mechanics
  and confidence metrics only.
- Confirm the exact provider model IDs and pricing visible to the release
  account. Catalog entries are configuration, not proof of account access or
  current pricing.
- Treat provider checks, calibration, and onboarding trials as beta evidence,
  not retroactive proof that the release was production-ready.
