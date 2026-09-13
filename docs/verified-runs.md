# Verified Runs

Verified Runs make an evidence-backed outcome claim for one `veto run` call.
They do not turn Veto into a test runner, security scanner, deployment system,
or approval authority.

## Use

```bash
veto run \
  --criteria-file acceptance.json \
  --evidence evidence.json \
  --verified-receipt verified-run.json \
  "Reduce checkout latency without changing payment behavior"
```

`acceptance.json` is versioned JSON:

```json
{"version":1,"criteria":["Checkout tests pass","p95 is below 100 ms"]}
```

`evidence.json` binds at least one bounded evidence item to every criterion:

```json
{
  "version": 1,
  "evidence": [
    {"id":"tests","criterion":"Checkout tests pass","type":"test","summary":"CI suite passed"},
    {"id":"benchmark","criterion":"p95 is below 100 ms","type":"benchmark","summary":"p95: 810 ms to 27 ms"}
  ]
}
```

Veto redacts and sends only the supplied summaries and optional digests to an
independent reviewer. It does not read referenced artifacts, execute commands, or record
paths, source code, prompts, or raw model output in the receipt.

## Outcomes

- `verified_pass`: every criterion has evidence and the independent review
  passes.
- `verified_fail`: evidence-backed review found an unmet criterion.
- `inconclusive`: review was unavailable, malformed, or insufficient.

An execution completing successfully is not verification. Human approval is
also separate from verification and is not modelled as evidence in v1.

Receipts are private local files under `~/.veto/receipts/` (directory `0700`,
files `0600`). `--verified-receipt` exports a redacted copy to an explicit,
safe relative path without overwriting an existing file.

## Reporting

```bash
veto verified-runs list
veto verified-runs report
veto verified-runs report --json
```

V1 reports execution cost per verified pass only if every stored receipt has a
known execution cost. Admission and reviewer provider costs are not yet
consistently observable, so they are never presented as zero or as whole-run
costs. Verified outcomes do not influence routing in v1.
