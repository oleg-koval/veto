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

Routing and execution are separate contracts. The admission gate sends a
short JSON-only probe with a fixed 512-token output budget. After a model is
selected, the command uses the executor's full-task path with an independent,
bounded output budget (8192 by default; configurable with
`--max-output-tokens`). A short admission response therefore cannot truncate
the user's task result. Provider usage and length termination are retained
when the transport reports them; missing usage remains unknown rather than
being treated as zero.

## Stage 1: Hard Filter (`pkg/router/filter.go`)

Removes models that cannot possibly handle the task. Pure function — no I/O, deterministic. Reasons it prunes:

| Condition | Reason code |
|-----------|-------------|
| Model lacks a required tool | `MISSING_REQUIRED_TOOL` |
| Model context window < task's `MaxTokens` | `CONTEXT_TOO_LARGE` |
| Estimated cost > task's `MaxCostUSD` | `COST_CEILING_EXCEEDED` |
| Model tier too low for task complexity | `COMPLEXITY_TOO_HIGH` |
| Task kind is in model's weakness list | `TASK_KIND_OUTSIDE_STRENGTHS` |

The cost estimate at this stage is intentionally rough (assumes 1000 input tokens, 100 output tokens). The admission gate refines it per-model. This is a preflight estimate for routing, not an absolute billing guarantee: actual execution can use a different number of tokens, and some providers do not report usage.

**Complexity enforcement** — `tierMeetsComplexity` maps `Complexity` values to a minimum tier: `complex` → `large` only; `moderate` → `mid` or `large`; `simple` → any tier. `task.Complexity` is auto-inferred by `Manager.Route` before the filter runs (see "Complexity inference" below). Models below the required tier are pruned here, before the self-admission gate, so they are never asked.

**Executable runtime enforcement** — `veto run` marks objectives with explicit repository mutation signals such as fixing a pull request and pushing the result. For these tasks, known text-only transports are rejected with `MISSING_REQUIRED_TOOL` before admission. Runtimes with a known executable tool set and agent runtimes whose tools are discovered only during execution remain eligible.

## Stage 2: Scorer (`pkg/router/scorer.go`)

Ranks the survivors by a weighted score (range 0.0–1.0):

| Factor | Weight | What it measures |
|--------|--------|-----------------|
| Cost fit | 35% | How cheap is this model relative to opus? (cheapest-viable-first) |
| Historical success rate | 25% | How often has this model succeeded on similar tasks? |
| Kind match | 20% | Is this task kind a model strength (1.0), neutral (0.5), or weakness (0.0)? |
| Reject rate | 10% | How rarely does this model reject tasks it's given? |
| Eval score | 10% | Average quality score on past tasks |

**Cost-fit formula** — `costFit` uses opus input cost ($0.015/1k tokens) as a reference baseline when no `MaxCostUSD` ceiling is set. Local/free models score 1.0; models score `max(0.05, 1.0 − (inputCostPer1k / 0.015))`. This means haiku/mini rank far above opus purely on cost, and opus scores ~0.05. With a ceiling set, `costFit` scales linearly from 1.0 (near-free) to 0.0 (at or above the ceiling).

Cost fit is weighted highest because the hard filter and admission gate already enforce kind-fit and tier constraints — the scorer's remaining job is to order the survivors cheapest-viable-first, so you never pay for opus when haiku or a local model can do the work.

Historical signals come from the manager's configured `Store`. The built-in `MemoryStore` and `FileStore` maintain task-kind-aware acceptance, execution, usage, cost, latency, and optional evaluation aggregates; `Manager.Route` passes that store to the scorer so a fresh process can learn from persisted history. The static `Registry.Signal()` remains a neutral-baseline signal source for callers that rank directly without a store.

## Offline evaluation (`veto benchmark`)

`veto benchmark --corpus <path>` is deterministic and does not load credentials or contact providers. The checked-in corpus replays four policies: cheapest viable, strongest tier, the static scorer with neutral history, and the adaptive scorer with recorded history. JSON metrics include success rate, quality score, average/P95 cost and latency, admission attempts, budget violations, and confidence-calibration error/Brier score when confidence labels are present. Synthetic replay validates routing mechanics only; real labeled executions are required to assess production model quality or calibration.

## Stage 3: Admission Gate (`pkg/router/admission.go`)

Sends a structured prompt to each candidate model in score order and parses its JSON response. The prompt tells the model its own capabilities and asks it to self-assess.

