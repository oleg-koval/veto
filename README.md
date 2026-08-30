<h1 align="center">veto</h1>

<p align="center">
  <a href="https://github.com/oleg-koval/veto/actions/workflows/ci.yml"><img src="https://github.com/oleg-koval/veto/actions/workflows/ci.yml/badge.svg" alt="Build and test status"></a>
  <a href="https://goreportcard.com/report/github.com/oleg-koval/veto"><img src="https://goreportcard.com/badge/github.com/oleg-koval/veto" alt="Go Report Card"></a>
  <a href="https://github.com/oleg-koval/veto/releases/latest"><img src="https://img.shields.io/github/v/release/oleg-koval/veto" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache--2.0-blue.svg" alt="Apache-2.0 license"></a>
</p>

<p align="center">
  <a href="https://oleg-koval.github.io/veto/"><img src="./site/assets/logo.svg" width="120" height="120" alt="Veto logo"></a>
</p>

<p align="center">
  Cost-aware AI model routing with explicit model admission.<br>
  <strong>Stop hardcoding which AI model gets every task.</strong>
</p>

<p align="center">
  <a href="https://oleg-koval.github.io/veto/">Website</a> ·
  <a href="https://github.com/oleg-koval/veto/releases/latest">Latest release</a> ·
  <a href="docs/architecture.md">Architecture</a>
</p>

---

Veto is a scriptable model router for developers using multiple AI providers.
It filters candidates by tool access, context, task complexity, and estimated
cost, then asks the remaining models for a structured accept/reject decision.
The first model to accept with at least 70% confidence is selected.

Example routing trace (values depend on the configured providers and their responses):

```console
$ veto route "refactor the auth middleware to use JWT" --kind refactor --risk medium

  Routing: "refactor the auth middleware to use JWT"
  kind: refactor  ·  risk: medium  ·  complexity: simple

  ── Filtering candidates ──────────────────────────────────

    haiku             pass
    sonnet            pass
    opus              pass

  ── Asking models ─────────────────────────────────────────

    haiku             ✗ TASK_KIND_OUTSIDE_STRENGTHS
    sonnet            ✓ accepted  94% confident · ~$0.0023 · ~1200 tokens

  → Selected: sonnet (mid tier)
```

```bash
brew install oleg-koval/tap/veto
veto doctor
veto login
veto route --json "summarize this pull request"
```

## Features

- **Cheapest viable model first** — deterministic capability, complexity, and
  cost filters run before model admission.
- **Explicit admission** — candidates return structured accept/reject decisions,
  confidence, estimated usage, and rejection reasons.
- **Multi-provider routing** — Anthropic, OpenAI, OpenRouter, xAI, Ollama, LM
  Studio, and other OpenAI-compatible endpoints can participate.
- **Route or execute** — select a model with `veto route`, run a task with
  `veto run`, or route each step of a plan with `veto exec`.
- **Automation-friendly output** — quiet and JSON modes support scripts and
  agent infrastructure.
- **Fail-closed review** — optional acceptance criteria reject unavailable,
  malformed, incomplete, or inconsistent reviews.

## Why veto exists

As model rosters multiply, every multi-model workflow faces the same decision: which model should get this task? Hardcoded rules, manual selection, and keyword routing miss context that matters, while sending everything to the largest model wastes capacity and money.

Veto combines deterministic filters and ranking with model self-assessment. Each surviving candidate receives the task spec and returns structured admission data: accept/reject, confidence, estimated tokens and cost, and rejection reasons. Veto records the decision trail and can emit it as JSON for scripts and agent infrastructure.

Candidates are ranked cheapest-viable-first, but self-reported confidence and cost are estimates rather than guarantees. Use the offline benchmark to inspect routing mechanics and real-provider trials to evaluate quality for your own workloads.

## License

Veto is released under the [Apache License 2.0](LICENSE). See the license
file for the permissions, conditions, and disclaimer.

## Installation

### Homebrew (recommended)

