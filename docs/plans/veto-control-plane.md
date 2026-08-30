# Implementation Plan: Veto Control Plane

## Status

Approved by the owner on 2026-08-30. Implementation proceeds incrementally,
one focused, verified pull request at a time.

This plan supersedes the narrower interpretation that OpenCode support means
only Agent Skill discovery. Skill discovery remains useful, but this plan adds
runtime/provider integration, a native Hermes plugin, first-class interfaces,
bounded goals, and browser-capable execution.

## Objective

Turn Veto from a command-oriented model router into a local-first routing
control plane that regular users can operate without memorizing commands.

The completed product should:

- connect direct providers, OpenRouter, OpenCode, and Hermes without copying
  credentials out of OpenCode or Hermes;
- discover a broad model catalog, shortlist candidates cheaply, and let Veto
  select and execute with the best eligible model;
- expose the same providers, models, routes, executions, approvals, costs,
  health, history, and bounded goals through CLI, TUI, and local web UI;
- execute browser/UI tasks through an approved agent runtime that actually has
  browser tools;
- remain safe, observable, cross-platform, and bounded by explicit time, turn,
  cost, tool, and authorization limits.

Primary users are individual developers and technical operators who already
use one or more AI coding agents or providers. The initial release is not aimed
at non-technical consumers or unattended production automation.

## Decision Gates

### Gate 1: Automatic agent routing

**Approved:** Veto routes each new user turn in Hermes and OpenCode by
default, with a visible per-session off switch. Internal tool continuations,
review calls, and Veto's own admission calls must bypass automatic routing.

Alternative: require explicit `/veto` or a Veto tool call for every route.

### Gate 2: Interface delivery order

**Approved:** ship the cross-platform TUI first, then reuse the same local
control API and event schema for a browser UI. Do not build both presentation
layers simultaneously.

Alternative: build TUI and web UI together at the cost of slower validation of
the core interaction model.

### Gate 3: Browser ownership

**Approved:** Veto routes browser work to OpenCode, Hermes, Claude CLI, or a
future runtime adapter that exposes an approved browser capability. Veto does
not embed its own Playwright/browser engine in the first version.

Alternative: make Veto own browser lifecycle, permissions, profiles, and
artifacts directly, which materially expands security and maintenance scope.

## Verified Current State

- OpenRouter login advertises a large catalog, but the static Veto registry
  currently contains only one OpenRouter model.
- OpenCode can list provider/model metadata, run an explicit model, expose a
  loopback server, create sessions, execute prompts, and stream events. It can
  therefore act as a credential-preserving runtime adapter.
- Hermes supports native plugins, tools, slash/CLI commands, lifecycle hooks,
  and LLM request/execution middleware. It can therefore host a close Veto
  integration without modifying Hermes core.
- Veto's existing browser dashboard is route-scoped and ephemeral. It is not a
  complete monitoring or configuration interface.
- The router has internal required-tool filtering, but normal users cannot
  request browser capability through the CLI.
- Only the Claude CLI executor currently advertises executable tools, and it
  advertises shell/file tools rather than browser automation.
- `veto exec` and `veto run --criteria` are bounded primitives. Veto does not
  currently persist goals or run an execute/evaluate/revise loop.

## Architecture

```text
                         Veto Core
        catalog + policy + routing + goals + event ledger
             │             │             │
      model sources   runtime adapters   control API
             │             │             │
   direct/OpenRouter  OpenCode/Hermes    CLI/TUI/Web
             │        Claude/local       plugins
             └─────────────┴─────────────┘
                    versioned events
```

### Core contracts

1. **Model source** discovers models and supplies stable identity, provider,
   price, context, modality, status, and declared capabilities.
2. **Runtime adapter** performs admission and execution and reports effective
   tools, permissions, streaming events, usage, cost, and artifacts.
3. **Routing policy** filters the full catalog locally, scores a small
   shortlist, then admits at most three candidates.
4. **Event ledger** records versioned, redacted lifecycle events for every
   route, execution, tool call, approval, review, goal transition, and failure.
5. **Control API** exposes the same application service to CLI, TUI, local web
   UI, OpenCode integration, and Hermes plugin.

Model source and runtime adapter are intentionally separate. A model may be
discovered through OpenRouter but executed through OpenCode, or discovered and
executed through the same direct provider.