The model must respond with a JSON object. The parser (`parseAdmissionJSON`) scans for the first `{` in the output, then uses `json.Decoder.Decode` to read exactly one JSON value and stop — trailing prose appended by open models (Llama, Mistral) is ignored rather than causing a parse failure:

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

**Per-model timeout:** each executor call in `Ask` runs under a child context derived from the outer context. The gate default is 20 seconds; `veto run` raises it to the configurable `--admission-timeout` default of 60 seconds because subscription CLI startup routinely exceeds 20 seconds. A hung model is treated as a rejection so routing continues to the next candidate. The outer `veto run --timeout` default of two hours still bounds admission plus execution.

**Claude CLI admission:** subscription admission uses `claude -p` in safe mode with tools disabled, session persistence disabled, and a native JSON schema matching `AdmissionDecision`. Veto extracts the CLI envelope's `structured_output` value before passing it to the shared parser. The later execution call does not use these admission-only restrictions and runs in the caller's working directory.

**Fail-safe:** two distinct failure paths:
- **Executor error** (API auth failure, rate limit, network error): `Ask` returns the executor's error directly. The manager surfaces it as the real message in `EventAskError.Detail` — the routing UI shows e.g. `! openai: 429 rate limited` rather than a generic label.
- **JSON parse failure** (model returned text with no valid JSON object): `Ask` returns a soft reject — `AdmissionDecision{Accept: false, ReasonCodes: ["PARSE_FAILURE"]}` with no error. Routing continues silently to the next candidate.

The gate never silently accepts on ambiguity.

## Complexity inference (`pkg/router/complexity.go`)

Before routing, `Manager.Route` auto-infers task complexity from the objective text and task kind if `TaskSpec.Complexity` is not set by the caller:

```go
if task.Complexity == "" {
    task.Complexity = InferComplexity(task.Objective, task.Kind)
}
```

`InferComplexity` scores the lowercase objective against three keyword tiers:

| Tier | Score added | Example keywords |
|------|-------------|-----------------|
| High | +3 each | `cqrs`, `event sourcing`, `microservices`, `distributed system`, `multi-tenant` |
| Medium | +2 each | `architecture`, `infrastructure`, `scalable`, `enterprise`, `system design` |
| Low | +1 each | `e2e`, `pipeline`, `service`, `deploy`, `integrate`, batch review-comment remediation |
| Simple signals | −2 each | `simple`, `basic`, `quick`, `hello world` |

Task kind adjusts the score too: `plan` adds 2; `debug` adds 1; `extract`/`summarize` subtract 2.

Final mapping: score ≥ 4 → `complex`; score ≥ 1 → `moderate`; else → `simple`.

The inferred (or caller-supplied) complexity is then used by `HardFilter` via `tierMeetsComplexity` to restrict candidates before scoring begins. The complexity value is displayed in the task header alongside kind and risk.

## Manager (`pkg/router/manager.go`)

Orchestrates the three stages. Emits `ProgressEvent` at each step so callers can render or log without being coupled to the pipeline internals.

**Sequential rank-order execution:** after filtering and ranking, the manager asks each candidate one at a time, in descending score order, passing the caller's `context.Context` straight through to `gate.Ask` — no child context or goroutines involved. As soon as a candidate accepts, the manager logs the decision, emits `EventAskAccept`, and returns immediately; no lower-ranked candidate is ever asked. This matches ADR-001: "ask each candidate in rank order, take the first that accepts with ≥70% confidence."

```
candidates [A, B, C]  (A highest-ranked)
    │
    ├─ gate.Ask(ctx, A) ──→ reject → emit EventAskReject, continue
    ├─ gate.Ask(ctx, B) ──→ accept → emit EventAskAccept, return B
    └─ C is never asked
```

On exec failure (network error, API timeout, auth error) for a candidate: logs `PARSE_FAILURE`, emits `EventAskError` with `Detail` set to the real error message (e.g. `"openai: 429 rate limited"`), and continues to the next candidate — unless the failure was caused by the outer context being cancelled/timed out, in which case the error propagates immediately via `fmt.Errorf("routing: %w", ctx.Err())`. JSON parse failures (model returned text but no valid JSON) are handled as a soft reject in the gate and never reach this path as errors. The loop also checks `ctx.Err()` before each ask, so cancellation between asks is caught promptly without waiting on a model call.

Cap: at most 3 candidates receive admission calls per run, including transport
failures. Checkpoint resume skips tried models and can continue with the next
bounded group in a later invocation.

## Checkpoint/Resume (`cmd/veto/checkpoint.go`)

