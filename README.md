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

veto opens the API keys page in your browser, then prompts for the key (masked input). Keys are stored at `~/.veto/credentials.json` (mode 0600).

You can also set environment variables directly:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
export OPENROUTER_API_KEY=sk-or-...
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

**3. Route a task:**

```bash
veto route --task "extract all TODO comments from the codebase" --kind extract
```

## Commands

| Command | What it does |
|---------|-------------|
| `veto login` | Connect a provider interactively (browser + masked key) |
| `veto providers` | Show which providers are configured and how |
| `veto route --task "..." [flags]` | Route a task to the best available model |

### `veto route` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--task` | *(required)* | What the model should do |
| `--kind` | `code-change` | Task type (see below) |
| `--risk` | `medium` | Impact level: `low`, `medium`, `high` |
| `--max-cost` | `0` (no limit) | Maximum spend in USD |
| `--timeout` | `30s` | Per-model admission timeout |
| `--quiet` | `false` | Machine-readable output (prints selected model name only) |
| `--no-resume` | `false` | Ignore saved checkpoint and start fresh |

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

**Quiet mode for scripts** — `--quiet` suppresses all UI and prints only the selected model name on stdout. Pipe it anywhere:

```bash
MODEL=$(veto route --task "summarize this PR" --kind summarize --quiet)
echo "Using: $MODEL"
```

**Confidence gating** — any model that accepts but reports less than 70% confidence is treated as a rejection. You only get models that are genuinely sure.

**Multi-provider fallback** — if your primary provider is down or all its models reject, veto continues down the ranked list across providers automatically.

**Structured rejection reasons** — when nothing accepts, you get machine-readable reason codes (`COST_CEILING_EXCEEDED`, `TASK_KIND_OUTSIDE_STRENGTHS`, etc.) in both the UI and the log, so you know exactly what to adjust.

**7-day rotating logs** — every routing decision is logged as JSON lines to `~/.veto/logs/veto-YYYY-MM-DD.log`. Files older than 7 days are pruned automatically.

## Providers and models

| Provider | Models | Set up with |
|----------|--------|-------------|
| Anthropic | `haiku` (fast/cheap), `sonnet` (balanced), `opus` (most capable) | `ANTHROPIC_API_KEY` |
| OpenAI | `gpt-4o`, `gpt-4o-mini` | `OPENAI_API_KEY` |
| OpenRouter | `llama-3.1-405b` (and 100+ more via API) | `OPENROUTER_API_KEY` |

## File layout

```
~/.veto/
  credentials.json          # stored API keys (0600)
  checkpoints/<hash>.json   # resume state for interrupted routing
  logs/veto-YYYY-MM-DD.log  # JSON-line routing history (7-day rolling)
```

## Development

```bash
go test ./...           # run all tests
go build ./cmd/veto     # build the binary
go vet ./...            # static analysis
```

See [`docs/architecture.md`](docs/architecture.md) for how the routing pipeline works internally.
