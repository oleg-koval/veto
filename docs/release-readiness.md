# Release readiness

This checklist separates first-beta publication gates from post-release
provider and human validation. A green build proves artifact mechanics, not
routing quality, provider access, or production readiness.

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
./scripts/package-release.sh v0.1.0 "${release_dist}"
```

The CI and release workflows run the same gates before artifacts are created.
The onboarding smoke test is deterministic and does not claim that a human
completed onboarding. The packaging dry run must produce exactly six platform
archives, `SHA256SUMS`, and `BINARY_SHA256SUMS`, then validate the native
artifact's version and offline doctor result.

## One-time release setup

Create a fine-grained GitHub token with read/write access to **Contents** for
only `oleg-koval/homebrew-tap`, then save it in the Veto repository as the
Actions secret `HOMEBREW_TAP_TOKEN`. The built-in `GITHUB_TOKEN` publishes the
Veto release; the separate token is used only for the cross-repository cask
update. When the tap token is missing, the workflow skips Homebrew publication
without weakening the GitHub release gates.

## v0.1.0 beta publication gates

- [x] Apache-2.0 license selected and committed.
- [x] Repository-authored history contains no third-party commit author; stop
  if contrary ownership or relicensing evidence appears.
- [ ] Race tests, vet, build, onboarding smoke, benchmark JSON validation, diff
  checks, and the local six-platform packaging dry run pass on the release
  commit.
- [ ] The pull request is mergeable, required checks pass, and no unresolved
  review threads remain.
- [ ] Tag the verified merge commit as `v0.1.0`; the release title and notes
  must identify it as beta while GitHub's prerelease flag remains false.
- [ ] Verify the live release has eight assets, both manifests validate, the
  extracted current-platform binary reports `veto 0.1.0`, and online
  `veto doctor --json` passes.
- [ ] Verify a temporary
  `go install github.com/oleg-koval/veto/cmd/veto@v0.1.0` reports `0.1.0` and
  identifies itself as a source build.

## Provider verification

For post-release account validation, run the account-level check with a
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
  systems using [onboarding-trial.md](onboarding-trial.md); record only
  aggregate timings, interventions, and failures.
- Run real-provider calibration with labeled outcomes before publishing
  routing-quality or savings claims. The offline benchmark reports mechanics
  and confidence metrics only.
- Confirm provider model IDs, availability, and pricing with release accounts.
  Catalog entries are configuration, not proof of account access or current
  pricing.
- Treat provider checks, calibration, and onboarding trials as beta evidence,
  not retroactive proof that v0.1.0 was production-ready.

## Publish and verify

After explicit owner approval:

```bash
make release RELEASE_VERSION=0.1.0
git tag -a v0.1.0 -m 'v0.1.0 beta'
git push origin v0.1.0
```

Keep the resulting claims separate: verify the GitHub Actions run and eight
release assets first. If `HOMEBREW_TAP_TOKEN` was configured, separately verify
the `Casks/veto.rb` update in `homebrew-tap` before claiming that distribution
path is available.
