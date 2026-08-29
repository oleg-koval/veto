# Changelog

Notable user-facing changes are documented here. Veto follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.1.0]: https://github.com/oleg-koval/veto/releases/tag/v0.1.0
