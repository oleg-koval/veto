# Veto for OpenCode

This directory is the canonical source for Veto's embedded OpenCode
integration. Install it from a Veto release binary:

```bash
veto opencode connect
veto opencode plugin install
```

Restart OpenCode after install. Automatic routing is on for each new session.
Use `/veto-off`, `/veto-on`, `/veto-status`, or `/veto-route <task>`. Agents can
inspect the same bounded interface through `veto_status` and `veto_route`.
Status probes only `veto version`; it does not load providers or return paths.

The plugin never reads credentials, opens a shell, changes OpenCode permission
rules, or auto-approves tool calls. It calls the exact `veto` executable on
`PATH` (or `VETO_BINARY`) with an argument array. Internal Veto sessions and
synthetic continuations bypass routing. Failures preserve OpenCode's current
model.

Development checks:

```bash
node --test integrations/opencode/*.test.mjs
go test ./integrations/opencode ./cmd/veto
```