Homebrew installs the checksum-pinned release on macOS or Linux:

```bash
brew install oleg-koval/tap/veto
veto version
veto doctor
```

### Release archive

Veto is currently a public beta. Download the latest archive for your operating system and CPU from [GitHub Releases](https://github.com/oleg-koval/veto/releases), extract `veto`, and put it on your `PATH` (for example, `~/.local/bin`). Archives cover Darwin, Linux, and Windows on amd64 and arm64.

Download the matching `SHA256SUMS` file and verify the archive before extracting:

```bash
version=VERSION # replace VERSION with the latest release number, without "v"
asset="veto_${version}_linux_amd64.tar.gz"
awk -v asset="${asset}" '$2 == asset {print}' SHA256SUMS > "${asset}.sha256"
test -s "${asset}.sha256"
sha256sum -c "${asset}.sha256"
tar -xzf "${asset}"
install -m 0755 "veto_${version}_linux_amd64/veto" ~/.local/bin/veto
veto version
veto doctor --json
```

On macOS, select the matching Darwin archive and pipe the checksum line to
`shasum -a 256 -c -` instead.
`SHA256SUMS` covers release archives; `BINARY_SHA256SUMS` lets `veto doctor`
verify an extracted official binary. These hashes detect corruption and release
mismatches; they are not cryptographic signatures.

### Build with Go

Go 1.26.6 or newer is required:

```bash
go install github.com/oleg-koval/veto/cmd/veto@latest
```

Or clone and build:

```bash
git clone https://github.com/oleg-koval/veto
cd veto
go build ./cmd/veto
```

`go install` and local builds are supported source-build paths. They report the
module version when available, but `veto doctor` does not present them as
checksum-verified official release binaries.

### Upgrade and uninstall

On every interactive launch, veto consults its update state and refreshes the
latest complete GitHub release at most once every 24 hours. When a newer stable
version exists, it asks before changing anything. Homebrew installs run
`brew upgrade oleg-koval/tap/veto`; versioned Go installs use the exact
`go install` version; official standalone binaries require both checksum
manifests before an atomic replacement. The original command does not continue
after an accepted update, so re-run it with the new binary. JSON, quiet,
non-interactive, development, and offline-failed checks never prompt or block.

To upgrade manually, run `brew upgrade oleg-koval/tap/veto` for Homebrew. For a
release archive, download and verify the newer archive, then replace the binary
in the same `PATH` directory. For corruption of an official release binary,
`veto doctor --fix` can reinstall that exact version when the executable is a
writable, unmanaged regular file. It refuses symlinks, package-manager paths,
source/Go-install builds, and unwritable targets. On Windows it leaves a
verified staged replacement and prints the exact manual replacement step. Your
provider credentials, local-model definitions, skills, plans, checkpoints, and
logs under `~/.veto/` are retained.

To uninstall, remove only the binary first (`rm "$(command -v veto)"`). To also remove veto's local state, back up anything you need and then remove `~/.veto/`; this deletes stored credentials, models, skills, plans, checkpoints, and logs.

## Quick start

**1. Connect a provider:**

```bash
veto login
```

For Anthropic, veto asks whether you use a **subscription** (Claude Max / Pro) or an **API key**:

- **Subscription mode** — if you have Claude Code installed and logged in, veto shells out to `claude -p` instead of hitting the API. Cost is $0 per route — your flat subscription covers it.
- **API key mode** — standard pay-per-token via the Anthropic API.

For subscription mode, veto verifies the `claude` CLI is present and saves a `CLAUDE_SUBSCRIPTION=true` marker. For API key mode, it opens the keys page in your browser and stores the key (masked input) at `~/.veto/credentials.json` (mode 0600).

For local / self-hosted models, choose option 5. veto guides you through three paths:

- **Ollama** — veto checks if Ollama is installed, lets you pick a model from a curated list (Qwen 2.5 Coder, Llama 3.2, Mistral), pulls it, and registers the model automatically. At inference time, if `ollama serve` isn't running, veto starts it in the background and waits up to 5s for it to become ready — no manual server management needed. Install Ollama from its official distribution instructions; veto does not execute a remote install script.
- **LM Studio** — walks you through starting the server manually, then collects the model id.
- **Manual** — enter endpoint URL and model id directly (works with any OpenAI-compatible server: vLLM, llama.cpp, etc).

The model is stored in `~/.veto/models.json` and participates in all routing calls at $0 inference cost. Local/OpenAI-compatible HTTP transports are text-only: they can return content but cannot read files, write files, run commands, or invoke tools through veto. After each local model is added, veto asks if you want to add another — you can register as many as you like in a single `veto login` session.

To remove a provider or local model: `veto logout` (interactive) or `veto logout <name>` (non-interactive).

You can also set environment variables directly:

```bash
# API key mode
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
export OPENROUTER_API_KEY=sk-or-...
export XAI_API_KEY=xai-...

# Subscription mode (Claude Max / Pro — requires claude CLI logged in)
export CLAUDE_SUBSCRIPTION=true
```

**2. Check what's connected:**

```bash
veto providers
```

`veto providers` also shows Grok status when `XAI_API_KEY` is set.

```
provider        status          models
──────────────  ──────────────  ──────────────────────
Anthropic       veto login      Claude Haiku, Sonnet, Opus
OpenAI          not set         run 'veto login'
OpenRouter      not set         run 'veto login'
xAI (Grok)      not set         run 'veto login'
```

To verify that an account can actually use every catalog ID for one provider,
run the opt-in account check (it makes one model-list request and saves the raw
response under `artifacts/http/`):

```bash
veto verify-models --provider openai
veto verify-models --provider anthropic
veto verify-models --provider openrouter
veto verify-models --provider xai
```

Use `--json` in automation. A nonzero exit status means the request failed or
at least one catalog ID was not returned by the account. The command never
prints or stores the API key; saved metadata contains only the endpoint,
status, timestamp, and response size. Claude subscription mode cannot be
verified through this API check because it uses the local `claude` CLI.

**3. Diagnose the installation:**

```bash
veto doctor
veto doctor --json
veto doctor --offline
veto doctor --fix
```

`veto doctor` checks the executable, PATH precedence, build version and
provenance, official release checksum, `~/.veto` ownership/permissions and
symlink shape, managed JSON, local-model definitions, approved skill paths,
and configured `claude`/`ollama` dependencies. It does not load providers,
contact models, validate credentials, or print credential values. `--offline`
skips GitHub integrity lookup. `--fix` creates missing managed directories,
corrects safe permissions, and can reinstall a corrupted official binary; it
never rewrites malformed configuration, changes login state or skill
approvals, or removes PATH duplicates. Warnings do not fail the command;
unresolved failures exit 1.

**4. Run a task:**

```bash
# route and execute — prints the model's response
veto run "extract all TODO comments from the codebase"

# route only — prints the selected model name
veto route "extract all TODO comments from the codebase" --kind extract
```

## Use Veto from coding agents

Compatible coding agents can discover the repository-local
[`$veto-routing`](.agents/skills/veto-routing/SKILL.md) skill. It teaches agents
when to use `route`, `run`, `exec`, and acceptance criteria while preserving
provider privacy, cost, transport, output, and authorization boundaries.

[`AGENTS.md`](AGENTS.md) contains the repository-wide engineering invariants and
points agents to the skill. Within this checkout, agents with project-skill
discovery can select it automatically or invoke it explicitly as
`$veto-routing`.

The same skill is distributed as
[`olko:veto-routing`](https://github.com/oleg-koval/agent-skills/tree/main/plugins/olko-skill-meta/skills/veto-routing)
in the `olko-skill-meta` marketplace plugin. For Claude Code:

```text
/plugin marketplace add oleg-koval/agent-skills
/plugin install olko-skill-meta@olko-agent-skills
```

Codex and other supported agents can install the catalog by following the
[agent-skills installation instructions](https://github.com/oleg-koval/agent-skills#quick-start).
After installation, invoke the skill as `$veto-routing` or `olko:veto-routing`,
depending on the agent's skill lookup convention.

## Commands

| Command | What it does |
|---------|-------------|
| `veto login` | Connect a provider interactively (browser + masked key) |
| `veto logout` | Remove a configured provider or local model |
| `veto run "..."` | Route a task and execute it — prints the model's response |
| `veto exec <plan.md>` | Execute a multi-step plan file, routing each step |
| `veto route "..."` | Route only — prints the selected model name, no execution |
| `veto disable <model...>` | Exclude one or more models from all routing |
| `veto enable <model...>` | Re-include a previously disabled model |
| `veto setup` | Discover and approve skill files from your skill directories |
| `veto providers` | Show which providers are configured and how |
| `veto benchmark` | Replay the offline routing corpus and emit JSON metrics |
| `veto verify-models` | Verify catalog IDs against one provider account |
| `veto doctor` | Diagnose installation and local-state integrity; optionally repair safe problems |
| `veto version` | Print veto version |
| `veto install-git-hook` | Add veto to your git workflow |

### `veto run` flags

Route and execute in one step. The winning model's response is printed to stdout. Streaming output is used automatically when the executor supports it (e.g. subscription mode via `claude -p`).

| Flag | Default | Description |
|------|---------|-------------|
| `--kind` | *(auto-detected)* | Task type (see below) |
| `--risk` | `medium` | Impact level: `low`, `medium`, `high` |
| `--max-cost` | `0` (no limit) | Estimated preflight cost ceiling in USD |
| `--timeout` | `120s` | Total timeout (routing + execution) |
| `--quiet` | `false` | Suppress routing animation — print model output only |
| `--max-output-tokens` | `8192` | Bounded output budget for the execution response |
| `--output` | *(none)* | Explicit relative file path for saving output |
| `--force` | `false` | Allow `--output` to replace an existing file |
| `--criteria` | *(none)* | Comma-separated acceptance criteria; a review pass runs after execution |

```bash
# route and execute, full pipeline visible
veto run "summarize the last 10 git commits"

# scriptable: just the output, no routing UI
veto run --quiet "extract all TODO comments" > todos.txt

# save explicitly; paths must stay inside the current directory
veto run --output todos-output.txt "extract all TODO comments"

# increase the bounded response budget and explicitly allow replacement
veto run --max-output-tokens 16000 --output summary.md --force "summarize this PR"

# auto-review the output against criteria (exits 1 if any criterion fails)
veto run "refactor the auth middleware" \
  --criteria "no third-party JWT dep,all existing tests pass,function names unchanged"
```

`veto run` makes two distinct calls when needed. Admission is a short JSON-only probe capped at 512 output tokens. Execution is a separate bounded response, defaulting to 8192 output tokens and controlled by `--max-output-tokens`; the admission limit never truncates the task result. If a provider reports that execution reached its output limit, Veto exits non-zero and does not save or review the partial output. Increase `--max-output-tokens` and retry.

`--output` is the only way for `veto run` to write a file. The path must be relative to the current directory, cannot traverse upward or target hidden files/directories, and is created with mode `0600`. Existing files are protected; pass `--force` to replace one. Objective text such as “save as report.md” does not write a file by itself.

### `veto exec` flags

Execute a multi-step plan file. Each step is routed independently to the best model and executed. Steps run sequentially.

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Print steps without executing |
| `--quiet` | `false` | Suppress routing animation — print model output only |
| `--timeout` | `60s` | Per-step timeout (routing + execution) |
| `--max-output-tokens` | `8192` | Bounded output budget for each step |
| `--on-failure` | `abort-ask` | What to do when a step fails: `abort-ask` (prompt), `abort`, or `continue` |

```bash
# preview what will run
veto exec my-plan.md --dry-run

# run the plan
veto exec my-plan.md

# run silently, keep going even if a step fails
veto exec my-plan.md --quiet --on-failure continue
```

**Plan file format** — a Markdown file with YAML frontmatter:

```markdown
---
title: Refactor auth middleware
version: 1
steps:
  - task: "Read current auth middleware and list what each function does"
    kind: extract
    risk: low
  - task: "Rewrite the token validation using the standard library JWT package"
    kind: code-change
    risk: medium
    depends_on: [1]
    success_criteria: "Tests pass, no third-party JWT dep, function names unchanged"
  - task: "Write unit tests for the new token validation"
    kind: code-change
    risk: low
    depends_on: [2]
---
```

If the file has no frontmatter or fails validation, `veto exec` offers to convert it automatically by routing the raw text through the best available model. The converted plan is saved to `~/.veto/plans/`.

### `veto route` flags

Route only — select the best model without executing the task. Useful when you want to call the model yourself or verify routing logic.

| Flag | Default | Description |
|------|---------|-------------|
| `--kind` | *(auto-detected)* | Task type (see below) |
| `--risk` | `medium` | Impact level: `low`, `medium`, `high` |
| `--max-cost` | `0` (no limit) | Estimated preflight cost ceiling in USD |
| `--timeout` | `30s` | Per-model admission timeout |
| `--quiet` | `false` | Print selected model name only (machine-readable) |
| `--json` | `false` | Print one JSON result line; implies `--quiet` and `--no-resume` |
| `--no-resume` | `false` | Ignore saved checkpoint and start fresh |
| `--dashboard` | `false` | Open a live routing view in your browser |

```bash
# scriptable model selection with metadata
veto route --json "summarize this PR"
```

### Task kinds

| Kind | Best for |
|------|----------|
| `extract` | Pull structured data from text |
| `summarize` | Condense or distill content |
| `code-change` | Write or modify code |
| `debug` | Diagnose and fix errors |
| `plan` | Break down work, write specs |
| `review` | Code review, analysis |
| `refactor` | Restructure without changing behavior |

## Superpowers

**Automatic cost preflight** — set `--max-cost 0.01` and veto filters out models whose estimated cost exceeds your ceiling before they're asked. This is a preflight estimate, not an absolute billing guarantee: admission and execution usage can differ, and some providers do not report usage. Veto reports unknown actual usage/cost as unknown rather than silently treating it as zero.

**Checkpoint resume** — if routing is interrupted (Ctrl+C, timeout, network blip), veto saves which models already responded. Re-run the same command to pick up where you left off. Use `--no-resume` to start fresh.

**End-to-end execution** — `veto run` routes and then calls the winning model with your task using the separate execution budget, printing the response to stdout. Streaming output is used automatically when the executor supports it (subscription mode via `claude -p` streams tokens as they arrive). HTTP/API and local OpenAI-compatible transports return text only; they do not get the Claude CLI's file, shell, or edit tools.

**Skill injection** — before executing, veto looks up reusable instruction snippets that match the task kind. Skills in `~/.veto/skills/` are always available (hand-written or previously generated via `generateSkill`). Skills from other directories (e.g. `~/.claude/skills/`) can be approved via `veto setup`. At startup, veto silently checks for unapproved skill files and reminds you to run `veto setup` if any are found. Kind-specific skills are preferred over generic ones; cap is 2 per execution. Skills are never auto-generated during a routing call — only pre-existing approved files are used, so there is no hidden warm-up cost at the start of each invocation.

**Acceptance-criteria review** — `--criteria "..."` on `veto run` triggers a second routing call after execution. A different model (not the one that did the work) grades the output against each criterion and returns a structured pass/fail. Exits 1 if any criterion fails, or if the review is unavailable, malformed, incomplete, or internally inconsistent — making a requested review a fail-closed quality gate.

**Multi-step plan execution** — `veto exec plan.md` runs a sequenced plan where each step is routed to the best model. If a step fails, you're asked whether to continue. Plans are just Markdown files with YAML frontmatter — write them by hand, or let veto convert any existing task list automatically. Use `--dry-run` to preview what will run before committing.

**Quiet mode for scripts** — `--quiet` on `veto run` suppresses the routing pipeline and prints only model output, making it composable:

```bash
# capture model output directly
veto run --quiet "summarize this PR" > summary.txt

# use the selected model name in a shell pipeline
MODEL=$(veto route --quiet "summarize this PR")
echo "Using: $MODEL"
```

**JSON mode for agent infrastructure** — `--json` on `veto route` suppresses animation and checkpoint resume, then emits one JSON line on stdout:

```json
{"model":"sonnet","tier":"mid","kind":"summarize","risk":"medium","complexity":"simple","confidence":0.93,"saved_usd":0.0123}
```

If no model accepts, the command exits non-zero and emits:

```json
{"error":"no_candidate","kind":"summarize","risk":"medium","complexity":"simple"}
```

**Complexity-aware tier enforcement** — veto auto-infers task complexity (`simple` / `moderate` / `complex`) from keywords in the objective and the task kind. Complex tasks (CQRS, microservices, distributed architecture…) are hard-filtered to large-tier models only; moderate tasks require mid or large tier. Small models are removed before the admission gate runs — they never get a chance to self-admit into tasks beyond their capability. Complexity is shown in the task header alongside kind and risk.

**Cost-first scoring** — candidates are ranked cheapest-viable-first. The scorer uses opus-level input cost ($0.015/1k tokens) as its reference baseline: local/free models score 1.0, haiku/mini score much higher than opus. Expensive models are asked only after cheaper ones reject. The admission gate already enforces kind-fit, so the scorer's job is to order the survivors by cost.

**Confidence gating** — any model that accepts but reports less than 70% confidence is treated as a rejection. You only get models that are genuinely sure.

**Offline evaluation** — `veto benchmark --corpus internal/eval/testdata/routing_corpus.json` replays cheapest, strongest, static, and adaptive policies without credentials or network access. It emits success, quality, cost, latency, admission-attempt, budget-violation, and confidence-calibration metrics. The checked-in corpus validates router mechanics; real-provider outcomes are required before making claims about production calibration.

**Multi-provider fallback** — if your primary provider is down or all its models reject, veto continues down the ranked list across providers automatically.

**Structured rejection reasons** — when nothing accepts, you get machine-readable reason codes (`COST_CEILING_EXCEEDED`, `COMPLEXITY_TOO_HIGH`, `TASK_KIND_OUTSIDE_STRENGTHS`, etc.) in both the UI and the log, so you know exactly what to adjust.

**Per-model disable/enable** — `veto disable haiku gpt-4.1` excludes those models from all future routing without removing their credentials. `veto enable haiku` brings them back. Disabled model names are stored in `~/.veto/config.json` under `"disabled_models"` — edit the file directly for bulk changes.

**7-day rotating logs** — every routing decision is logged as JSON lines to `~/.veto/logs/veto-YYYY-MM-DD.log`. Files older than 7 days are pruned automatically.

## Providers and models

| Provider | Models | Set up with |
|----------|--------|-------------|
| Anthropic (subscription) | `haiku`, `sonnet`, `opus` | `CLAUDE_SUBSCRIPTION=true` + `claude` CLI logged in |
| Anthropic (API key) | `haiku`, `sonnet`, `opus` | `ANTHROPIC_API_KEY` |
| OpenAI | `gpt-4.1`, `gpt-4.1-mini`, `sol`, `terra`, `luna` | `OPENAI_API_KEY` |
| OpenRouter | `meta-llama/llama-4-maverick` (1 currently routable model) | `OPENROUTER_API_KEY` |
| xAI (Grok) | `grok-4.5`, `grok-4.3`, `grok-3`, `grok-3-mini` | `XAI_API_KEY` |
| Local / self-hosted | any name you choose | `veto login` → option 5 (guided Ollama install, LM Studio, or manual) |

Subscription mode takes precedence over API key when both are configured. Claude subscription mode uses the `claude` CLI and is the only current transport with executable tools (`bash`, `read`, `write`, and `edit`). Anthropic/OpenAI/OpenRouter APIs and local OpenAI-compatible servers are text-only through veto, even when the underlying model advertises function calling; they cannot inspect or modify your files. Local inference has $0 provider billing, but still consumes your machine's resources. `veto providers` shows which mode is active and lists all local models.

OpenRouter itself offers a much larger catalog, but this version of veto only
registers the model listed above for routing. Dynamic OpenRouter catalog support
is planned and is not implied by connecting an API key.

**Ollama models curated for routing:**

| Model | Size | Best for |
|-------|------|----------|
| `qwen2.5-coder:7b` | 4.7 GB | Code tasks — outperforms many larger models on coding |
| `llama3.2:3b` | 2.0 GB | Quick tasks, low-RAM machines |
| `mistral:7b` | 4.1 GB | General-purpose, good speed/quality balance |

## File layout

```
~/.veto/
  credentials.json                      # stored API keys and subscription marker (0600)
  models.json                           # local / self-hosted model definitions (0600)
  config.json                           # settings: on_failure, skills approval state, disabled_models
  skills/<kind>.md                      # cached skill snippets (auto-generated, editable)
  checkpoints/<hash>.json               # resume state for interrupted routing
  plans/<timestamp>-<slug>-converted.md # auto-converted plan files
  logs/veto-YYYY-MM-DD.log              # JSON-line routing history (7-day rolling)
```

## Development

```bash
make test     # go test -race -timeout 120s ./...
make build    # build with version injected from git tag (or "dev")
make lint     # go vet ./...
make release RELEASE_VERSION=0.1.0  # validate release, then print tagging instructions
release_dist=$(mktemp -d)
./scripts/package-release.sh v0.1.0 "${release_dist}"  # local release dry run
```

The binary embeds the normalized version without the leading `v` (`veto version`). A versioned `go install` resolves the same public version through Go build metadata while remaining honestly labeled as a source build. CI runs on every push and PR to `main`, including local release-packaging and Homebrew-formula dry runs. Conventional commits on `main` maintain a Release Please pull request (`fix` increments patch, `feat` increments minor, and a breaking change increments major). Merging that release PR creates the tag and GitHub release, then explicitly starts the existing release workflow. That workflow runs the offline gates before GoReleaser publishes six Darwin/Linux/Windows amd64/arm64 archives plus `SHA256SUMS` and `BINARY_SHA256SUMS`. When `HOMEBREW_TAP_TOKEN` is configured, it also publishes the checksum-pinned Homebrew formula. Every GoReleaser binary is checked against the binary manifest before publication. Nothing is published unless all applicable gates and checksum verification pass.

See [`docs/architecture.md`](docs/architecture.md) for how the routing pipeline works internally.
See [`docs/release-readiness.md`](docs/release-readiness.md) for the automated gates and the owner-run provider, trial, license, and publish checklist.
See [`docs/launch.md`](docs/launch.md) for the launch angle, channel-ready copy, share loops, measurement plan, and launch gates.
See [`CONTRIBUTING.md`](CONTRIBUTING.md) before opening an issue or pull request.

## Project status

Veto is a public beta. CI exercises the router, race detector, onboarding smoke
test, offline benchmark, release packaging, and Homebrew formula rendering.
Published releases include checksum manifests. When `HOMEBREW_TAP_TOKEN` is
configured, the release workflow updates the Homebrew tap from those verified
artifacts; otherwise it skips the tap update. Real-provider availability,
pricing, and routing quality still depend on the configured accounts and
workloads.

See the [latest release](https://github.com/oleg-koval/veto/releases/latest) and
[`CHANGELOG.md`](CHANGELOG.md) for shipped changes.