Task identity is a SHA-256 hash of `(objective, kind, risk, maxCost)`, truncated to 8 bytes (16 hex chars). On interruption, the current `Checkpoint` (which models were tried and their outcomes) is serialized to `~/.veto/checkpoints/<hash>.json`.

On the next run with the same task spec, veto loads the checkpoint, skips already-tried models, and continues. `--no-resume` bypasses this.

## Provider model (`cmd/veto/main.go`, `pkg/executor/`)

Each provider has a concrete `Executor` in `pkg/executor/`. The `ExecutorFactory` interface (defined in `pkg/router/`) maps model names to executors — this keeps the router package free of provider-specific imports.

```
pkg/router/admission.go   ExecutorFactory (interface)
                              ↑
cmd/veto/main.go          providerRegistry (concrete factory)
                              ↓
pkg/executor/             AnthropicExecutor, OpenAIExecutor, OpenRouterExecutor, CLIExecutor,
                          CodexCLIExecutor, OpenAICompatibleExecutor (local/self-hosted)
```

`providerRegistry` exposes two views of the model set: `For(name)` (executor lookup, used by admission and execution) and `modelCaps()` (capability slice, used to build the `router.Registry`). `modelCaps()` intersects catalog metadata with the active transport's effective tools before hard filtering. This allows local models added via `veto login` to participate in scoring and filtering alongside built-ins without claiming capabilities their transport cannot provide.

Model discovery and execution are separate internal contracts. A
`router.ModelSource` returns catalog metadata without choosing a transport; the
built-in catalog and local model configuration both implement it. An
`executor.RuntimeAdapter` provides the separate admission and full-execution
paths plus a stable runtime ID. When the provider registry binds them, each
model has a source/provider/model/runtime identity. Effective tools still come
from the active transport. `executor.ToolCapabilityStatus` preserves an
undiscovered runtime tool set as unknown rather than silently changing it to a
known-empty set.

`pkg/openroutercatalog` implements bounded discovery against OpenRouter's
official models endpoint and persists a versioned cache at
`~/.veto/cache/openrouter-models.json`. The client validates third-party data,
preserves unknown values separately from zero, exposes fresh/stale and offline
state independently, and never replaces a known-good cache with malformed or
partial data. Optional ETags support conditional refresh. The official schema
provides `expiration_date` rather than a separate status, so cached status is
derived as available or scheduled for removal. See
[`docs/openrouter-catalog.md`](openrouter-catalog.md). When OpenRouter is
configured, validated available entries join the active registry with their
unknown metadata preserved explicitly. Local preferences filter the catalog,
normal hard filters and scoring rank it, and only three candidates can reach
paid admission.

`pkg/opencode` implements OpenCode runtime discovery independently from model
routing. Attach mode accepts only explicit HTTP loopback URLs, rejects
redirects, and reads health plus connected provider/model metadata through the
documented `/provider` API with `/config/providers` compatibility fallback.
CLI mode executes the exact `opencode` binary returned by `PATH`, validates its
semantic version, and parses bounded `provider/model` output without a shell.
Managed mode launches `opencode serve` on an explicit loopback host/port with a
random process-local Basic Auth password and exposes an owned process lifecycle.
The adapter never scans ports or reads OpenCode configuration or credential
stores. Discovered models join the provider registry under
`opencode:<provider>/<model>` and retain known static catalog metadata when an
exact provider/model match exists; unknown price, tier, and tools remain
unknown.

`pkg/opencode.Runtime` binds one exact provider/model to that runtime. Attach
and managed modes create a fresh documented session, subscribe to
`/global/event`, send an asynchronous prompt, stream text deltas, and delete the
internal session afterward. Admission sessions carry an opaque
`veto:admission:*` title and a session-level deny-all permission rule. Execution
sessions carry `veto:execution:*`, preserve OpenCode's existing permission
policy, and explicitly reject any new `permission.asked` event. Cancellation
calls the session abort endpoint before cleanup. CLI fallback invokes the exact
discovered executable with JSON output, an exact `provider/model`, an opaque
title, and a `--` argument boundary; it never supplies OpenCode's auto-approval
flags. Its admission subprocess uses a final inline deny-all permission override
while preserving unrelated inline configuration.

The optional `executor.EventTaskExecutor` contract streams text and emits only
allowlisted lifecycle metadata. OpenCode tool states, approvals, patch/diff or
attachment counts, usage, cost, failures, and cancellation map into Veto
execution and ledger events. Prompts, tool arguments/output, paths, file
contents, and provider response bodies are not placed in the event envelope.

