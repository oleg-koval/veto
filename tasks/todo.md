# Veto User-Ready Harness Tasks

## Task 1: Separate admission and execution contracts

**Acceptance criteria:** admission remains 512 tokens; execution accepts an explicit output limit; result reports usage and truncation without conflating unknown with zero.

**Verification:** focused executor and admission tests fail first, then pass.

**Dependencies:** None.

## Task 2: Implement provider execution paths

**Acceptance criteria:** Anthropic, OpenAI-compatible, and CLI transports support full execution; errors and length termination remain actionable.

**Verification:** fake-provider request/response tests, including output above 512 tokens.

**Dependencies:** Task 1.

## Task 3: Integrate execution into CLI

**Acceptance criteria:** `run`, `exec`, review, conversion, and skill paths use execution rather than admission; output limit is configurable and bounded.

**Verification:** CLI fakes distinguish admission from execution calls; quiet/stdout contract remains stable.

**Dependencies:** Tasks 1-2.

## Task 4: Make capabilities transport-derived

**Acceptance criteria:** HTTP/local executors advertise no executable tools; CLI executors retain their real tools; hard filtering happens before admission.

**Verification:** provider-registry and filter tests.

**Dependencies:** Task 1.

## Task 5: Add kind-aware history metrics

**Acceptance criteria:** history persists kind, status, latency, usage, cost, and optional score with known flags; old files still load.

**Verification:** round-trip, legacy, corrupt-file, and race tests.

**Dependencies:** Task 1.

## Task 6: Activate adaptive ranking and result recording

**Acceptance criteria:** manager ranks with store signals; every execution records exactly one result; kinds remain isolated.

**Verification:** fresh-manager persistence integration test and execution-path tests.

**Dependencies:** Task 5.

## Task 7: Report cost honestly

**Acceptance criteria:** provider usage produces known actual cost; unknown stays unknown; UI/docs call preflight ceilings estimated; over-estimate is visible.

**Verification:** provider usage and boundary tests.

**Dependencies:** Tasks 2, 5-6.

## Task 8: Fix root help

**Acceptance criteria:** `--help`, `-h`, and `help` exit zero without side effects; unknown commands still fail.

**Verification:** subprocess CLI smoke tests.

**Dependencies:** None.

## Task 9: Make output files explicit and safe

**Acceptance criteria:** objective text never writes; `--output` is explicit; existing files require `--force`; failures are non-zero.

**Verification:** traversal, absolute, sensitive, overwrite, permission, and fence tests.

**Dependencies:** Task 3.

## Task 10: Fail requested reviews closed

**Acceptance criteria:** unavailable, malformed, incomplete, or inconsistent review results fail; plan failure policy remains respected.

**Verification:** review and plan integration tests.

**Dependencies:** Tasks 3, 6.

## Task 11: Remove remote-shell installation

**Acceptance criteria:** Linux onboarding prints official manual instructions and never executes a remote shell pipeline.

**Verification:** installer command-selection tests.

**Dependencies:** None.

## Task 12: Add offline routing evaluation

**Acceptance criteria:** deterministic corpus compares four policies and emits JSON metrics without credentials or network.

**Verification:** schema/invariant tests and CI smoke command.

**Dependencies:** Tasks 6-7.

## Task 13: Add fresh-home onboarding smoke tests

**Acceptance criteria:** temporary home/PATH and fake providers validate critical CLI flows, exits, stdout/stderr, permissions, and no secret leakage.

**Verification:** macOS/Linux CI job.

**Dependencies:** Tasks 8-11.

## Task 14: Centralize model metadata

**Acceptance criteria:** routing, executors, provider display, and documentation derive from or are tested against one catalog.

**Verification:** catalog consistency and documentation drift tests.

**Dependencies:** Task 4.

## Task 15: Harden beta artifacts

**Acceptance criteria:** release gates run before publish; archives include normalized versions and SHA-256 sums for supported platforms including Linux arm64.

**Verification:** local artifact dry run and workflow validation; no tag is pushed.

**Dependencies:** Tasks 12-14.

## Task 16: Update user and architecture documentation

**Acceptance criteria:** docs accurately describe text-only transports, estimated costs, output limits, safe file writes, evaluation limits, install, upgrade, and uninstall.

**Verification:** command examples checked against built CLI and catalog drift tests.

**Dependencies:** Tasks 1-15.

## Task 17: Prepare external onboarding trial protocol

**Acceptance criteria:** repeatable checklist captures setup time, failures, successful first route/run, and user feedback without collecting credentials.

**Verification:** protocol reviewed locally; actual external trials explicitly remain pending.

**Dependencies:** Tasks 13, 16.

## Task 19: Filter text-only transports for explicit repository mutations

**Acceptance criteria:** the reported PR-fix-and-push objective requires an executable runtime; API and local text-only transports are filtered before admission; ordinary content-only code generation remains eligible.

**Verification:** focused inference and router filter tests fail first, then pass.

**Dependencies:** Task 4.

## Task 20: Harden Claude subscription admission

**Acceptance criteria:** admission uses Claude safe mode, disabled tools, no session persistence, and native schema output; execution retains normal project tools and permission policy; admission has its own configurable deadline.

**Verification:** CLI argument and JSON-envelope tests fail first, then pass; a real subscription route returns a valid decision.

**Dependencies:** Task 19.

## Task 21: Verify the exact agentic run flow

**Acceptance criteria:** `veto run` has enough default wall time for repository work; a fake Claude CLI proves admission then execution for the exact objective; the fresh binary runs the exact command from the Roazon PR branch and reaches a truthful terminal result.

**Verification:** focused subprocess test, race suite, vet, build, onboarding smoke, benchmark, and live PR-state audit.

**Dependencies:** Tasks 19-20.
