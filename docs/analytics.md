# Analytics and data use

Veto is local-first. It records a bounded, redacted diagnostic ledger so the
user can understand routing and execution behavior. That ledger stays on the
user's machine and is not product analytics.

## Current behavior

The local ledger is written to `~/.veto/logs/veto-YYYY-MM-DD.log` and rotated
after seven days. It can contain task kind, risk, runtime and model names,
route outcomes, reason codes, confidence, known usage, known cost, known
latency, and bounded error detail. It does not contain prompts, objectives,
responses, credentials, cookies, local paths, file contents, terminal history,
raw provider events, or browser content.

No Veto command currently sends this ledger or any other usage data to a Veto
server or analytics provider.

## User controls

```text
veto analytics status
veto analytics status --json
veto analytics enable
veto analytics disable
```

The default is `not set`, which behaves as opt-out. `enable` records an explicit
opt-in preference for a future reviewed remote export. It sends nothing today
because Veto has no remote analytics transport. `disable` records opt-out and
is the withdrawal control for any future transport.

The preference is stored in the `analytics` section of
`~/.veto/config.json`, with a policy version and update timestamp. A future
transport must check the current policy version and the opt-in preference. It
must fail closed for missing, invalid, stale, or opt-out state.

## Future export boundary

Before remote collection is implemented, Veto must publish the exact endpoint,
payload, purpose, retention, deletion process, and network-metadata handling.
The current opt-in preference is not permission to silently expand the payload
or add a new recipient.

A future aggregate export may be considered for routing-quality and
compatibility questions using coarse, bounded fields such as Veto version,
platform family, task-kind category, runtime family, outcome category, and
bucketed cost/latency. It must not include:

- prompts, objectives, responses, task text, file contents, or terminal history;
- credentials, cookies, tokens, API keys, authorization values, or provider data;
- local paths, repository names, filenames, project hashes, stable device IDs,
  account identifiers, or raw timestamps;
- custom model names or other user-supplied identifiers without a separate,
  specific review and approval.

Even a payload without direct identifiers can arrive with network metadata such
as an IP address. Veto must not call the future service “anonymous” until the
server-side retention and deletion policy covers that metadata too.

## Legal review gate

This is an engineering data contract, not legal advice. Before a remote
transport is added, the owner must review at least:

1. the purpose and lawful basis for the collection in each target jurisdiction;
2. the service owner, processors, endpoint hosting, and any data-processing
   agreements;
3. retention, deletion, export, and correction paths, including backups;
4. the user-facing notice, versioned consent text, and withdrawal behavior;
5. network metadata, cross-border transfer, and incident handling.

Unknown legal facts remain `not assessed`; they are not treated as approval.
The [legal-skills repository](https://github.com/gfodor/legal-skills) follows
the same useful discipline of explicit checklists, source refresh, and honest
unknowns. It is a U.S. utility-patent workflow and does not replace privacy
counsel or jurisdiction-specific review.
