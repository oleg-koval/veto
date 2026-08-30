# OpenRouter catalog cache

Veto's OpenRouter catalog client reads the official
[`GET /api/v1/models`](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties)
response. It requests all output modalities with a maximum of 1,000 records
and keeps only the fields needed for later routing policy: model ID and name,
context length, input/output modalities, supported parameters, per-token prompt
and completion prices, and expiration status.

The official schema currently exposes `expiration_date`, not a separate model
status. A returned model is therefore `available` unless it has an expiration
date, in which case Veto records `scheduled_for_removal`. The
[models guide](https://openrouter.ai/docs/guides/overview/models) defines the
retained `prompt` and `completion` prices as per-token decimal strings; other
pricing fields use different units. It also describes the supported-parameter
and architecture fields.

## Bounds and validation

- HTTPS is mandatory and cross-origin redirects are rejected.
- A request has a 10-second deadline and a 16 MiB response limit.
- Empty, duplicate, oversized, malformed, or partial catalogs are rejected.
- Unknown context, modality, capability, or price remains unknown; a verified
  zero price remains distinct.
- API keys are request-only and never enter the cache.

The current API response does not advertise an `ETag`, but Veto stores one and
uses `If-None-Match` when a compatible response provides it. A 304 response
refreshes the cache timestamp without replacing model data.

## Persistence and states

The versioned cache lives at
`~/.veto/cache/openrouter-models.json`. Its directory and file use `0700` and
`0600` permissions where supported. Writes use a same-directory temporary
file, file sync, atomic rename where available, and rollback-safe replacement
on platforms that cannot overwrite by rename. Symlinked managed cache paths
are refused.

Consumers receive independent state signals:

- `StateFresh` or `StateStale` describes cache age;
- `Offline` states whether network refresh was intentionally disabled.

A valid stale cache remains usable when refresh fails. An invalid response or
failed write never replaces a known-good cache. `veto doctor` validates cache
shape and schema without contacting OpenRouter.

Catalog discovery does not yet make every discovered model routable. The
current provider registry retains its explicit model list until shortlist and
selection policy are added.
