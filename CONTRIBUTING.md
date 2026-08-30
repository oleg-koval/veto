# Contributing to Veto

Veto is in public beta. Bug reports, provider compatibility notes, and focused
improvements to routing behavior are welcome.

## Structured contribution workflow

Use the repository issue forms for [bugs](.github/ISSUE_TEMPLATE/bug_report.yml),
[features](.github/ISSUE_TEMPLATE/feature_request.yml), and
[optimization proposals](.github/ISSUE_TEMPLATE/optimization_proposal.yml).
Every issue must state a concise summary, reproduction or context, expected and
actual behavior, scope, and a checklist of explicit acceptance criteria.

Pull requests must use the PR template. Link the issue, copy each acceptance
criterion into the evidence section, and include concrete verification for each
one. The repository check verifies that the fields and evidence are present; it
does not semantically prove that the criteria are complete. Maintainers make
that determination and retain responsibility for merge approval.

Contributor trust is classified by the versioned
[policy](.github/contributor-policy.json): whitelisted logins bypass reputation
lookup, blacklisted logins are blocked with the configured reason, and every
unlisted login is grey. Grey contributors need a same-repository issue with
acceptance criteria and pass the advisory GitHub reputation lookup; valid grey
PRs receive `needs-maintainer-review`. Login matching is case-insensitive. The
PR author is accountable; commit-author mismatches are reported separately for
maintainer review. Reputation data is only a review signal, never an automatic
quality judgment.

Release Please PRs are an explicit trusted-automation exception: the policy
matches the `github-actions` login and the repository's Release Please branch
prefix, then skips duplicate issue and acceptance-criteria checks because those
changes were governed in their source PRs. Other automation and bot PRs remain
subject to the normal checks.

The governance check fails closed when a grey contributor's issue or reputation
data cannot be read. Maintainers may override a classification by updating the
versioned policy in a reviewed PR, documenting the reason, and retaining the
normal issue, acceptance-criteria, and branch-protection checks. Blacklisted
PRs are commented on and blocked, not automatically closed.

## Before opening an issue

- Search existing issues first.
- Include the command, expected result, actual result, operating system, and
  Veto version.
- Redact API keys, credentials, provider responses, terminal history, and
  files under `~/.veto/`.
- For routing-quality reports, include the task kind and risk level, but remove
  proprietary task content.

## Before opening a pull request

Keep changes narrow and explain the user-visible behavior they affect. Add or
update tests when behavior changes, then run:

```bash
go test -race -timeout 120s ./...
go vet ./...
go build ./cmd/veto
./scripts/agent-skill-smoke.sh
release_dist=$(mktemp -d)
./scripts/package-release.sh v0.0.0 "${release_dist}"
```

Changes to installation, `doctor`, or release packaging should also extend the
fresh-home onboarding smoke and validate all six archives plus both checksum
manifests locally. Do not add a second packaging implementation to the GitHub
workflow; it must reuse `scripts/package-release.sh`.

Changes to model IDs, pricing, or provider behavior need current provider
evidence. Synthetic benchmark results may demonstrate routing mechanics, but
must not be presented as proof of production quality or savings.

## Reporting feedback from the CLI

`veto feedback` collects the same structured vocabulary as the issue forms,
saves a redacted report under `~/.veto/feedback/` with mode `0600`, and opens a
prefilled GitHub issue in the default browser. It never includes credentials,
raw task text, provider responses, terminal history, or `~/.veto/` contents by
default. Provider/model metadata is included only with explicit
`--include-provider` confirmation. Use flags for scripts, or pipe a report
object with `--stdin --json`.

The browser payload is bounded. If it is shortened, the complete redacted
report remains at the printed local path and can be attached manually. GitHub
sign-in is required for attribution; Veto never creates or uses a shared
account. If GitHub is unavailable, keep the local report and submit it later
after signing in. Post-run feedback is disabled by default and never appears
for non-interactive execution. Opt in with `"post_run_feedback": true` in
`~/.veto/config.json`; use `veto run --no-feedback` or
`veto exec --no-feedback` for an explicit per-run opt-out.

By contributing, you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