### Local control API

- Loopback-only by default; never bind a public interface implicitly.
- Versioned request/event schemas.
- Random per-instance bearer token stored with mode `0600` where supported.
- Bounded request sizes, timeouts, SSE client counts, and event retention.
- No credential-returning endpoint.
- Explicit process ownership and clean shutdown.
- CLI remains usable without a daemon by running the same service in-process.

### Capability taxonomy

Replace ad hoc tool strings with documented stable capabilities while retaining
backward-compatible JSON values:

- `shell`, `read`, `write`, `edit`
- `web-search`
- `browser-dom`, `browser-screenshot`, `browser-network`
- `computer-use`
- `mcp`

The effective runtime capability always wins over model marketing metadata.

## Delivery Plan

Each task should land as a focused PR. Provider-account checks, human UX trials,
release publication, and production use remain separate evidence.

### Phase 0: Contracts and truthful foundations

#### Task 0.1: Correct OpenRouter claims and lock current behavior

**Acceptance criteria:**

- Provider output reports the actual routable OpenRouter model count.
- Documentation no longer implies that every OpenRouter model is already
  routable.
- Existing direct-provider behavior remains compatible.

**Verification:** focused login/provider tests, root help tests, race suite,
onboarding smoke, `git diff --check`.

**Likely files:** `cmd/veto/login.go`, `cmd/veto/main.go`, associated tests,
`README.md`.

**Dependencies:** none.

#### Task 0.2: Introduce model-source and runtime-adapter contracts

**Acceptance criteria:**

- Static catalog, local models, and existing executors implement the new
  contracts without user-visible regression.
- Stable model identity distinguishes source, provider, model, and runtime.
- Effective runtime tools remain fail-closed.

**Verification:** contract tests with fakes, full race suite, vet, build,
benchmark comparison.

**Likely files:** `pkg/router/`, `pkg/executor/`, focused command wiring/tests.

**Dependencies:** Task 0.1.

#### Task 0.3: Define versioned events and persistent execution ledger

**Acceptance criteria:**

- Routing, execution, usage, tool, approval, artifact, review, and goal events
  use a documented versioned envelope.
- Secrets and raw credentials cannot enter serialized events.
- Existing history is readable or migrated conservatively.

**Verification:** redaction tests, schema golden tests, corrupt-ledger recovery,
race suite.

**Likely files:** new package under `pkg/`, `cmd/veto/logger.go`, migration and
tests, `docs/architecture.md`.

**Dependencies:** Task 0.2.

### Checkpoint A: Foundation

- Existing CLI contract passes unchanged.
- Static and local models route and execute normally.
- Event schema and migrations receive owner review before integrations begin.

### Phase 1: Real OpenRouter integration

#### Task 1.1: Add a bounded OpenRouter catalog client and cache

**Acceptance criteria:**

- Veto fetches model ID, name, context, modalities, supported parameters,
  status, and per-token pricing from the official catalog.
- Responses have time and size limits and are cached with timestamp/ETag.
- Offline and stale-cache states are explicit.
- Malformed or partial catalog data never replaces a known-good cache.

**Verification:** fake HTTPS server tests for valid, oversized, malformed,
stale, conditional, timeout, and rollback cases.

**Likely files:** OpenRouter model-source package and tests, state paths,
doctor checks.

**Dependencies:** Checkpoint A.

#### Task 1.2: Add model selection policy for large dynamic catalogs

**Acceptance criteria:**

- Hundreds of discovered models are filtered locally before paid admission.
- At most three candidates receive admission calls.
- Users can pin, favorite, allow, disable, and exclude models/providers.
- Unknown price or capability remains distinct from zero/unsupported.

**Verification:** deterministic corpus tests, cost ceiling tests, unknown-data
tests, benchmark comparison.

**Likely files:** `pkg/router/`, configuration model, tests, benchmark corpus.

**Dependencies:** Task 1.1.

#### Task 1.3: Add OpenRouter OAuth PKCE login

**Acceptance criteria:**

- Browser-based localhost PKCE succeeds without exposing the verifier or key.
- Manual API-key login remains supported.
- Cancellation, callback spoofing, timeout, and port-conflict cases fail safely.
- Logout removes only Veto-owned OpenRouter authorization.

