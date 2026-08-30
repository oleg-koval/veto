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

Optional typed fields carry reason codes, confidence, estimates, known usage,
known cost, known latency, and bounded error detail. Unknown usage, cost, and
latency are omitted rather than serialized as zero.

Event types are namespaced under `route`, `admission`, `execution`, `tool`,
`approval`, `artifact`, `review`, and `goal`. New fields may be added within
schema version 1. Breaking interpretation changes require a new version.

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