`integrations/opencode` embeds a self-contained local OpenCode plugin and four
global command definitions in the Veto binary. The installer writes only its
six managed files with private modes, refuses symlink-shaped targets and
unapproved collisions, and uninstalls only checksum-identical content. The
plugin's `chat.message` hook extracts bounded non-synthetic user text, invokes
`veto route --json --runtime opencode` without a shell, validates an exact
OpenCode routing identity, and mutates the pending message model. Errors are
visible and fail open to the current OpenCode model.

Recursion is blocked in two independent ways. Plugin-spawned Veto processes set
`VETO_ROUTING_ORIGIN=opencode-plugin`, which OpenCode CLI descendants inherit,
and attach-mode session events identify opaque `veto:*` titles as internal.
All-synthetic continuations also bypass routing. `/veto-off` is a visible,
in-memory per-session override; explicit `/veto-route` still works while it is
off. The plugin exposes no permission hook and never approves a tool call.

`integrations/hermes` embeds a standalone Hermes plugin in the Veto binary.
The installer writes four private managed files under
`<HERMES_HOME>/plugins/veto`, rejects symlink-shaped directories and collisions,
and removes only checksum-identical content. It deliberately does not edit
Hermes' enabled-plugin list or provider configuration; the operator validates
and enables it through `hermes plugins doctor` and `hermes plugins enable`.

The plugin registers six namespaced tools (`veto_status`, `veto_route`,
`veto_run`, `veto_models`, `veto_cost`, and `veto_cancel`) and five slash
commands (`/veto`, `/models`, `/route`, `/cost`, and `/veto-off`). Handlers
invoke an exact `VETO_BINARY` or `veto` argument array without a shell, set
`VETO_ROUTING_ORIGIN=hermes-plugin`, cap objective and response sizes, apply
timeouts, and terminate the owned subprocess (including its POSIX process group)
on timeout or explicit cancellation. A caller-supplied execution ID lets
`veto_cancel` target one
plugin-owned process; it cannot cancel unrelated processes.

`veto hermes api --json` is the version-1 compatibility handshake. Missing,
older, malformed, and incompatible binaries become structured tool results
rather than plugin registration or session failures. `veto models --json`
provides a stable version-1 list of effective source/provider/model/runtime
identities and retains known zero cost separately from unknown cost or tool
support. The cost tool reports routing savings as an estimate, never as actual
provider billing. The plugin registers Hermes' `turn_route` middleware for
external user turns. Hermes resolves provider credentials only after the
middleware returns public model/provider metadata; internal events and tool
continuations bypass it, and failures fail open. `/veto-off` and `/veto off`
are session-scoped, while `/veto pin <provider>` constrains that session's
Veto admission candidates.

**Per-model disable/enable** — `buildProviderRegistry` calls `loadDisabledModels()` which reads `~/.veto/config.json` under the `"disabled_models"` key. Any model name found there is silently skipped when registering executors — it is invisible to the router and never appears as a candidate. `veto disable <model...>` adds names to this list; `veto enable <model...>` removes them. Both commands persist changes back to `config.json` and take effect on the next invocation.

The optional `routing` section in `config.json` adds pinned/favorite/allowed
model and provider lists plus excluded models/providers. Exclusion and the
legacy disabled list win over allowlists; pins are exclusive; favorites are a
stable promotion after ordinary ranking. This policy is deterministic and
network-free.

### Executors

| Executor | Transport | When used |
|----------|-----------|-----------|
| `AnthropicExecutor` | Anthropic API (HTTP) | `ANTHROPIC_API_KEY` set, no subscription |
| `OpenAIExecutor` | OpenAI Responses for GPT-5.6; Chat Completions for GPT-4.1 (HTTP) | `OPENAI_API_KEY` set |
| `OpenRouterExecutor` | OpenRouter API (HTTP) | `OPENROUTER_API_KEY` set |
| `CLIExecutor` | `claude -p` subprocess | `CLAUDE_SUBSCRIPTION=true` |
| `CodexCLIExecutor` | `codex exec` subprocess | Codex CLI has an active ChatGPT login |
| `opencode.Runtime` | OpenCode session SSE or JSON CLI subprocess | `veto opencode connect` |
| `OpenAICompatibleExecutor` | any OpenAI-compatible endpoint (HTTP) | local model configured via `veto login` |

**Subscription mode** (`CLIExecutor`) shells out to the `claude` CLI with `-p` (print mode) and `--output-format text`. This bypasses the Anthropic API entirely — cost is $0 per route because it runs under the user's flat Claude Max / Pro subscription. Subscription takes precedence over API key when both are configured.

