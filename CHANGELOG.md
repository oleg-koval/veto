# Changelog

Notable user-facing changes are documented here. Veto follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.17.1](https://github.com/oleg-koval/veto/compare/v0.17.0...v0.17.1) (2026-09-28)


### Bug Fixes

* require workspace-independent capture attestation ([#129](https://github.com/oleg-koval/veto/issues/129)) ([6f11c36](https://github.com/oleg-koval/veto/commit/6f11c36ebefe4500d0c157b5113c767e503ba7e4))

## [0.17.0](https://github.com/oleg-koval/veto/compare/v0.16.0...v0.17.0) (2026-09-28)


### Features

* add opt-in private paired capture ([#127](https://github.com/oleg-koval/veto/issues/127)) ([af7219a](https://github.com/oleg-koval/veto/commit/af7219ac4cd45f0f1d7a1e9fd4916f72840de92c))

## [0.16.0](https://github.com/oleg-koval/veto/compare/v0.15.0...v0.16.0) (2026-09-28)


### Features

* add private provenance validation for paired replay ([#125](https://github.com/oleg-koval/veto/issues/125)) ([88a1957](https://github.com/oleg-koval/veto/commit/88a19575a66ddc5076b2577b463ce7d26addc53f))

## [0.15.0](https://github.com/oleg-koval/veto/compare/v0.14.1...v0.15.0) (2026-09-28)


### Features

* add offline paired replay and execution preflight ([#123](https://github.com/oleg-koval/veto/issues/123)) ([fb80c16](https://github.com/oleg-koval/veto/commit/fb80c168fd3c6323303cd3a406319d65158d2ddb))

## [0.14.1](https://github.com/oleg-koval/veto/compare/v0.14.0...v0.14.1) (2026-09-27)


### Bug Fixes

* harden Jev shadow readiness and exec flag parsing ([#121](https://github.com/oleg-koval/veto/issues/121)) ([c16dec6](https://github.com/oleg-koval/veto/commit/c16dec6f262d5f4629ad74ab011b3758c7f8d5e7))

## [0.14.0](https://github.com/oleg-koval/veto/compare/v0.13.0...v0.14.0) (2026-09-26)


### Features

* add a versioned redacted event ledger ([#26](https://github.com/oleg-koval/veto/issues/26)) ([b0bd67b](https://github.com/oleg-koval/veto/commit/b0bd67bb129aa2c0be2d56a65069eb563725e24b))
* add bounded OpenRouter catalog cache ([#28](https://github.com/oleg-koval/veto/issues/28)) ([56c4c08](https://github.com/oleg-koval/veto/commit/56c4c08d6a24acc972e46643bd75066263578644))
* add consent controls for future analytics ([#75](https://github.com/oleg-koval/veto/issues/75)) ([0c60253](https://github.com/oleg-koval/veto/commit/0c60253a5c2e65b0c66c3d14dd568385f92903bc))
* add doctor and prepare v0.1.0 beta ([#8](https://github.com/oleg-koval/veto/issues/8)) ([c258300](https://github.com/oleg-koval/veto/commit/c258300b55a494210b00953b996eba52f47cabc4))
* add governed feedback reporting ([#33](https://github.com/oleg-koval/veto/issues/33)) ([a0ce7c5](https://github.com/oleg-koval/veto/commit/a0ce7c5bf5ef9898ca417761d718f24b884d6256))
* add multi-provider executors and CLI entry point ([7787db5](https://github.com/oleg-koval/veto/commit/7787db55e5232d5e521ce88a15405679f867cfe1))
* add native Hermes integration ([#49](https://github.com/oleg-koval/veto/issues/49)) ([473fdd9](https://github.com/oleg-koval/veto/commit/473fdd939d8499b0df59c427b06be935d4e71016)), closes [#47](https://github.com/oleg-koval/veto/issues/47)
* add OpenRouter browser login with PKCE ([#32](https://github.com/oleg-koval/veto/issues/32)) ([9c569ec](https://github.com/oleg-koval/veto/commit/9c569ec737abc89aac99582f458a29a44a79b139))
* add routing cockpit TUI and harden runtime controls ([#91](https://github.com/oleg-koval/veto/issues/91)) ([c5b9f68](https://github.com/oleg-koval/veto/commit/c5b9f68253df159f308556ee09ecc21437e2bd59))
* add verified run receipts ([#115](https://github.com/oleg-koval/veto/issues/115)) ([ed668b4](https://github.com/oleg-koval/veto/commit/ed668b41d54a9cfbb091d15c34b6c72e138aff50))
* add veto login for interactive credential storage ([c016e57](https://github.com/oleg-koval/veto/commit/c016e5792b98a5605409832bce96a6d9b2897b54))
* add xAI Grok support (grok-4.5, grok-4.3 + grok-3 family) ([b0087e7](https://github.com/oleg-koval/veto/commit/b0087e73e09a3a4377a02f52a8465ad1d2e3e425))
* add xAI Grok support (grok-4.5, grok-4.3, grok-3, grok-3-mini) ([26a3d5c](https://github.com/oleg-koval/veto/commit/26a3d5c454eeec50e74618aa185ed2c607d19585))
* animated routing pipeline, checkpoint resume, and log rotation ([f0d862c](https://github.com/oleg-koval/veto/commit/f0d862c1ce1d2f0a8dc132c57692ffdfec8240ab))
* automate releases and Homebrew publishing ([#7](https://github.com/oleg-koval/veto/issues/7)) ([813a5da](https://github.com/oleg-koval/veto/commit/813a5dab9320365bf0c9802905f8e6929523e264))
* automate releases and interactive updates ([#14](https://github.com/oleg-koval/veto/issues/14)) ([c403125](https://github.com/oleg-koval/veto/commit/c403125445b4d8e8110373eb11dceb33752829d2))
* **cmd:** one-arg routing, savings line, git hook, and live web dashboard ([0623f65](https://github.com/oleg-koval/veto/commit/0623f65d963f54f23a46af0b3dddbcda442542ce))
* define task role contract ([#110](https://github.com/oleg-koval/veto/issues/110)) ([b700c3c](https://github.com/oleg-koval/veto/commit/b700c3ca6ac77d8ddc0431387485ce6a8d02c3f8))
* discover OpenCode runtimes ([#36](https://github.com/oleg-koval/veto/issues/36)) ([0426dea](https://github.com/oleg-koval/veto/commit/0426dea7a5ca6c438bbeaf400e9773d3981537a2))
* enable automatic Hermes turn routing ([169887a](https://github.com/oleg-koval/veto/commit/169887addcb7cda076f1aecc7713d9a2f72386df))
* execute tasks through OpenCode sessions ([#39](https://github.com/oleg-koval/veto/issues/39)) ([0776aba](https://github.com/oleg-koval/veto/commit/0776aba4c8e7d59477bb86a81e66131a27a05c40))
* **executor,login:** add subscription mode — route via claude -p at $0 ([fde0c92](https://github.com/oleg-koval/veto/commit/fde0c928f09889dd0540937168b138a18df0e8a1))
* **executor:** auto-start ollama serve on connection refused ([2701ca7](https://github.com/oleg-koval/veto/commit/2701ca76f9331d3483d77873cb4219e3056c853b))
* explain routing with an animated flow ([#29](https://github.com/oleg-koval/veto/issues/29)) ([44f0bee](https://github.com/oleg-koval/veto/commit/44f0bee66d697548e5f625274ea378dbb7173bc3))
* GA fixes — skill discovery, exec wiring, streaming capture, CI, version ([56ed127](https://github.com/oleg-koval/veto/commit/56ed1274eec753410fbdf79e828a77d3e5ce66b5))
* harden veto for user-ready harness ([#3](https://github.com/oleg-koval/veto/issues/3)) ([1695a22](https://github.com/oleg-koval/veto/commit/1695a22a3184435d9abaa24c7bbce023fbd7b9a1))
* initial commit — model router with self-admitting receivers ([e1750e7](https://github.com/oleg-koval/veto/commit/e1750e770b721210eb86cd8460e8529398ad721c))
* integrate Veto routing with OpenCode ([#42](https://github.com/oleg-koval/veto/issues/42)) ([6b17723](https://github.com/oleg-koval/veto/commit/6b17723eb9377b20dec6edfb74580cfcda1ed634))
* local models, veto logout, plan/exec commands, sequential routing ([f71a0af](https://github.com/oleg-koval/veto/commit/f71a0af982ca2b097689baebfce9bddfaf5ab575))
* **login:** allow adding multiple local models in one session ([db1ab69](https://github.com/oleg-koval/veto/commit/db1ab69d27ef18b74444841d5ad4c0ba9c0447bb))
* **login:** guided local model install — Ollama auto-detect, pull, start ([47c5451](https://github.com/oleg-koval/veto/commit/47c5451b99da5f3d0b8a52646adb7d943a65cd3e))
* publish Arch and Debian Linux packages ([#79](https://github.com/oleg-koval/veto/issues/79)) ([b256814](https://github.com/oleg-koval/veto/commit/b2568142c1a993e8981244579489fce67d334fa2))
* redesign veto login with browser-open and masked key input ([41553ec](https://github.com/oleg-koval/veto/commit/41553ec1209ad224813911b17fa198c0f0d3dd61))
* retry transient API errors and stabilize routing spinner ([a848893](https://github.com/oleg-koval/veto/commit/a8488939775822a0a34212f45c0b228d6dee7144))
* **route:** --json output for scripting / agent infra ([7e9607f](https://github.com/oleg-koval/veto/commit/7e9607fdfe0c3f1a45bf80f33d4a0ee26ac095dc))
* **route:** add JSON output mode ([5801722](https://github.com/oleg-koval/veto/commit/580172289335f2e99c679b42f6766de520f39ce3))
* **router:** provider-aware catalog with persistent history feeding ranking ([5f64245](https://github.com/oleg-koval/veto/commit/5f64245529de8809c5bab6f624c863c92a6c499c))
* **routing:** cost-first scoring + complexity-based tier enforcement ([12ad4ac](https://github.com/oleg-koval/veto/commit/12ad4ac89aefb26dbf0aab959ea7f9eada7a215f))
* **run:** add veto run — route and execute in one step ([4bd54ad](https://github.com/oleg-koval/veto/commit/4bd54ad285e93c4270c7035f656b135d2f020d6a))
* **run:** extract and save output file when objective specifies filename ([3f5a0e3](https://github.com/oleg-koval/veto/commit/3f5a0e3de60990fb1dcf6b820f6742213f447a17))
* shortlist the dynamic OpenRouter catalog ([#31](https://github.com/oleg-koval/veto/issues/31)) ([1f4d103](https://github.com/oleg-koval/veto/commit/1f4d1036b3d1798679d1681943ab57e7db1118e8))
* **site:** local commands and changelog pages, GA4/GSC wiring, mobile fixes ([#85](https://github.com/oleg-koval/veto/issues/85)) ([25ebd1b](https://github.com/oleg-koval/veto/commit/25ebd1b9a10069582d3d35505a0052a6de440206))
* skill recommendation + final QA/review integrator ([cd06774](https://github.com/oleg-koval/veto/commit/cd06774bfea85f016cae015240be47e7e3666e51))
* update model roster + per-model disable/enable ([6d5ebfb](https://github.com/oleg-koval/veto/commit/6d5ebfb8a00a0bb1518e9367988f3e241f670676))


### Bug Fixes

* address clean architecture review findings ([#83](https://github.com/oleg-koval/veto/issues/83)) ([ebae6b8](https://github.com/oleg-koval/veto/commit/ebae6b86b68874cc7cc0cdd0402e4cb3fd5ae625))
* **admission:** surface real error instead of generic "parse failure" ([e2635fc](https://github.com/oleg-koval/veto/commit/e2635fc4623da17f7d1a9aa71552091a879e7084))
* **admission:** use json.Decoder to handle trailing prose from open models ([7d08ebd](https://github.com/oleg-koval/veto/commit/7d08ebd2d670fbd8a8bf69c49ee5ee0d3bdd9949))
* align provider status tables ([#77](https://github.com/oleg-koval/veto/issues/77)) ([775d7df](https://github.com/oleg-koval/veto/commit/775d7df49d7ddf954b48d5efb2f854413df861c9))
* allow Release Please through contributor governance ([#35](https://github.com/oleg-koval/veto/issues/35)) ([f1c2f62](https://github.com/oleg-koval/veto/commit/f1c2f626d1d179473975a563fb9aa650cfab3376))
* **ci:** avoid duplicate release dispatch ([#71](https://github.com/oleg-koval/veto/issues/71)) ([59ab2f3](https://github.com/oleg-koval/veto/commit/59ab2f3397e36a8904e9364df20fe28ff94ada66))
* **ci:** handle Release Please PR references ([#64](https://github.com/oleg-koval/veto/issues/64)) ([c60c9ee](https://github.com/oleg-koval/veto/commit/c60c9ee8b6446c3d3c8962ad89933fd657148f5b)), closes [#63](https://github.com/oleg-koval/veto/issues/63)
* **ci:** honor contributor whitelist ([#68](https://github.com/oleg-koval/veto/issues/68)) ([5c5b561](https://github.com/oleg-koval/veto/commit/5c5b5613d2c00d29229a7fb741a36fa671c3f1a0))
* close remaining PR [#90](https://github.com/oleg-koval/veto/issues/90) review gaps ([#94](https://github.com/oleg-koval/veto/issues/94)) ([76af6ed](https://github.com/oleg-koval/veto/commit/76af6edadae5bccd37d9d6ef3c4097d737da8fa5))
* do not block releases on optional Homebrew token ([#9](https://github.com/oleg-koval/veto/issues/9)) ([9f0eceb](https://github.com/oleg-koval/veto/commit/9f0ecebff6f9bba9d28b4a1b9bcebf166577eff6))
* polish animated routing flow ([#43](https://github.com/oleg-koval/veto/issues/43)) ([6f7ed39](https://github.com/oleg-koval/veto/commit/6f7ed394f5f4cef8f68b981d831a7af135074f5f))
* polish responsive project site ([#24](https://github.com/oleg-koval/veto/issues/24)) ([39e45da](https://github.com/oleg-koval/veto/commit/39e45daaa726159c0e3cb41a76a558180275ed2f))
* polish Veto mission control TUI ([#103](https://github.com/oleg-koval/veto/issues/103)) ([9022997](https://github.com/oleg-koval/veto/commit/90229977cbec5bf6aa88b86d1e68b85e5fc2f1e4))
* preserve event ledger correlation semantics ([#27](https://github.com/oleg-koval/veto/issues/27)) ([b660225](https://github.com/oleg-koval/veto/commit/b660225743868859c9e51db621681786d377c91f))
* publish Homebrew formula from releases ([#12](https://github.com/oleg-koval/veto/issues/12)) ([26b9098](https://github.com/oleg-koval/veto/commit/26b9098055c24036d26d332a4e92b0489f618e02))
* publish releases only after artifact upload ([#102](https://github.com/oleg-koval/veto/issues/102)) ([8b74ca8](https://github.com/oleg-koval/veto/commit/8b74ca8f1a8e57892582586ae23353d71b0f880e))
* reopen rewritten ledger before append ([#104](https://github.com/oleg-koval/veto/issues/104)) ([e97090d](https://github.com/oleg-koval/veto/commit/e97090dec1e38cb117838f6d9904d036159a961a))
* report the routable OpenRouter catalog honestly ([#22](https://github.com/oleg-koval/veto/issues/22)) ([fb439a1](https://github.com/oleg-koval/veto/commit/fb439a1ac8ef7cb95cb8d28ce64e51356178128f))
* restore real-provider routing ([#13](https://github.com/oleg-koval/veto/issues/13)) ([649edf6](https://github.com/oleg-koval/veto/commit/649edf63d306a97bce1677d7c0397d7db8be9412))
* **routing:** eliminate silent hang before routing starts ([26f584d](https://github.com/oleg-koval/veto/commit/26f584dbbb908806b09b289244f1130ee00244f0))
* **routing:** honest executor capability + text-only execution prompt ([52b8131](https://github.com/oleg-koval/veto/commit/52b813183d47c73a1f07f5e487057ec5696096f4))
* stabilize TUI execution smoke assertion ([#97](https://github.com/oleg-koval/veto/issues/97)) ([5ea7830](https://github.com/oleg-koval/veto/commit/5ea7830b0714671c9ff1e533a5140c1aa17086ac))
* surface history deletion failures ([#105](https://github.com/oleg-koval/veto/issues/105)) ([72b8bc8](https://github.com/oleg-koval/veto/commit/72b8bc8f9e920f5995a5008f55c84fe3f4c21dac))
* trust repository GitHub Actions PRs ([#46](https://github.com/oleg-koval/veto/issues/46)) ([618b6f1](https://github.com/oleg-koval/veto/commit/618b6f13d5efb6f2a3e46ee3904776737d19ce25))
* upload only generated release assets ([#101](https://github.com/oleg-koval/veto/issues/101)) ([4ce7a0e](https://github.com/oleg-koval/veto/commit/4ce7a0e04d203a95642a05e032bfa856bc297ff2))
* upload release assets to existing release ([#99](https://github.com/oleg-koval/veto/issues/99)) ([1efd365](https://github.com/oleg-koval/veto/commit/1efd365f98135bd5e1b82497564811cfa102587b))
* use a publisher-safe binary manifest path ([#10](https://github.com/oleg-koval/veto/issues/10)) ([aa0df63](https://github.com/oleg-koval/veto/commit/aa0df631f2a730864d3f701731311c7b64376935))
* use durable session identity for Hermes routing controls ([#60](https://github.com/oleg-koval/veto/issues/60)) ([f1df7e8](https://github.com/oleg-koval/veto/commit/f1df7e8d055160e91a08b28816b98fb78f86786f))


### Performance Improvements

* **store:** make Signal() O(1) with incremental stats, add benchmarks ([8d402ff](https://github.com/oleg-koval/veto/commit/8d402ff2954e83ac65b63b0a1705636c6363bb0d))

## [0.13.0](https://github.com/oleg-koval/veto/compare/v0.12.0...v0.13.0) (2026-09-26)


### Features

* introduce provider-neutral decision engine boundary ([#117](https://github.com/oleg-koval/veto/issues/117)) ([bb40a7f](https://github.com/oleg-koval/veto/commit/bb40a7f7dc72f04c18403789960ed263a87facac))
* add opt-in Jev shadow evaluation ([#118](https://github.com/oleg-koval/veto/issues/118)) ([6cb19a4](https://github.com/oleg-koval/veto/commit/6cb19a41efd78343727258eb8af54b78aafa49ce))

## [0.12.0](https://github.com/oleg-koval/veto/compare/v0.11.0...v0.12.0) (2026-09-13)


### Features

* add verified run receipts ([#115](https://github.com/oleg-koval/veto/issues/115)) ([ed668b4](https://github.com/oleg-koval/veto/commit/ed668b41d54a9cfbb091d15c34b6c72e138aff50))

## [0.11.0](https://github.com/oleg-koval/veto/compare/v0.10.4...v0.11.0) (2026-09-10)


### Features

* define task role contract ([#110](https://github.com/oleg-koval/veto/issues/110)) ([b700c3c](https://github.com/oleg-koval/veto/commit/b700c3ca6ac77d8ddc0431387485ce6a8d02c3f8))

## [0.10.4](https://github.com/oleg-koval/veto/compare/v0.10.3...v0.10.4) (2026-09-09)


### Bug Fixes

* polish Veto mission control TUI ([#103](https://github.com/oleg-koval/veto/issues/103)) ([9022997](https://github.com/oleg-koval/veto/commit/90229977cbec5bf6aa88b86d1e68b85e5fc2f1e4))
* reopen rewritten ledger before append ([#104](https://github.com/oleg-koval/veto/issues/104)) ([e97090d](https://github.com/oleg-koval/veto/commit/e97090dec1e38cb117838f6d9904d036159a961a))
* surface history deletion failures ([#105](https://github.com/oleg-koval/veto/issues/105)) ([72b8bc8](https://github.com/oleg-koval/veto/commit/72b8bc8f9e920f5995a5008f55c84fe3f4c21dac))

## [0.10.3](https://github.com/oleg-koval/veto/compare/v0.10.2...v0.10.3) (2026-09-08)


### Bug Fixes

* publish releases only after artifact upload ([#102](https://github.com/oleg-koval/veto/issues/102)) ([8b74ca8](https://github.com/oleg-koval/veto/commit/8b74ca8f1a8e57892582586ae23353d71b0f880e))
* upload only generated release assets ([#101](https://github.com/oleg-koval/veto/issues/101)) ([4ce7a0e](https://github.com/oleg-koval/veto/commit/4ce7a0e04d203a95642a05e032bfa856bc297ff2))
* upload release assets to existing release ([#99](https://github.com/oleg-koval/veto/issues/99)) ([1efd365](https://github.com/oleg-koval/veto/commit/1efd365f98135bd5e1b82497564811cfa102587b))

## [0.10.2](https://github.com/oleg-koval/veto/compare/v0.10.1...v0.10.2) (2026-09-08)


### Bug Fixes

* stabilize TUI execution smoke assertion ([#97](https://github.com/oleg-koval/veto/issues/97)) ([5ea7830](https://github.com/oleg-koval/veto/commit/5ea7830b0714671c9ff1e533a5140c1aa17086ac))
* default non-interactive TUI plan execution to abort on failure
* keep the execution budget out of review routing so locally configured models remain eligible

## [0.10.1](https://github.com/oleg-koval/veto/compare/v0.10.0...v0.10.1) (2026-09-06)


### Bug Fixes

* close remaining PR [#90](https://github.com/oleg-koval/veto/issues/90) review gaps ([#94](https://github.com/oleg-koval/veto/issues/94)) ([76af6ed](https://github.com/oleg-koval/veto/commit/76af6edadae5bccd37d9d6ef3c4097d737da8fa5))

## [0.10.0](https://github.com/oleg-koval/veto/compare/v0.9.0...v0.10.0) (2026-09-06)


### Features

* add routing cockpit TUI and harden runtime controls ([#91](https://github.com/oleg-koval/veto/issues/91)) ([c5b9f68](https://github.com/oleg-koval/veto/commit/c5b9f68253df159f308556ee09ecc21437e2bd59))

## [0.9.0](https://github.com/oleg-koval/veto/compare/v0.8.1...v0.9.0) (2026-09-01)


### Features

* **site:** local commands and changelog pages, GA4/GSC wiring, mobile fixes ([#85](https://github.com/oleg-koval/veto/issues/85)) ([25ebd1b](https://github.com/oleg-koval/veto/commit/25ebd1b9a10069582d3d35505a0052a6de440206))

## [0.8.1](https://github.com/oleg-koval/veto/compare/v0.8.0...v0.8.1) (2026-09-01)


### Bug Fixes

* address clean architecture review findings ([#83](https://github.com/oleg-koval/veto/issues/83)) ([ebae6b8](https://github.com/oleg-koval/veto/commit/ebae6b86b68874cc7cc0cdd0402e4cb3fd5ae625))

## [0.8.0](https://github.com/oleg-koval/veto/compare/v0.7.0...v0.8.0) (2026-08-31)


### Features

* publish Arch and Debian Linux packages ([#79](https://github.com/oleg-koval/veto/issues/79)) ([b256814](https://github.com/oleg-koval/veto/commit/b2568142c1a993e8981244579489fce67d334fa2))

## [0.7.0](https://github.com/oleg-koval/veto/compare/v0.6.3...v0.7.0) (2026-08-31)


### Features

* add consent controls for future analytics ([#75](https://github.com/oleg-koval/veto/issues/75)) ([0c60253](https://github.com/oleg-koval/veto/commit/0c60253a5c2e65b0c66c3d14dd568385f92903bc))


### Bug Fixes

* align provider status tables ([#77](https://github.com/oleg-koval/veto/issues/77)) ([775d7df](https://github.com/oleg-koval/veto/commit/775d7df49d7ddf954b48d5efb2f854413df861c9))

## [0.6.3](https://github.com/oleg-koval/veto/compare/v0.6.2...v0.6.3) (2026-08-31)


### Bug Fixes

* **ci:** avoid duplicate release dispatch ([#71](https://github.com/oleg-koval/veto/issues/71)) ([59ab2f3](https://github.com/oleg-koval/veto/commit/59ab2f3397e36a8904e9364df20fe28ff94ada66))

## [0.6.2](https://github.com/oleg-koval/veto/compare/v0.6.1...v0.6.2) (2026-08-31)


### Bug Fixes

* **ci:** honor contributor whitelist ([#68](https://github.com/oleg-koval/veto/issues/68)) ([5c5b561](https://github.com/oleg-koval/veto/commit/5c5b5613d2c00d29229a7fb741a36fa671c3f1a0))

## [0.6.1](https://github.com/oleg-koval/veto/compare/v0.6.0...v0.6.1) (2026-08-30)


### Bug Fixes

* **ci:** handle Release Please PR references ([#64](https://github.com/oleg-koval/veto/issues/64)) ([c60c9ee](https://github.com/oleg-koval/veto/commit/c60c9ee8b6446c3d3c8962ad89933fd657148f5b)), closes [#63](https://github.com/oleg-koval/veto/issues/63)
* use durable session identity for Hermes routing controls ([#60](https://github.com/oleg-koval/veto/issues/60)) ([f1df7e8](https://github.com/oleg-koval/veto/commit/f1df7e8d055160e91a08b28816b98fb78f86786f))

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
