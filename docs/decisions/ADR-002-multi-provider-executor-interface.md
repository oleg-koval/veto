# ADR-002: Provider executors behind an interface, concrete types in pkg/executor

## Status
Accepted

## Date
2026-06-27

## Context

veto supports multiple AI providers (Anthropic, OpenAI, OpenRouter, local OpenAI-compatible servers, and the Claude subscription CLI). The admission gate in `pkg/router` needs to call models, but routing should not import provider-specific SDKs — that would create coupling between routing logic and provider implementations, making both harder to test and extend. Full task execution has different output limits and transport capabilities from the short admission probe, so it must not share the admission-only call implicitly.

## Decision

Define the `Executor` interface at the consumer (`pkg/router`), not the implementer (`pkg/executor`). Concrete implementations in `pkg/executor` satisfy this interface via duck typing — they don't import `pkg/router`.

Keep the router-facing admission contract deliberately small: `Run(ctx,
prompt)` is the fixed 512-token JSON probe. `router.AdmissionResult` aliases
the stable `pkg/execution.Result` contract so executors returning the prior
`pkg/executor.Result` alias remain source-compatible. The router consumes only
`Output` and `Error`. Full task execution separately uses
`TaskExecutor.Execute(ctx, prompt, ExecutionOptions)` with a bounded output
budget and provider telemetry.

The wiring happens at the CLI layer (`cmd/veto/main.go`) via `providerRegistry`, a concrete `ExecutorFactory` that maps model names to executors. This is the only place that imports both packages.

```
cmd/veto/main.go ───────▶ pkg/router/admission.go ───────▶ pkg/execution/Result
       │                  Executor + tool DTO               ▲
       │                                                    │
       └────────────────▶ pkg/executor/ transports ─────────┘
```

The `ExecutorFactory` interface (also in `pkg/router`) lets the admission gate select the right executor per model name without knowing how providers are organized.

## Alternatives considered

### Single shared executor with a provider field
One `Executor` struct that switches on a `provider` enum internally.

Rejected: forces all provider logic into one file, makes the switch statement grow with every new provider, and makes it impossible to test providers in isolation.

### Provider interfaces in a shared `pkg/interfaces` package
Define `Executor` in a neutral package, import it from both `pkg/router` and `pkg/executor`.

Rejected: creates a third package just to hold an interface that only `pkg/router` consumes. The Go convention is to define interfaces at the point of use.

### Import `pkg/executor` directly from `pkg/router`
Let the router know about concrete executor types.

Rejected: couples routing logic to provider implementations. Adding a new provider would require changes inside `pkg/router`.

## Consequences

- `pkg/router` owns the admission-facing interface and tool DTO; it depends only on the stable result contract, never provider transports or full-execution options
- Existing external executors returning `pkg/executor.Result` remain compatible because that type and `router.AdmissionResult` alias the same stable result
- New providers require a new file in `pkg/executor` and one entry in `buildProviderRegistry()` — no changes to the router package
- Every production provider must implement both the admission `Run` path and the full-task `TaskExecutor` path; HTTP transports remain text-only until a real tool loop is added
- `cmd/veto/main.go` is the only place with full knowledge of the wiring; it acts as a composition root
- Tests for the admission gate use `pkg/router/mocks/executor.go` (generated via moq) — the mock satisfies the interface defined in `pkg/router`, not one imported from elsewhere
