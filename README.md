# veto

**Model router with self-admitting receivers.**

Instead of guessing which AI model to use, veto asks each candidate model whether it *wants* the task. Models respond with structured JSON — accept/reject, confidence score, estimated cost, and a reason if they decline. The first model that accepts wins.

```
$ veto route --task "refactor the auth middleware to use JWT" --kind refactor --risk medium

  Routing: "refactor the auth middleware to use JWT"
  kind: refactor  ·  risk: medium

  ── Filtering candidates ──────────────────────────────────

    haiku             pass
    sonnet            pass
    opus              pass

  ── Asking models ─────────────────────────────────────────

    haiku             ✗ TASK_KIND_OUTSIDE_STRENGTHS
    sonnet            ✓ accepted  94% confident · ~$0.0023 · ~1200 tokens

  → Selected: sonnet (mid tier)
```

## Why veto exists

Every AI-assisted workflow faces the same problem: you have multiple models at different price/capability points, and picking the wrong one wastes money or produces bad output. The common solutions — hardcoded rules, manual selection, or routing by keyword — all break down as soon as the task gets subtle.

veto takes a different approach: **let the models decide**. Each candidate is sent the task spec and asked to self-assess. A model that knows it's weak at planning will say so. A model that sees its cost ceiling exceeded will reject. The router only needs to filter obviously impossible candidates (wrong tools, context too large) and rank the rest — the models handle the nuanced judgment.

The result: you get the cheapest model that's genuinely confident it can do the work, with machine-readable rejection reasons when nothing fits.

## Install

```bash
go install github.com/oleg-koval/veto/cmd/veto@latest
```

Or clone and build:

```bash
git clone https://github.com/oleg-koval/veto
cd veto
go build ./cmd/veto
```

## Quick start

**1. Connect a provider:**

```bash
veto login
```

For Anthropic, veto asks whether you use a **subscription** (Claude Max / Pro) or an **API key**:

- **Subscription mode** — if you have Claude Code installed and logged in, veto shells out to `claude -p` instead of hitting the API. Cost is $0 per route — your flat subscription covers it.
- **API key mode** — standard pay-per-token via the Anthropic API.

For subscription mode, veto verifies the `claude` CLI is present and saves a `CLAUDE_SUBSCRIPTION=true` marker. For API key mode, it opens the keys page in your browser and stores the key (masked input) at `~/.veto/credentials.json` (mode 0600).

For local / self-hosted models (Ollama, LM Studio, vLLM, llama.cpp), choose option 4 and enter a name, endpoint URL, and model id. The model is stored in `~/.veto/models.json` and participates in all routing calls at $0 cost.

To remove a provider or local model: `veto logout` (interactive) or `veto logout <name>` (non-interactive).

You can also set environment variables directly:

```bash
# API key mode
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
export OPENROUTER_API_KEY=sk-or-...

# Subscription mode (Claude Max / Pro — requires claude CLI logged in)
export CLAUDE_SUBSCRIPTION=true
```

**2. Check what's connected:**

```bash
veto providers
```

```
provider        status          models
──────────────  ──────────────  ──────────────────────
Anthropic       veto login      Claude Haiku, Sonnet, Opus
OpenAI          not set         run 'veto login'
OpenRouter      not set         run 'veto login'
```

**3. Run a task:**

```bash
# route and execute — prints the model's response
veto run "extract all TODO comments from the codebase"

# route only — prints the selected model name
veto route "extract all TODO comments from the codebase" --kind extract
```

## Commands

| Command | What it does |
|---------|-------------|
| `veto login` | Connect a provider interactively (browser + masked key) |
| `veto logout` | Remove a configured provider or local model |
| `veto run "..."` | Route a task and execute it — prints the model's response |
| `veto exec <plan.md>` | Execute a multi-step plan file, routing each step |
| `veto route "..."` | Route only — prints the selected model name, no execution |
| `veto providers` | Show which providers are configured and how |
| `veto install-git-hook` | Add veto to your git workflow |

### `veto run` flags

Route and execute in one step. The winning model's response is printed to stdout. Streaming output is used automatically when the executor supports it (e.g. subscription mode via `claude -p`).

| Flag | Default | Description |
|------|---------|-------------|
| `--kind` | *(auto-detected)* | Task type (see below) |
| `--risk` | `medium` | Impact level: `low`, `medium`, `high` |
| `--max-cost` | `0` (no limit) | Maximum spend in USD |
| `--timeout` | `60s` | Total timeout (routing + execution) |
| `--quiet` | `false` | Suppress routing animation — print model output only |