**Codex subscription mode** (`CodexCLIExecutor`) is registered automatically
when `codex login status` succeeds. Admission runs ephemerally in a temporary
read-only workspace, ignores user config and exec-policy rules, and writes the
schema-constrained decision to a dedicated output file. Full execution runs a
normal ephemeral Codex agent in the caller's working directory so repository
instructions, tools, hooks, and the user's approval policy remain effective.
Authentication comes from the existing Codex CLI login. Veto distinguishes a
ChatGPT subscription login (known zero marginal provider cost) from API-key or
unrecognized CLI authentication, whose cost remains unknown.

All concrete transports implement the short `Run` admission path and the
separate `Execute` task path. HTTP executors send the provider-specific bounded
output field (`max_output_tokens` for OpenAI Responses, `max_tokens` for the
other transports), defaulting to 8192 for execution. GPT-5.6 admission disables
reasoning for predictable latency; full execution uses medium reasoning.
Provider length termination is exposed as truncation metadata. Buffered task
execution fails closed on that signal, so partial output is not saved or sent
to acceptance review. The Claude CLI
owns its own output controls and reports usage/truncation as unknown through
this contract; its execution path retains the real shell/read/write/edit tools.

**Local / self-hosted models** (`OpenAICompatibleExecutor`) target any server that speaks the OpenAI chat-completions API: Ollama, LM Studio, vLLM, llama.cpp. They are configured via `veto login` → option 4 and stored in `~/.veto/models.json` as `LocalModel` entries. At build time, `buildProviderRegistry` loads these and calls `lm.capabilities()` which converts them to `router.ModelCapabilities` with defaults (tier `small`, 8192-token context, no executable tools, `CostPer1k*USD = 0`). OpenAI-compatible HTTP is text-only in veto: it returns content but does not read files, write files, run commands, or invoke tools. The resulting capability list, including both built-ins and locals, is passed to `router.NewRegistryFromModels` so the scorer and filter treat local models honestly.

**Ollama auto-start** — `OpenAICompatibleExecutor.Run` calls `tryStartOllama(endpoint)` on the first failed request (`attempt == 1`). The helper checks whether the endpoint targets `localhost:11434` or `127.0.0.1:11434`, locates the `ollama` binary with `exec.LookPath`, starts `ollama serve` in the background (stdout/stderr discarded), then polls `http://localhost:11434` every 500ms for up to 10 attempts (~5s). If the server becomes reachable, it clones the original request and retries once. If the server doesn't come up in time, the original connection error is returned normally. This means veto manages the Ollama server lifecycle at inference time — the server does not need to be running before `veto run` or `veto route`.

## Event bus

`Manager.OnEvent` is a single callback wired by the CLI. All events from the routing pipeline flow through it:

| Event | Meaning |
|-------|---------|
| `filter_pass` | Model survived hard filter |
| `filter_fail` | Model pruned by hard filter (with reason) |
| `ask_start` | Admission gate sending prompt to model |
| `ask_accept` | Model accepted (with confidence, est. cost, est. tokens) |
| `ask_reject` | Model rejected (with reason codes) |
| `ask_error` | Executor error (network, auth, rate limit) — `Detail` carries the real error message |

The CLI renderer (`cmd/veto/render.go`) uses these events to drive the animated terminal display. The logger (`cmd/veto/logger.go`) uses the same events to write JSON lines to disk. Neither is coupled to the other.

## `veto exec` — multi-step plan execution (`cmd/veto/exec.go`, `cmd/veto/plan.go`)

`veto exec <plan.md>` runs a sequenced plan file, routing each step independently.