**Verification:** fake authorization server, callback/state tests, permission
checks, onboarding smoke.

**Likely files:** login/auth helper, credential store, tests, README.

**Dependencies:** Task 1.1.

### Checkpoint B: OpenRouter vertical slice

- A fresh user connects OpenRouter, sees the dynamic catalog, routes from a
  local shortlist, executes, and sees honest cost/usage status.
- No claim is made from a real provider without a separate opt-in account test.

### Phase 2: OpenCode runtime integration

#### Task 2.1: Discover and connect an OpenCode runtime

**Acceptance criteria:**

- Veto detects the OpenCode CLI and an explicitly configured loopback server.
- Connected providers/models are obtained without reading OpenCode credential
  files.
- Veto supports attach, managed local server, CLI fallback, and disconnect.
- Version incompatibility is diagnosed clearly.

**Verification:** fake OpenCode API, subprocess fakes, hostile URL tests,
timeouts, Windows path tests, doctor coverage.

**Likely files:** new OpenCode adapter package, command wiring, doctor/tests,
README.

**Dependencies:** Checkpoint A.

#### Task 2.2: Route and execute through OpenCode sessions

**Acceptance criteria:**

- Veto can admit and execute a specific `provider/model` through OpenCode.
- Streaming text, tool calls, approvals, usage, artifacts, cancellation, and
  failures map into Veto events.
- Veto never passes OpenCode `--auto` implicitly.
- Internal Veto admission sessions are isolated and identifiable.

**Verification:** fake SSE/session server, CLI fallback subprocess tests,
cancellation, permission denial, partial output, and restart recovery.

**Likely files:** OpenCode runtime adapter and tests, control events, command
wiring.

**Dependencies:** Tasks 2.1 and 0.3.

#### Task 2.3: Add OpenCode-side Veto integration

**Acceptance criteria:**

- Users can inspect Veto status and explicitly route from OpenCode.
- Automatic routing mode follows Gate 1 and has a visible session override.
- A recursion marker prevents Veto admissions and internal continuations from
  re-entering the router.

**Verification:** isolated OpenCode configuration, plugin/tool smoke, recursive
call test, permission test.

**Likely files:** `integrations/opencode/`, integration docs/tests.

**Dependencies:** Task 2.2 and Gate 1.

### Checkpoint C: OpenCode vertical slice

- A user with OpenCode credentials can use those models through Veto without
  copying credentials.
- Tool permissions and browser capability are reported, not assumed.

### Phase 3: Native Hermes integration

#### Task 3.1: Ship a native Hermes plugin with explicit tools and commands

**Acceptance criteria:**

- Plugin manifest passes `hermes plugins doctor`.
- Plugin registers Veto status, route, run, models, cost, and cancel tools.
- `/veto`, `/models`, `/route`, `/cost`, and `/veto-off` commands are available.
- Veto absence or version mismatch produces a useful error, not a broken Hermes
  session.

**Verification:** isolated `HERMES_HOME`, plugin doctor, tool/command contract
tests, install/enable/disable smoke.

**Likely files:** `integrations/hermes/veto/` plugin package, tests, docs.

**Dependencies:** control API and Task 0.3.

#### Task 3.2: Add automatic Hermes LLM routing middleware

**Acceptance criteria:**

- Middleware can rewrite eligible first-turn provider/model requests from a
  Veto decision.
- Internal calls and tool continuations bypass routing.
- Per-session disable, provider pinning, timeout fallback, and decision trace
  are visible.
- Middleware never causes duplicate provider execution.

**Verification:** fake middleware host, recursion tests, fallback tests,
single-execution assertion, real isolated Hermes smoke without account data.

**Likely files:** Hermes plugin middleware/tests, Veto control endpoint, docs.

**Dependencies:** Task 3.1 and Gate 1.

### Checkpoint D: Agent integration

- OpenCode and Hermes each demonstrate explicit routing, automatic mode,
  disable/pin behavior, and safe fallback.
- A user can identify which system executed the task and why.

### Phase 4: Cross-platform flagship TUI

Use a mature Go terminal framework only after Gate 2 approval. Bubble Tea,
Bubbles, and Lip Gloss are the recommended stack, subject to dependency and
license review.

#### Task 4.1: TUI shell, navigation, theme, and accessibility

**Acceptance criteria:**

