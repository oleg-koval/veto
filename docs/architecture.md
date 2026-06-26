# Architecture

veto routes tasks to AI models through a three-stage pipeline: hard filtering, scoring, and admission gating. Each stage narrows the candidate list; the first model to pass all three wins.

## Pipeline overview

```
TaskSpec
    │
    ▼
┌─────────────────────────────┐
│  1. Hard Filter             │  deterministic, zero I/O
│  prune obviously wrong fits │
└────────────────┬────────────┘
                 │ survivors
                 ▼
┌─────────────────────────────┐
│  2. Scorer                  │  weighted multi-factor ranking
│  rank survivors by score    │
└────────────────┬────────────┘
                 │ ranked list
                 ▼
┌─────────────────────────────┐
│  3. Admission Gate          │  real LLM call per candidate
│  ask each model in order    │
└────────────────┬────────────┘
                 │ first accept
                 ▼
        ModelCapabilities
        + AdmissionDecision
```

## Stage 1: Hard Filter (`pkg/router/filter.go`)

Removes models that cannot possibly handle the task. Pure function — no I/O, deterministic. Reasons it prunes:

| Condition | Reason code |
|-----------|-------------|
| Model lacks a required tool | `MISSING_REQUIRED_TOOL` |
| Model context window < task's `MaxTokens` | `CONTEXT_TOO_LARGE` |
| Estimated cost > task's `MaxCostUSD` | `COST_CEILING_EXCEEDED` |
| Task kind is in model's weakness list | `TASK_KIND_OUTSIDE_STRENGTHS` |

The cost estimate at this stage is intentionally rough (assumes 1000 input tokens, 100 output tokens). The admission gate refines it per-model.

## Stage 2: Scorer (`pkg/router/scorer.go`)

Ranks the survivors by a weighted score (range 0.0–1.0):

| Factor | Weight | What it measures |
|--------|--------|-----------------|
| Kind match | 30% | Is this task kind a model strength (1.0), neutral (0.5), or weakness (0.0)? |
| Historical success rate | 25% | How often has this model succeeded on similar tasks? |
| Cost fit | 20% | How much headroom is there under the cost ceiling? |
| Reject rate | 15% | How rarely does this model reject tasks it's given? |
| Eval score | 10% | Average quality score on past tasks |

Historical signals come from `Registry.Signal()` — currently a stub returning neutral values. Phase 2 will replace this with a real store backed by routing history.

## Stage 3: Admission Gate (`pkg/router/admission.go`)

Sends a structured prompt to each candidate model in score order and parses its JSON response. The prompt tells the model its own capabilities and asks it to self-assess.

The model must respond with JSON only:

```json
{
  "accept": true,
  "confidence": 0.94,
  "reason_codes": [],
  "estimated_tokens": 1200,
  "estimated_cost_usd": 0.0023,
  "suggested_alternative_model": "",
  "required_task_changes": []
}
```

**Confidence gate:** any model accepting with confidence < 0.7 is treated as a rejection (`LOW_CONFIDENCE`). This prevents models from accepting out of politeness without real certainty.

**Fail-safe:** if the model returns prose instead of JSON, or the executor errors, the gate rejects. The gate never silently accepts on ambiguity.

## Manager (`pkg/router/manager.go`)

Orchestrates the three stages. Emits `ProgressEvent` at each step so callers can render or log without being coupled to the pipeline internals.

On exec failure (network error, API timeout) for a single model: logs the error and continues to the next candidate. On context cancellation (Ctrl+C, deadline): saves checkpoint and propagates the error.

Cap: at most 3 admission attempts on a fresh run. When resuming from a checkpoint, all remaining untried candidates are attempted.

## Checkpoint/Resume (`cmd/veto/checkpoint.go`)

Task identity is a SHA-256 hash of `(objective, kind, risk, maxCost)`, truncated to 8 bytes (16 hex chars). On interruption, the current `Checkpoint` (which models were tried and their outcomes) is serialized to `~/.veto/checkpoints/<hash>.json`.

On the next run with the same task spec, veto loads the checkpoint, skips already-tried models, and continues. `--no-resume` bypasses this.

## Provider model (`cmd/veto/main.go`, `pkg/executor/`)

Each provider (Anthropic, OpenAI, OpenRouter) has a concrete `Executor` in `pkg/executor/`. The `ExecutorFactory` interface (defined in `pkg/router/`) maps model names to executors — this keeps the router package free of provider-specific imports.

```
pkg/router/admission.go   ExecutorFactory (interface)
                              ↑
cmd/veto/main.go          providerRegistry (concrete factory)
                              ↓
pkg/executor/             AnthropicExecutor, OpenAIExecutor, OpenRouterExecutor
```

## Event bus

`Manager.OnEvent` is a single callback wired by the CLI. All events from the routing pipeline flow through it:

| Event | Meaning |
|-------|---------|
| `filter_pass` | Model survived hard filter |
| `filter_fail` | Model pruned by hard filter (with reason) |
| `ask_start` | Admission gate sending prompt to model |
| `ask_accept` | Model accepted (with confidence, est. cost, est. tokens) |
| `ask_reject` | Model rejected (with reason codes) |
| `ask_error` | Executor or parse failure |

The CLI renderer (`cmd/veto/render.go`) uses these events to drive the animated terminal display. The logger (`cmd/veto/logger.go`) uses the same events to write JSON lines to disk. Neither is coupled to the other.

## Credential storage

`veto login` stores API keys in `~/.veto/credentials.json` (mode 0600, JSON object of `ENV_KEY → value`). At runtime, environment variables take precedence — the credentials file is only consulted when the env var is absent.

## Logging

One log file per calendar day: `~/.veto/logs/veto-YYYY-MM-DD.log`. Format is JSON lines via `log/slog`. Files older than 7 days are pruned on each `veto route` invocation. If the log file can't be created, routing continues silently (logs go to stderr at ERROR level only).