**Plan format** — a Markdown file with YAML frontmatter:

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
---
```

Each step has `task` (objective), `kind`, `risk`, optional `depends_on` (forward references are rejected at validation), and optional `success_criteria` (printed after a step completes).

**Plan loading and conversion** — `loadOrConvertPlan` first tries `ParsePlan` + `ValidatePlan`. If either fails (no frontmatter, unknown kind, invalid risk), it:
1. Prints the violations to stderr.
2. If stdout is a TTY and `--quiet` is not set: prompts the user to convert.
3. On yes: calls `convertPlan`, which routes the raw plan text to the best available model using `conversionPromptTemplate`, strips code fences from the output, re-parses and re-validates. The result is saved to `~/.veto/plans/<timestamp>-<slug>-converted.md`.

**Step execution** — steps run sequentially. Each step gets its own `context.WithTimeout` (default 60s) and its own `Renderer`. Each step's `TaskSpec.Complexity` is left unset, so `Manager.Route` infers it independently from that step's objective and kind — a plan with a "summarize logs" step and a "design distributed cache" step routes them to appropriately different tier models. `routeAndCapture` (see below) handles routing + execution. On step failure, behavior is controlled by `--on-failure`:

| Mode | Behavior |
|------|----------|
| `abort-ask` (default) | Prompt: "Continue with remaining steps? [y/N]" |
| `abort` | Exit immediately |
| `continue` | Keep going; report failed step numbers at the end |

`--on-failure` defaults from `--on-failure` flag > `~/.veto/config.json` `on_failure` key > `"abort-ask"`.

`--dry-run` prints the step list (number, kind, risk, task) without executing anything.

**Per-step criteria** — each plan step can carry a `success_criteria` string (comma or pipe-separated). `splitCriteria` parses it into a `[]string` and passes it to the step's `TaskSpec.SuccessCriteria`. After the step runs, `reviewOutput` checks the output against those criteria using a second model (quiet renderer, kind=review).

**Final integrator pass** — after all steps complete successfully, if any step had criteria, `exec` runs one more review over the combined outputs of all steps. It collects every per-step criterion into `allCriteria`, appends a built-in regression guard (`"no step undid another step's work"`), and routes a single review task with the concatenated outputs as the body. This catches cross-step regressions that per-step reviews cannot see. The integrator pass is skipped when any step failed (controlled by the `failed` slice check).

## `veto run` — route + execute (`cmd/veto/run.go`)

`veto run` is a thin wrapper around the routing pipeline that adds an execution step. After `Manager.Route` returns a winner, `cmdRun` looks up the executor for that model via the provider registry and invokes its separate full-task execution contract with `executor.ExecutionOptions{MaxOutputTokens: ...}`. The default is `executor.DefaultExecutionMaxTokens` (8192), configurable with `--max-output-tokens`; the admission probe's 512-token budget is never reused for task output.

**Streaming:** `cmdRun` first checks for `executor.EventTaskExecutor`, which
returns full result telemetry while streaming text and structured runtime
events. OpenCode and Codex implement this path. It then falls back to the legacy
optional `streamer` interface:

```go
type streamer interface {
    Stream(ctx context.Context, prompt string, w io.Writer) error
}
```

The Claude subscription CLI implements the legacy path. Other executors use
their buffered `Execute` method. Codex consumes its bounded JSONL event stream,
prints completed agent messages, records only allowlisted tool lifecycle names,
and reports CLI token usage with known zero marginal subscription cost. OpenCode exposes provider-reported usage and
cost when present; unknown pricing is not recomputed as a known zero. Its API
does not expose a portable per-prompt output-token field, so Veto still enforces
the command timeout and bounded 8 MiB event/text safety limit, while reporting
provider output-length termination as a failed truncated result.

`--quiet` on `veto run` suppresses the routing animation entirely and prints only the model's output — making `veto run --quiet "..." > file` scriptable.

`--json` on `veto route` is the stricter scripting mode for agent infrastructure. It implies `--quiet` and `--no-resume`, suppresses checkpoint prompts, and emits a single JSON object on stdout. Successful routes include `model`, stable `source`/`provider`/`api_model`/`runtime` identity, `tier`, `kind`, `risk`, `complexity`, `confidence`, and `saved_usd`. `--runtime` restricts the effective registry before admission so a host integration never selects a model it cannot execute. No-candidate routes exit non-zero with `{"error":"no_candidate",...}` and include `provider_errors` when transports failed. Legitimate model rejections and hard-filter exhaustion omit that field.

The `veto route --timeout` value is a per-model admission deadline. A transport
failure is logged with its normalized detail and consumes one of the three
admission calls for that run; checkpoint resume can continue with untried
candidates later.

The timeout on `veto run` (default two hours) covers both routing and execution, unlike `veto route` which only times out the admission phase. `veto run --admission-timeout` separately bounds each admission attempt and defaults to 60 seconds. When the `CLIExecutor` (`claude -p`) is killed by a timeout, it distinguishes `"claude cli admission: timed out"` from `"claude cli execution: timed out"` rather than exposing the raw `"signal: killed"` from the subprocess.

On Unix, subscription CLI processes run in a dedicated process group. On macOS and Linux, cancellation also snapshots and kills descendants that created their own process groups, so agent-spawned tests, hooks, and pushes cannot survive a Veto timeout and continue mutating the repository in the background. Other platforms retain group or standard process cancellation behavior.

**Shared helper: `routeAndCaptureWithOptions`** — both `cmdRun` and `cmdExec` (for plan steps) share the execution helper in `run.go`. It wires `mgr.OnEvent`, calls `mgr.Route`, looks up the executor, calls its full `Execute` method with explicit options, and returns `(modelName, output, error)`. Internal conversion/review paths use the bounded default. This keeps each command's routing setup in one place (`prepareRouting`) and prevents admission and execution transports from drifting.

**Pull-request review execution:** when an objective explicitly asks the executor to fix review comments on a pull request, `executionPrompt` adds a narrow live-verification contract. The executor must query GitHub inline `reviewThreads`, address and resolve the requested reviewer's unresolved threads, push when requested, and re-query before claiming completion. Ordinary tasks receive no added instructions.

**Explicit output files:** only `--output <relative-path>` writes a file. The
path must remain inside the current working directory and cannot target hidden
files or directories. New files are created with mode `0600`; existing files
are never replaced unless `--force` is supplied. Objective text is never parsed
as an implicit write command.

## Skills library (`cmd/veto/skills.go`)

Skill files are Markdown files with YAML frontmatter:

```markdown
---
name: code-change
kinds: [code-change]    # empty = matches all kinds
keywords: []            # empty = no keyword filter
---
- Prefer the minimal diff — change only what the task requires
- Preserve existing naming conventions and indentation style
- Write one test per changed behaviour
```

**Skill security model** — veto only loads skills from approved sources:

- `~/.veto/skills/` — always approved, auto-generated by veto, safe to edit by hand
- Other directories (e.g. `~/.claude/skills/`) — must be explicitly approved via `veto setup`
- Individual files — can be approved one-by-one via `veto setup` if directory-level approval is declined

Approval state is stored in `~/.veto/config.json` under the `"skills"` key as `approved_dirs`, `approved_files`, and `auto_approve_new`. At startup (for any command except `setup` and `version`), `checkPendingSkills` scans approved directories for unapproved new files and prints a one-line reminder if any are found.

**`veto setup`** runs the interactive discovery and approval flow. It scans candidate directories (currently `~/.claude/skills/`), displays each skill file with its name, and offers approval per-directory or per-file.

**Resolution flow** for each `veto run` call:

1. `loadSkills()` reads all `.md` files from `skillSourceDirs()` (the union of `~/.veto/skills/` and user-approved dirs), filtering to only approved files in unapproved dirs.
2. `matchSkills(spec)` separates matches into kind-specific (skill has `kinds` list that includes the task kind) and generic (empty `kinds`). Kind-specific are preferred; combined list capped at 2.
3. `withSkills(objective, bodies)` prepends matched skill bodies under `## Relevant skills` before the task objective. Internal/meta routes (review, plan conversion) pass `nil` to avoid recursion.