```bash
# route and execute, full pipeline visible
veto run "summarize the last 10 git commits"

# scriptable: just the output, no routing UI
veto run --quiet "extract all TODO comments" > todos.txt
```

### `veto exec` flags

Execute a multi-step plan file. Each step is routed independently to the best model and executed. Steps run sequentially.

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Print steps without executing |
| `--quiet` | `false` | Suppress routing animation — print model output only |
| `--timeout` | `60s` | Per-step timeout (routing + execution) |
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
    success_criteria: "Tests pass, no third-party JWT dep"
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
| `--max-cost` | `0` (no limit) | Maximum spend in USD |
| `--timeout` | `30s` | Per-model admission timeout |
| `--quiet` | `false` | Print selected model name only (machine-readable) |
| `--no-resume` | `false` | Ignore saved checkpoint and start fresh |
| `--dashboard` | `false` | Open a live routing view in your browser |

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

**Automatic cost control** — set `--max-cost 0.01` and veto will never route to a model whose estimated cost exceeds your ceiling. Models that would exceed it are filtered before they're even asked.

**Checkpoint resume** — if routing is interrupted (Ctrl+C, timeout, network blip), veto saves which models already responded. Re-run the same command to pick up where you left off. Use `--no-resume` to start fresh.

**End-to-end execution** — `veto run` routes and then calls the winning model with your task, printing the response to stdout. Streaming output is used automatically when the executor supports it (subscription mode via `claude -p` streams tokens as they arrive).

**Multi-step plan execution** — `veto exec plan.md` runs a sequenced plan where each step is routed to the best model. If a step fails, you're asked whether to continue. Plans are just Markdown files with YAML frontmatter — write them by hand, or let veto convert any existing task list automatically. Use `--dry-run` to preview what will run before committing.

**Quiet mode for scripts** — `--quiet` on `veto run` suppresses the routing pipeline and prints only model output, making it composable:

```bash
# capture model output directly
veto run --quiet "summarize this PR" > summary.txt

# use the selected model name in a shell pipeline
MODEL=$(veto route --quiet "summarize this PR")
echo "Using: $MODEL"
```

**Confidence gating** — any model that accepts but reports less than 70% confidence is treated as a rejection. You only get models that are genuinely sure.

**Multi-provider fallback** — if your primary provider is down or all its models reject, veto continues down the ranked list across providers automatically.

**Structured rejection reasons** — when nothing accepts, you get machine-readable reason codes (`COST_CEILING_EXCEEDED`, `TASK_KIND_OUTSIDE_STRENGTHS`, etc.) in both the UI and the log, so you know exactly what to adjust.

**7-day rotating logs** — every routing decision is logged as JSON lines to `~/.veto/logs/veto-YYYY-MM-DD.log`. Files older than 7 days are pruned automatically.

## Providers and models

| Provider | Models | Set up with |
|----------|--------|-------------|
| Anthropic (subscription) | `haiku`, `sonnet`, `opus` | `CLAUDE_SUBSCRIPTION=true` + `claude` CLI logged in |
| Anthropic (API key) | `haiku`, `sonnet`, `opus` | `ANTHROPIC_API_KEY` |
| OpenAI | `gpt-4o`, `gpt-4o-mini` | `OPENAI_API_KEY` |
| OpenRouter | `llama-3.1-405b` (and 100+ more via API) | `OPENROUTER_API_KEY` |
| Local / self-hosted | any name you choose | `veto login` → option 4 |

Subscription mode takes precedence over API key when both are configured. Local models use `NewOpenAICompatibleExecutor` — any server that speaks the OpenAI chat-completions API works (Ollama, LM Studio, vLLM, llama.cpp). Cost is $0 — local inference has no per-token billing. `veto providers` shows which mode is active and lists all local models.

## File layout

```
~/.veto/
  credentials.json                      # stored API keys and subscription marker (0600)
  models.json                           # local / self-hosted model definitions (0600)
  config.json                           # on_failure default and other settings
  checkpoints/<hash>.json               # resume state for interrupted routing
  plans/<timestamp>-<slug>-converted.md # auto-converted plan files
  logs/veto-YYYY-MM-DD.log              # JSON-line routing history (7-day rolling)
```

## Development

```bash
go test ./...           # run all tests
go build ./cmd/veto     # build the binary
go vet ./...            # static analysis
```

See [`docs/architecture.md`](docs/architecture.md) for how the routing pipeline works internally.
