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

Run the packaging dry run for the intended version:

```bash
make release-check RELEASE_VERSION=0.1.0
```

This validates the matching [`CHANGELOG.md`](../CHANGELOG.md) section and
builds every release archive without publishing it.

## One-time release setup

Create a fine-grained GitHub token with read/write access to **Contents** for
only `oleg-koval/homebrew-tap`, then save it in the Veto repository as the
Actions secret `HOMEBREW_TAP_TOKEN`. The built-in `GITHUB_TOKEN` publishes the
Veto release; the separate token is used only for the cross-repository cask
update. The release workflow fails before building or publishing when the tap
token is missing.

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
- Add the intended version and user-facing notes to `CHANGELOG.md`, then tag and
  publish only after the preceding gates are signed off. The release workflow
  publishes those notes, checksummed archives, and the Homebrew cask, but it
  cannot make the owner decisions above.

## Publish and verify

After explicit owner approval:

```bash
make release RELEASE_VERSION=0.1.0
git tag -a v0.1.0 -m 'v0.1.0'
git push origin v0.1.0
```

Keep the resulting claims separate: verify the GitHub Actions run and release
assets first, then the `Casks/veto.rb` update in `homebrew-tap`, and finally
fresh installs through both distribution paths:

```bash
GOPROXY=https://proxy.golang.org go install github.com/oleg-koval/veto/cmd/veto@v0.1.0
brew update
brew install --cask oleg-koval/tap/veto
veto version
```
