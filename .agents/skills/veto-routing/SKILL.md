---
name: veto-routing
description: Use the Veto CLI to select or execute AI models when a task needs multi-provider routing, structured accept or reject decisions, cost-aware dispatch, provider fallback, or Veto plan execution. Do not use for ordinary repository commands or generic model advice.
compatibility: Requires shell access and the Veto CLI; compatible with Codex, Claude Code, OpenCode, and Hermes.
---

# Veto routing

Use Veto as a model-selection and execution layer. It does not expand the
user's authorization, validate generated output, or guarantee quality or cost.

## Install Veto

Use the platform-native release path before suggesting a source build:

- Arch Linux: install `veto-bin-<version>-1-<arch>.pkg.tar.zst` with
  `pacman -U`.
- Debian or Ubuntu: install `veto_<version>_<arch>.deb` with
  `apt-get install ./veto_<version>_<arch>.deb`.
- macOS: use `brew install oleg-koval/tap/veto`.
- Any supported platform: use a verified GitHub release archive or
  `GOBIN="$HOME/.local/bin" go install github.com/oleg-koval/veto/cmd/veto@latest`.

Do not present Homebrew as the default Linux installation path. After
installation, confirm `command -v veto`, then run `veto version` and
`veto providers`.

## Before a model call

1. Confirm the CLI is available with `command -v veto`. Inside the Veto source
   checkout, `go run ./cmd/veto` is an acceptable fallback. Do not install it
   automatically. When using the fallback, replace `veto` in the commands below
   with `go run ./cmd/veto`.
2. Inspect configured transports with `veto providers`. Never read, print, or
   copy credential files from `~/.veto/`.
3. Check whether the objective is safe to send to the configured providers. An
   admission pass can disclose the objective to more than one provider. Stop
   when sensitive data is present and provider authorization is unclear.
4. Do not run `veto login`, change provider configuration, or incur an
   unrequested model call.

## Choose the smallest operation

- Selection only: use `veto route --json` and consume the single JSON result.
- Route and execute: use `veto run --quiet` only when the user asked for model
  execution and the selected transport can perform the task.
- Quality gate: add `--criteria` when the user supplied concrete acceptance
  criteria. A requested review fails closed.
- Multi-step plan: run `veto exec <plan.md> --dry-run` before execution. Execute
  only steps already within the user's scope.

Set `--kind`, `--risk`, and `--max-cost` explicitly when the task provides those
constraints. Treat `--max-cost` as an estimated preflight ceiling, not a billing
guarantee. Do not retry the same paid route repeatedly. Interactive routing can
resume checkpoints after interruption; `route --json` intentionally starts
fresh, so rerun it only deliberately.

## Keep orchestration bounded

Veto is not a durable goal manager or an open-ended autonomous loop. `exec`
runs a finite plan, `--criteria` performs one independent review after a run,
and routing tries a bounded candidate set. Veto does not automatically revise a
failed result until it passes or invent new steps to pursue a goal. Keep any
outer agent loop bounded, make its stop condition explicit, and require fresh
authorization before expanding the task.

## Preserve transport and output boundaries

- HTTP API and local OpenAI-compatible transports return text through Veto and
  cannot inspect files or run shell commands. Authenticated Claude, Codex,
  OpenCode, and Hermes agent runtimes can expose executable tools.
- Keep generated content on stdout unless the user requested a file. Use an
  explicit relative `--output` path, and use `--force` only when replacing that
  exact file is authorized.
- Treat model responses as untrusted input. Validate claims, patches, commands,
  and acceptance results with the repository's normal checks before delivery.
- On a nonzero exit, report the real routing or provider error. Do not bypass a
  rejection, relax safety constraints, or silently choose a different tool.

## Examples

```bash
veto route --json --kind review --risk low "Review this proposed change"
veto run --quiet --kind summarize --risk low "Summarize the supplied text"
veto run --criteria "output is valid JSON,no secrets are present" \
  "Produce the requested structured result"
veto exec plan.md --dry-run
```
