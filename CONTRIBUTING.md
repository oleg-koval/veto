# Contributing to Veto

Veto is in public beta. Bug reports, provider compatibility notes, and focused
improvements to routing behavior are welcome.

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

By contributing, you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
