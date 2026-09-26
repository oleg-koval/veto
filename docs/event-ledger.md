# Event ledger

Veto writes lifecycle events as newline-delimited JSON to
`~/.veto/logs/veto-YYYY-MM-DD.log`. The ledger is diagnostic telemetry;
`~/.veto/history.json` remains the backward-compatible aggregate input used by
routing signals.

## Envelope version 1

Every valid line contains:

```json
{
  "schema_version": 1,
  "timestamp": "2026-08-30T07:00:00Z",
  "event_id": "32 lowercase hexadecimal characters",
  "run_id": "correlation identifier",
  "type": "admission.accepted",
  "task_id": "task correlation identifier",
  "task_kind": "plan",
  "risk": "low",
  "model": "sol",
  "runtime": "openai-api",
  "status": "success"
}
```

`run_id` is unique to one Veto command invocation. `task_id` is the stable
task correlation and may recur when the same task is routed again.

Optional typed fields carry reason codes, confidence, estimates, known usage,
known cost, known latency, and bounded error detail. Unknown values are omitted
rather than serialized as zero; accepted zero-cost estimates remain explicit.

Event types are namespaced under `route`, `decision`, `admission`, `execution`, `tool`,
`approval`, `artifact`, `review`, and `goal`. New fields may be added within
schema version 1. Breaking interpretation changes require a new version.

Agent runtimes map tool state, approval decisions, and artifact existence into
those namespaces. The optional detail contains only a bounded tool/artifact
kind and count (for example `name=patch count=2`), never tool arguments, tool
output, paths, file contents, or raw provider events. OpenCode usage and cost
are persisted only when OpenCode reports them.

## Decision boundary events

`decision.started` is emitted immediately before each engine call, after the
filter/shortlist events. Exactly one `decision.completed` (valid selection or
no selection) or `decision.error` (engine failure or invalid outcome) follows.
Legacy admission events retain their order inside this boundary, including the
live `ask_start` before the provider call and persistence before terminal events.
No boundary events are emitted when there is no eligible request to dispatch.

These events retain the schema-1 envelope and correlation IDs and add a typed
`decision` object, independently versioned with `contract_version: 1`:

```json
{"contract_version":1,"mode":"sequential-admission","candidate_count":3,"status":"selected","selected_model":"sol"}
```

Status is `started`, `selected`, `no_selection`, or `error`. Only valid completed
outcomes may add `selected_model` and measured `input_tokens`, `output_tokens`,
`total_tokens`, `cached_input_tokens`, `cost_usd`, or `latency_ms`. Unknown fields
are absent; known zero is explicit, including independently known cached input.
The default sequential engine reports no measured decision telemetry. Admission
estimates stay on legacy admission events and are not copied into decision usage.

The mapping excludes task text, prompts/responses, credentials, raw provider
payloads, reason details, errors and file contents. It also ignores generic
progress-event model/reasons/detail/estimates in favor of the structural payload.
Decision telemetry is nested to avoid conflating it with execution accounting.
Normal/quiet/JSON routing output and checkpoint updates ignore these events.
Old schema-1 lines without `decision` continue to read and render unchanged;
aggregate `history.json` is unchanged.

## Privacy and recovery

The envelope has no objective, prompt, response, credential, cookie, or raw
browser-content field. Detail is whitespace-normalized, limited to 500 bytes,
and redacts authorization values, API keys, tokens, passwords, and common
provider-key prefixes before persistence.

Replay accepts current-schema lines independently. Malformed, incomplete, or
incompatible lines are counted and skipped so one damaged record does not hide
later events. An oversized line stops replay with an explicit error.

Existing `history.json` files retain their current format and legacy fallback
behavior. The ledger does not rewrite history, checkpoints, configuration, or
credentials.
