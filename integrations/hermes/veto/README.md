# Veto for Hermes Agent

This native Hermes plugin exposes Veto routing tools, slash commands, and
bounded automatic routing for external user turns. Hermes remains the owner of
credentials, provider clients, tools, and permissions; Veto only returns public
model/provider selection metadata. Internal calls and tool continuations bypass
automatic routing, and failures fall back to Hermes' configured route.

Install and enable it with:

```sh
veto hermes plugin install
hermes plugins doctor veto --ci
hermes plugins enable veto --no-allow-tool-override
```

Restart Hermes, then use `/veto`, `/models`, `/route <objective>`, or
`/cost <objective>`. `/veto off` and `/veto-off` disable automatic routing for
the current session; `/veto on` enables it again. Pin or clear a provider with
`/veto pin <provider>` and `/veto pin off`. `/veto status` shows the latest
decision trace. Disable before uninstalling:

```sh
hermes plugins disable veto
veto hermes plugin uninstall
```

`VETO_BINARY` may point to a specific Veto executable. All calls are made as
argument arrays without a shell, have time and response-size limits, and return
structured errors instead of raising into the Hermes session.
