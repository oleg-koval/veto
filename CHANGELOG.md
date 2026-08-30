# Changelog

Notable user-facing changes are documented here. Veto follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.6.0](https://github.com/oleg-koval/veto/compare/v0.5.0...v0.6.0) (2026-08-30)


### Features

* enable automatic Hermes turn routing ([169887a](https://github.com/oleg-koval/veto/commit/169887addcb7cda076f1aecc7713d9a2f72386df))

## [0.5.0](https://github.com/oleg-koval/veto/compare/v0.4.1...v0.5.0) (2026-08-30)


### Features

* add native Hermes integration ([#49](https://github.com/oleg-koval/veto/issues/49)) ([473fdd9](https://github.com/oleg-koval/veto/commit/473fdd939d8499b0df59c427b06be935d4e71016)), closes [#47](https://github.com/oleg-koval/veto/issues/47)

## [0.4.1](https://github.com/oleg-koval/veto/compare/v0.4.0...v0.4.1) (2026-08-30)


### Bug Fixes

* trust repository GitHub Actions PRs ([#46](https://github.com/oleg-koval/veto/issues/46)) ([618b6f1](https://github.com/oleg-koval/veto/commit/618b6f13d5efb6f2a3e46ee3904776737d19ce25))

## [0.4.0](https://github.com/oleg-koval/veto/compare/v0.3.0...v0.4.0) (2026-08-30)


### Features

* execute tasks through OpenCode sessions ([#39](https://github.com/oleg-koval/veto/issues/39)) ([0776aba](https://github.com/oleg-koval/veto/commit/0776aba4c8e7d59477bb86a81e66131a27a05c40))
* integrate Veto routing with OpenCode ([#42](https://github.com/oleg-koval/veto/issues/42)) ([6b17723](https://github.com/oleg-koval/veto/commit/6b17723eb9377b20dec6edfb74580cfcda1ed634))


### Bug Fixes

* polish animated routing flow ([#43](https://github.com/oleg-koval/veto/issues/43)) ([6f7ed39](https://github.com/oleg-koval/veto/commit/6f7ed394f5f4cef8f68b981d831a7af135074f5f))

## [0.3.0](https://github.com/oleg-koval/veto/compare/v0.2.0...v0.3.0) (2026-08-30)


### Features

* add a versioned redacted event ledger ([#26](https://github.com/oleg-koval/veto/issues/26)) ([b0bd67b](https://github.com/oleg-koval/veto/commit/b0bd67bb129aa2c0be2d56a65069eb563725e24b))
* add bounded OpenRouter catalog cache ([#28](https://github.com/oleg-koval/veto/issues/28)) ([56c4c08](https://github.com/oleg-koval/veto/commit/56c4c08d6a24acc972e46643bd75066263578644))
* add governed feedback reporting ([#33](https://github.com/oleg-koval/veto/issues/33)) ([a0ce7c5](https://github.com/oleg-koval/veto/commit/a0ce7c5bf5ef9898ca417761d718f24b884d6256))
* add OpenRouter browser login with PKCE ([#32](https://github.com/oleg-koval/veto/issues/32)) ([9c569ec](https://github.com/oleg-koval/veto/commit/9c569ec737abc89aac99582f458a29a44a79b139))
* discover OpenCode runtimes ([#36](https://github.com/oleg-koval/veto/issues/36)) ([0426dea](https://github.com/oleg-koval/veto/commit/0426dea7a5ca6c438bbeaf400e9773d3981537a2))
* explain routing with an animated flow ([#29](https://github.com/oleg-koval/veto/issues/29)) ([44f0bee](https://github.com/oleg-koval/veto/commit/44f0bee66d697548e5f625274ea378dbb7173bc3))
* shortlist the dynamic OpenRouter catalog ([#31](https://github.com/oleg-koval/veto/issues/31)) ([1f4d103](https://github.com/oleg-koval/veto/commit/1f4d1036b3d1798679d1681943ab57e7db1118e8))


### Bug Fixes

* allow Release Please through contributor governance ([#35](https://github.com/oleg-koval/veto/issues/35)) ([f1c2f62](https://github.com/oleg-koval/veto/commit/f1c2f626d1d179473975a563fb9aa650cfab3376))
* polish responsive project site ([#24](https://github.com/oleg-koval/veto/issues/24)) ([39e45da](https://github.com/oleg-koval/veto/commit/39e45daaa726159c0e3cb41a76a558180275ed2f))
* preserve event ledger correlation semantics ([#27](https://github.com/oleg-koval/veto/issues/27)) ([b660225](https://github.com/oleg-koval/veto/commit/b660225743868859c9e51db621681786d377c91f))
* report the routable OpenRouter catalog honestly ([#22](https://github.com/oleg-koval/veto/issues/22)) ([fb439a1](https://github.com/oleg-koval/veto/commit/fb439a1ac8ef7cb95cb8d28ce64e51356178128f))

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