- `veto` with no arguments or `veto tui` launches the agreed interface without
  breaking existing non-interactive behavior.
- Keyboard and mouse navigation, narrow terminals, no-color mode, reduced
  motion, and screen-reader-friendly text fallbacks are supported.
- Linux, macOS, and Windows builds pass.

**Verification:** model/update unit tests, PTY smoke, resize/key tests, CI matrix,
manual terminal QA protocol.

**Likely files:** new `internal/tui/` package, root dispatch, tests, docs.

**Dependencies:** Gate 2 and stable control service.

#### Task 4.2: Provider onboarding and model explorer

**Acceptance criteria:**

- Users connect/disconnect providers and runtimes without memorizing commands.
- Model view exposes source, runtime, price, context, tools, status, pins, and
  exclusions.
- Secrets are masked and never enter events or screenshots.

**Verification:** fake provider flows, snapshot/model tests, keyboard-only QA,
small-terminal QA.

**Likely files:** focused TUI screens/components and tests.

**Dependencies:** Task 4.1 and integration vertical slices.

#### Task 4.3: Task composer and live routing graph

**Acceptance criteria:**

- Users submit objective, risk, kind, budget, tools, output, and criteria from a
  single screen.
- Filtering, shortlist, admissions, winner, execution, review, and failure are
  visible live.
- Every visual state has a text equivalent and stable event source.

**Verification:** event-replay tests, cancellation, empty/error/loading states,
resize/mouse/key QA.

**Likely files:** composer/route screens, event adapters, tests.

**Dependencies:** Tasks 4.1, 0.3, and control API.

#### Task 4.4: Monitoring, history, health, and settings

**Acceptance criteria:**

- Users can monitor active sessions, tools, approvals, cost, tokens, latency,
  artifacts, goals, and provider health.
- History supports redacted inspection and bounded retention.
- Doctor findings link to safe repairs without broad automatic mutation.

**Verification:** ledger replay, corrupt state, retention, doctor repair, and
cross-platform permission tests.

**Likely files:** monitoring/history/health screens and tests.

**Dependencies:** Tasks 4.1-4.3.

### Checkpoint E: TUI beta

- Fresh-user onboarding can be completed without documentation.
- Experienced users can perform every core CLI flow from the TUI.
- Human trials cover macOS, Linux, Windows, keyboard-only use, and terminal
  resizing before release claims.

### Phase 5: Bounded goals and browser-capable execution

#### Task 5.1: Add explicit tool requirements to public task contracts

**Acceptance criteria:**

- CLI, plan, control API, TUI, and plugins accept stable required capabilities.
- A browser task cannot route to a text-only runtime.
- Missing capabilities fail before paid admission.

**Verification:** CLI/JSON compatibility tests, hard-filter tests, plan parsing,
TUI contract tests.

**Likely files:** task spec, CLI flags, plan schema, API schema/tests.

**Dependencies:** capability taxonomy and TUI composer.

#### Task 5.2: Map agent-runtime browser events and artifacts

**Acceptance criteria:**

- Approved OpenCode/Hermes/Claude browser tools map to stable Veto events.
- Screenshot, DOM assertion, console, and network evidence are identifiable and
  bounded.
- Credentials, cookies, storage tokens, and unrelated page data are excluded.

**Verification:** fake browser runtime, hostile content, oversized artifact,
redaction, cancellation, permission denial.

**Likely files:** runtime adapters, event/artifact schema, tests.

**Dependencies:** Tasks 2.2, 3.2, and 5.1; Gate 3.

#### Task 5.3: Add a bounded goal runner

**Acceptance criteria:**

- Goal state persists objective, finite plan, current step, criteria, attempts,
  approvals, cost, time, and artifacts.
- Every goal requires maximum turns, time, and cost.
- Execute/evaluate/revise stops on success, limit, repeated failure, cancel, or
  required approval.
- Pause/resume is deterministic after restart.

**Verification:** state-machine tests, crash/restart, limit enforcement,
fail-closed review, repeated-failure stop, cancellation races.

**Likely files:** new `pkg/goal/`, ledger events, command/API wiring, tests.

**Dependencies:** event ledger, acceptance review, Tasks 5.1-5.2.

### Checkpoint F: Browser and goals beta

