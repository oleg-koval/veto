# veto

Go CLI tool that routes tasks to AI models via self-admitting receivers.

## Commands

Always use `rtk proxy` prefix for Go commands — the RTK hook filters verbose output and saves tokens:

```
rtk proxy go test -race ./...
rtk proxy go build ./...
rtk proxy go test -race -run TestFoo ./pkg/router/
```

Never run raw `go test` or `go build` directly.

## Testing

- Always run tests with `-race` flag
- Full suite: `rtk proxy go test -race ./...`
- Single package: `rtk proxy go test -race ./pkg/router/`
- Benchmarks: `rtk proxy go test -bench=. -benchmem ./pkg/router/`

## Project layout

- `pkg/router/` — admission gate, scoring, store, manager (core routing logic)
- `pkg/executor/` — Anthropic, OpenAI, OpenRouter, ClaudeCLI HTTP executors
- `cmd/veto/` — CLI entry point, renderer, dashboard, login, checkpoint