Skills are **never auto-generated during a routing call**. `resolveSkills` only reads from pre-existing approved files — there is no hidden upstream routing call before the animation starts. `generateSkill` still exists for offline/manual skill creation but is no longer part of the hot path. Skills can be hand-written and placed in `~/.veto/skills/<kind>.md`; veto uses the file as-is on the next call.

## Acceptance-criteria review (`cmd/veto/review.go`)

When `--criteria "..."` is supplied to `veto run`, a second routing call runs after execution:

1. `buildReviewPrompt` constructs a JSON-response prompt that includes the original objective, the acceptance criteria, and the model's output.
2. `reviewOutput` routes this as a `review/low` task using `TaskSpec.SkipModels = [executorModel]` — the model that produced the output is excluded to prevent self-grading bias.
3. The reviewer must respond with JSON only:

```json
{
  "passed": true,
  "score": 0.83,
  "criteria": [
    { "criterion": "no third-party JWT dep", "met": true, "note": "only stdlib crypto/hmac used" },
    { "criterion": "all existing tests pass", "met": false, "note": "TestRefresh fails — signature mismatch" }
  ]
}
```

4. `render.PrintReview` displays the per-criterion table. If `passed` is false, `veto run` exits with code 1.

If criteria were requested and no review-capable model is available, routing
fails, the reviewer returns malformed JSON, or the result is incomplete or
internally inconsistent, the quality gate fails closed and the command exits
non-zero. An output is not considered verified merely because review was
unavailable.

`TaskSpec.SkipModels` is a general mechanism: it causes the Manager to skip those model names in the admission loop. It is also used by checkpoint resume (already-tried models are skipped on re-entry).

## Feedback reports (`cmd/veto/feedback.go`)

