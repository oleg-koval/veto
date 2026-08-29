# Veto agent guide

## Project purpose

Veto is a Go CLI that filters and ranks configured AI models, asks viable
candidates for a structured admission decision, and optionally executes the
task with the first model that accepts at sufficient confidence.

Read `README.md` for the user contract and `docs/architecture.md` before
changing routing, execution, provider, persistence, or safety behavior.

## Use Veto from an agent

This repository ships the discoverable `$veto-routing` skill at
`.agents/skills/veto-routing/SKILL.md`. Use it when a task calls for:

- selecting among configured models or providers;
- cost-aware or capability-aware model dispatch;
- a machine-readable routing decision;
- Veto plan execution or acceptance-criteria review.

Do not invoke Veto for ordinary shell commands, builds, or deterministic local
analysis. A routing call can send the task objective to multiple configured
providers, so never include credentials, secrets, private source, or proprietary
task content without authorization. Do not install Veto, run `veto login`, or
change `~/.veto/` configuration unless the user explicitly requests it.

## Engineering invariants

- Admission and execution are separate contracts. Admission stays short and
  structured; execution uses its own bounded output budget.
- Derive tools from the active transport. HTTP API and local OpenAI-compatible
  transports are text-only; do not advertise file or shell access for them.
- Treat preflight cost ceilings and model confidence as estimates. Preserve
  known, unknown, and zero usage/cost as distinct states.
- Requested acceptance reviews fail closed when unavailable, malformed,
  incomplete, or inconsistent.
- Objective text alone never authorizes a file write. Keep `--output` explicit,
  relative, traversal-safe, private, and overwrite-protected without `--force`.
- Load executor skills only from approved sources. Do not weaken the approval
  boundary around external skill directories.
- Keep model metadata centralized and update drift tests and user documentation
  when provider IDs, capabilities, or pricing change.

## Development commands

Use `rtk proxy` when it is installed; otherwise run the equivalent Go command
directly.

```bash
rtk proxy go test -race -timeout 120s ./...
rtk proxy go vet ./...
rtk proxy go build ./cmd/veto
```

For onboarding and routing changes, also run a fresh binary through:

```bash
VETO_BINARY=/path/to/fresh/veto ./scripts/onboarding-smoke.sh
go run ./cmd/veto benchmark --corpus internal/eval/testdata/routing_corpus.json
```

Use fake providers and temporary homes in automated tests. For account-level
catalog verification, use `veto verify-models`; do not replace its redacted raw
capture workflow with ad hoc HTTP calls.

## Delivery boundaries

Keep local tests, real-provider verification, human onboarding trials, release
publication, and production use as separate claims. Do not tag, publish a
release, contact users, or expose provider-account captures without explicit
owner approval.
