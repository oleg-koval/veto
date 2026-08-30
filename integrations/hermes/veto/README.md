# Veto for Hermes Agent

This native Hermes plugin exposes explicit Veto routing tools and slash
commands. It does not read Hermes credentials, replace built-in tools, register
permission hooks, or enable automatic routing. Automatic first-turn routing is
a separate opt-in middleware feature planned for the next integration slice.

Install and enable it with:

```sh
veto hermes plugin install
hermes plugins doctor veto --ci
hermes plugins enable veto --no-allow-tool-override
```

Restart Hermes, then use `/veto`, `/models`, `/route <objective>`,
`/cost <objective>`, or `/veto-off`. Disable before uninstalling:

```sh
hermes plugins disable veto
veto hermes plugin uninstall
```

`VETO_BINARY` may point to a specific Veto executable. All calls are made as
argument arrays without a shell, have time and response-size limits, and return
structured errors instead of raising into the Hermes session.
