# Changelog

Notable user-facing changes are documented here. Veto follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0](https://github.com/oleg-koval/veto/compare/v0.1.0...v0.2.0) (2026-08-29)


### Features

* automate releases and interactive updates ([#14](https://github.com/oleg-koval/veto/issues/14)) ([c403125](https://github.com/oleg-koval/veto/commit/c403125445b4d8e8110373eb11dceb33752829d2))


### Bug Fixes

* publish Homebrew formula from releases ([#12](https://github.com/oleg-koval/veto/issues/12)) ([26b9098](https://github.com/oleg-koval/veto/commit/26b9098055c24036d26d332a4e92b0489f618e02))
* restore real-provider routing ([#13](https://github.com/oleg-koval/veto/issues/13)) ([649edf6](https://github.com/oleg-koval/veto/commit/649edf63d306a97bce1677d7c0397d7db8be9412))

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