`veto feedback` is a separate, scriptable reporting path. It collects the issue
form vocabulary (kind, summary, reproduction/context, expected and actual
behavior, scope, acceptance criteria, and optional performance evidence),
redacts secrets and local paths, writes a `0600` report below
`~/.veto/feedback/`, and optionally opens a bounded prefilled GitHub issue URL.
It does not read credentials, task text, provider responses, terminal history,
or Veto state to populate a report. Provider/model metadata is excluded unless
the user explicitly opts in. `--stdin --json` is the agent/script interface;
the JSON result includes the saved path and whether browser payload shortening
was required. GitHub authentication remains the user's responsibility.

The post-run prompt is disabled by default, is only available on a TTY, and can
be enabled with `post_run_feedback: true` in `~/.veto/config.json`. `run` and
`exec` accept `--no-feedback` as an explicit per-invocation opt-out.

## Installation diagnostics (`cmd/veto/doctor.go`)

`veto doctor` is a separate, provider-free command path. Dispatch bypasses the
pending-skill scan, and the diagnostic engine never builds a provider registry
or performs credential/provider connectivity checks. Filesystem, executable,
PATH, build-metadata, command lookup, and HTTP boundaries are injectable for
tests.

Checks return stable IDs with `PASS`, `WARN`, `FAIL`, or `FIXED`, a message, and
a repairability flag. JSON adds summary counts and a final `ok`; warnings such
as source builds or `--offline` checksum skips do not change the exit code, but
unresolved failures do.

The release integrity path is enabled only for artifacts marked `official` by
the packaging script. It fetches `BINARY_SHA256SUMS` for the exact embedded
version. `--fix` additionally verifies `SHA256SUMS`, constrains archive content
to the expected platform binary, checks the candidate's version, and performs
a rollback-protected same-directory replacement. See
[ADR-003](decisions/ADR-003-release-provenance-and-repair.md) for the trust and
ownership boundaries.

## Interactive release updates (`cmd/veto/update*.go`)

Before normal command dispatch, an interactive build with a stable version
consults `~/.veto/update.json`. The private cache limits GitHub's unauthenticated
latest-release API check to once per 24 hours; a bad clock, malformed cache,
network failure, or incomplete release fails open and never blocks the command.
JSON, quiet, piped, and development invocations bypass the updater.

Only a stable tag with all six platform archives plus `SHA256SUMS` and
`BINARY_SHA256SUMS` is offered. Installation always requires an explicit `y`.
Homebrew owns Homebrew paths, source builds use an exact versioned `go install`,
and other package-manager paths are refused. Official standalone replacement
reuses the doctor trust path: both manifests, archive containment, binary hash,
candidate version, path ownership, permissions, and rollback behavior must all
pass. See [ADR-004](decisions/ADR-004-automated-releases-and-consented-updates.md).

## Credential storage

`veto login` stores API keys in `~/.veto/credentials.json` (mode 0600, JSON object of `ENV_KEY → value`). At runtime, environment variables take precedence — the credentials file is only consulted when the env var is absent. OpenRouter browser login uses its documented S256 PKCE flow, an ephemeral IPv4 loopback listener, and a random callback-path nonce. Only the exchanged Veto-owned key is persisted; the verifier, authorization code, and callback nonce remain in memory. Manual key entry remains supported.

Local model definitions are stored separately in `~/.veto/models.json` (mode 0600, JSON array of `LocalModel`). `saveLocalModel` replaces by name if the name already exists. `veto logout` removes entries from either file: API keys via `removeCredential`, local models via `removeLocalModel`. Both interactive (menu) and non-interactive (`veto logout <name>`) modes are supported.

OpenCode connection metadata is stored in the `opencode` section of
`~/.veto/config.json` as a mode and optional loopback URL. It contains no
OpenCode provider credentials. Environment-supplied OpenCode server Basic Auth
can protect an attached endpoint but is never copied into this section.
Disconnect deletes only this section.

`loginLocalModel` (option 5) runs in a loop: after each model is registered, it asks "Add another local model? [y/N]", allowing multiple local models to be registered in a single `veto login` invocation. Option 6 connects OpenCode runtime discovery.

## Logging

One log file per calendar day: `~/.veto/logs/veto-YYYY-MM-DD.log`. Each line is
a versioned, allowlisted lifecycle envelope defined in
[`docs/event-ledger.md`](event-ledger.md). Run and task IDs correlate routing,
execution, artifact, and review events without persisting objectives, prompts,
or responses. Sensitive error detail is redacted and bounded before writing.
Files older than 7 days are pruned on each routing invocation. If the log file
cannot be created, routing continues with the ledger discarded.

`history.json` remains separate: it preserves backward-compatible admission
and execution aggregates used by the scorer. Corrupt or legacy history falls
back conservatively and is not rewritten by the event ledger.
