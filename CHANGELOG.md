# Changelog

Notable user-facing changes are documented here. Veto follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-08-29

### Added

- Cost-aware routing across Anthropic, OpenAI, OpenRouter, xAI, and local
  OpenAI-compatible models.
- Structured model admission with capability, context, complexity, confidence,
  and estimated-cost gates.
- `route`, `run`, and multi-step `exec` workflows with JSON output, checkpoints,
  explicit output files, and optional acceptance-criteria review.
- Interactive provider onboarding, account-level model catalog verification,
  persisted routing history, and per-model enable/disable controls.
- Deterministic offline routing benchmarks and a discoverable agent routing
  skill.
- Versioned binaries for macOS, Linux, and Windows, checksum verification,
  standard Go module installation, and Homebrew tap publication.

[0.1.0]: https://github.com/oleg-koval/veto/releases/tag/v0.1.0