- A browser-capable runtime can build or test a UI under explicit limits.
- Veto shows evidence and stop reasons, but does not claim visual correctness
  without the required assertions or human QA.

### Phase 6: Local web UI

#### Task 6.1: Stabilize the local control API for multiple clients

**Acceptance criteria:**

- TUI and web client receive identical state from snapshot plus event replay.
- Reconnect, backpressure, version mismatch, and multi-client behavior are
  bounded and tested.
- No public bind or remote access is enabled by default.

**Verification:** API conformance, SSE reconnect, slow client, auth, CSRF/origin,
and version negotiation tests.

**Likely files:** control API, schema docs, conformance tests.

**Dependencies:** proven TUI event model.

#### Task 6.2: Build the local browser interface

**Acceptance criteria:**

- Provider onboarding, model explorer, composer, live graph, monitoring,
  history, doctor, settings, and goals match TUI capabilities.
- Desktop/tablet layouts and accessibility checks pass.
- Static assets are embedded in release archives without requiring Node at
  runtime.

**Verification:** unit tests, Playwright flows, axe checks, browser console,
desktop/tablet screenshots, fresh binary smoke.

**Likely files:** web source/build config, embedded asset package, API client,
tests.

**Dependencies:** Task 6.1 and Gate 2.

### Checkpoint G: Unified interface release candidate

- CLI, TUI, and web UI pass shared conformance scenarios.
- Release archives are validated on all six supported platform targets.
- Provider-account checks and cross-platform human trials remain separately
  reported.

## Security and Privacy Invariants

- Never read or copy OpenCode/Hermes credential stores.
- Never disclose a task objective to an unapproved provider/runtime.
- Never query every dynamic model for admission.
- Never auto-approve OpenCode, Hermes, browser, shell, file, or network actions.
- Never treat model-declared tools as effective runtime tools.
- Never persist credentials, cookies, storage tokens, or unredacted browser
  content in the event ledger.
- Never run an unbounded goal; limits are mandatory and fail closed.
- Never expose the local control API publicly by default.
- Objective text alone never authorizes file, browser, account, or publication
  mutations.

## Not Doing in the Initial Program

- Copying credentials between Veto, OpenCode, and Hermes.
- Asking hundreds of OpenRouter models to self-admit.
- Building a Veto-owned browser engine before runtime reuse is validated.
- Shipping a native desktop wrapper before the local web UI proves a need.
- Remote multi-user control-plane hosting.
- Provider-quality, savings, visual-correctness, or production-readiness claims
  without current evidence.

## Required Verification per Implementation PR

At minimum:

```bash
go test -race -timeout 120s ./...
go vet ./...
go build ./cmd/veto
VETO_BINARY=/path/to/fresh/veto ./scripts/onboarding-smoke.sh
go run ./cmd/veto benchmark --corpus internal/eval/testdata/routing_corpus.json
git diff --check
```

Integration PRs additionally require isolated fake-runtime tests and the
relevant local CLI/plugin doctor. TUI and web PRs require their focused runtime
QA protocols. Release publication remains a separate owner-approved action.

## Execution Handoff

After the three decision gates are answered:

1. Update this document with the chosen decisions and change status to
   `Approved`.
2. Start a fresh context from current `origin/main` in an isolated worktree.
3. Use this document as the source of truth; do not replay exploratory chat.
4. Begin with Task 0.1 only.
5. Complete its focused/full verification, commit, push, and PR before Task 0.2.
6. Revisit the plan only when evidence changes a contract or scope decision.

Suggested fresh-context prompt:

```text
Implement the next unchecked task in docs/plans/veto-control-plane.md from a
fresh isolated branch based on current origin/main. Treat the plan as the source
of truth, preserve unrelated work, run the specified verification, and deliver
a populated PR. Do not advance past the next checkpoint without owner review.
```

## Primary References

- OpenCode server API: <https://dev.opencode.ai/docs/server/>
- OpenCode CLI: <https://dev.opencode.ai/docs/cli/>
- OpenRouter models API: <https://openrouter.ai/docs/api/api-reference/models/get-models>
- OpenRouter OAuth PKCE: <https://openrouter.ai/docs/guides/overview/auth/oauth>
- Hermes plugins: <https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/plugins.md>
- Hermes middleware: <https://github.com/NousResearch/hermes-agent/blob/main/docs/middleware/README.md>
