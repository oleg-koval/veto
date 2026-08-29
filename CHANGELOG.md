# Changelog

Notable user-facing changes are documented here. Veto follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Homebrew now uses an installable checksum-pinned formula, with deterministic
  formula generation available to the tag release workflow.
- GPT-5.6 routing now uses the OpenAI Responses API, honors the documented
  per-model admission timeout, falls back past transport errors, and exposes
  normalized provider failures in JSON and structured logs.
- Buffered execution now fails when a provider reports output truncation,
  preventing partial results from being saved or accepted as successful.

## [0.1.0] - 2026-08-29

### Added

- Multi-provider filtering, adaptive ranking, structured admission, route-only
  JSON output, bounded task execution, multi-step plans, and fail-closed
  acceptance review.
- Anthropic, OpenAI, OpenRouter, xAI, Claude subscription CLI, and local
  OpenAI-compatible model transports with transport-derived tool capabilities.
- `veto doctor` for side-effect-free installation and local-state diagnostics,
  with explicit safe repair through `--fix`.
- Six-platform release archives plus archive and extracted-binary SHA-256
  manifests.
- Versioned Go installation and optional Homebrew tap publication support.

### Security

- Explicit traversal-safe output files, approved skill-source boundaries,
  restrictive local-state permissions, bounded release downloads, hostile
  archive rejection, and rollback-protected official-binary replacement.

### Known limitations

- This is a beta. Provider availability, model IDs and pricing, confidence
  calibration, routing quality, and savings require account-specific and human
  validation. Checksums are not signatures.

[Unreleased]: https://github.com/oleg-koval/veto/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/oleg-koval/veto/releases/tag/v0.1.0
