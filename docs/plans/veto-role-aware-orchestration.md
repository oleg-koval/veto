# Veto Role-Aware Orchestration Implementation Plan

**Status:** Approved
**Approved:** 2026-09-10
**Planning baseline:** Veto v0.10.4 (`151ace4692bdcb898f92dfc47a883bc81e28ccc3`)
**Reference reviewed:** `donvito/codex-astra-luna-orchestrator` at `f1de1b8729c8ccfb7978321b4764c58bd34c8493`

## Objective

Add the useful orchestration concepts from the reference repository without
turning Veto into a general autonomous scheduler. The first release slice adds:

1. an optional, generic task role;
2. deterministic root-only versus delegated execution advice; and
3. local usage reporting grouped by session and role.

Later work remains gated on evidence from real use.

## Product Boundary

Veto remains a bounded router and executor. It may describe an execution shape,
but it does not own an autonomous agent loop, edit global Codex configuration,
infer subscription quotas, or silently create parallel writers.

### Adopt

- Generic task roles instead of model-branded roles.
- Deterministic advice about root-only versus delegated execution.
- Session, parent-task, and role attribution in the local ledger.
- Reviewer diversity policies that can prefer or require provider separation.
- Explicit capacity profiles after demand is demonstrated.
- Dependency-aware parallel execution only after sequential semantics are sound.

### Do not copy

- Hard-coded Astra/Luna model assignments.
- Mandatory delegation for ordinary tasks.
- Global installers that overwrite user configuration or `AGENTS.md`.
- Autonomous continuation loops.
- Subscription or quota inference from incomplete local signals.
- Parallel execution before dependency and write-conflict safety exists.

## Architecture Decisions

- Add fields rather than change existing meanings. An omitted role preserves the
  current routing, admission, execution, JSON, and plan behavior.
- Keep role descriptive in the first slice. It is sent to admission and recorded
  in evidence, but it does not affect ranking until measured results justify a
  policy.
- Keep orchestration advice pure and deterministic: no provider call, history
  lookup, or hidden filesystem mutation.
- Treat advice as a recommendation, not authorization. File writes, external
  skills, execution, and parallelism retain their existing explicit boundaries.
- Preserve known, unknown, and known-zero token and cost values in all new
  aggregation paths.
- Use generic role and policy names. Provider/model selection continues through
  Veto's existing catalog and dispatch mechanisms.
- Keep all later phases behind explicit evidence gates; do not use completion of
  local tests as proof of product demand.

## Capability and Dependency Graph

```text
optional task role
    |-- CLI, plan, control-plane, and admission propagation
    |-- ledger attribution --> usage reporting --> demand evidence
    |-- deterministic adviser --> optional Codex integration pack
    `-- reviewer role --> reviewer diversity policy

demand evidence
    |-- capacity profiles
    `-- dependency-aware parallel execution
```

## Execution Preconditions

- Re-read `README.md` and `docs/architecture.md` from the implementation base.
- Re-verify the current `origin/main` SHA; do not assume the planning baseline is
  still current.
- Implement from an isolated clean worktree. The primary checkout was conflicted
  when this plan was approved and contains unrelated user-owned changes.
- Carry this plan into the implementation branch without modifying the existing
  incomplete `tasks/plan.md` or `tasks/todo.md`.
- Use fake providers and temporary homes for automated tests.

## Phase 1: Add the Role Contract

### Task 1: Define an optional task role

**Description:** Add a centralized `TaskRole` type with the values
`unspecified`, `orchestrator`, `explorer`, `worker`, `tester`, `reviewer`, and
`researcher`. Add an optional role field to `router.TaskSpec`.

**Acceptance criteria:**

- Missing or empty role normalizes to `unspecified` and preserves current output.
- Unknown role values fail at CLI or decoding boundaries with an actionable error.
- Existing serialized task fixtures continue to decode unchanged.

**Verification:** Focused table tests for parsing, normalization, JSON omission,
legacy JSON decoding, and invalid input.

**Dependencies:** None
**Likely files:** `pkg/router/types.go`, `pkg/router/types_test.go`
**Scope:** Small

### Task 2: Propagate role through CLI and plan contracts

**Description:** Add `--role` to task-producing route/run commands and an
optional `role` field to plan steps. Preserve plan schema version 1 because this
is an additive optional field.

**Acceptance criteria:**

- Existing commands and version-1 plans work without specifying a role.
- Human and JSON output include role only when it adds information.
- Plan conversion and execution preserve a supplied role end to end.

**Verification:** CLI argument tests, plan round-trip tests, legacy plan fixtures,
and invalid-role exit-code tests.

**Dependencies:** Task 1
**Likely files:** `cmd/veto/main.go`, `cmd/veto/run.go`, `cmd/veto/plan.go`,
`cmd/veto/exec.go`, focused tests
**Scope:** Medium

### Task 3: Propagate role through application, admission, and ledger boundaries

**Description:** Carry role through the typed control plane, include it in the
short structured admission request, and record it in run evidence. Do not change
filtering or ranking weights.

**Acceptance criteria:**

- Every task path either carries the supplied role or explicitly uses
  `unspecified`.
- Admission receives role as structured context without increasing its output
  budget or merging admission with execution.
- A role alone never grants tools, file access, or execution authority.

**Verification:** Control-plane parity tests, admission prompt/schema tests,
ledger round-trip tests, and a regression test proving identical ranking when
only role differs.

**Dependencies:** Tasks 1-2
**Likely files:** `internal/controlplane/contract.go`, application adapters,
`pkg/router/admission.go`, `pkg/ledger/ledger.go`, focused tests
**Scope:** Medium

### Checkpoint: Backward-compatible role support

- `go test -race -timeout 120s ./...`
- `go vet ./...`
- `go build ./cmd/veto`
- `VETO_BINARY=/path/to/fresh/veto ./scripts/onboarding-smoke.sh`
- `go run ./cmd/veto benchmark --corpus internal/eval/testdata/routing_corpus.json`
- Confirm role omission produces no routing or output regression.

## Phase 2: Deterministic Orchestration Advice

### Task 4: Define the adviser contract

**Description:** Add a pure adviser under `pkg/dispatch` that accepts explicit
task shape and risk signals and returns:

- mode: `root-only` or `delegated`;
- recommended roles;
- maximum useful parallelism;
- reason codes and human-readable reasons; and
- unknowns that prevented stronger advice.

**Acceptance criteria:**

- The contract is typed, versionable, deterministic, and JSON serializable.
- Missing signals produce conservative advice and explicit unknowns.
- The adviser cannot select a specific branded model or authorize execution.

**Verification:** Contract serialization tests and deterministic table tests.

**Dependencies:** Task 1
**Likely files:** `pkg/dispatch/advice.go`, `pkg/dispatch/advice_test.go`
**Scope:** Small

### Task 5: Implement conservative advice rules

**Description:** Recommend delegation only for explicit shapes such as multiple
independent research targets, independent file ownership, or a separate review
role. Default to root-only for small, sequential, ambiguous, or write-conflicting
tasks.

**Acceptance criteria:**

- Rules use only caller-provided task facts.
- The same input always yields the same ordered output.
- Advice caps parallelism and explains every delegated role.
- Unknown write ownership prevents parallel writer recommendations.

**Verification:** A table corpus covering small tasks, independent reads,
dependent work, shared writes, review separation, high risk, and missing data.

**Dependencies:** Task 4
**Likely files:** `pkg/dispatch/advice.go`, `pkg/dispatch/advice_test.go`
**Scope:** Small

### Task 6: Expose `veto advise`

**Description:** Add a read-only command that accepts the same task fields as
routing plus explicit task-shape signals and prints human or stable JSON advice.

**Acceptance criteria:**

- `veto advise` makes no provider calls and writes no user state.
- Exit codes and validation match existing CLI conventions.
- JSON exposes the typed result without terminal-only formatting.

**Verification:** Subprocess CLI tests with temporary homes, network-disabled
tests, help/output snapshots, and onboarding smoke coverage.

**Dependencies:** Tasks 4-5
**Likely files:** `cmd/veto/main.go`, a focused command file, control-plane action
definitions if required by the current architecture, and tests
**Scope:** Medium

### Checkpoint: Advice is bounded and deterministic

- Run the full Go test, vet, build, onboarding smoke, and routing benchmark gates.
- Verify advice changes neither ranking nor execution without an explicit caller
  action.

## Phase 3: Session and Role Usage Evidence

### Task 7: Extend ledger attribution

**Description:** Add optional `session_id`, `parent_run_id`, and `role` fields to
ledger records. Upgrade the schema only if required by the current decoder; in
either case, retain backward-compatible reads of schema-v1 records.

**Acceptance criteria:**

- Legacy records load with absent attribution rather than invented values.
- Child runs can be associated with one parent without changing run identity.
- Token and cost known/unknown flags retain their current semantics.
- IDs and objective content remain local and are not sent to providers merely for
  reporting.

**Verification:** Schema-v1 fixtures, new round trips, malformed input, unknown
usage, known-zero usage, and concurrent append/read tests under the race detector.

**Dependencies:** Task 3
**Likely files:** `pkg/ledger/ledger.go`, ledger tests, execution record adapters
**Scope:** Medium

### Task 8: Add `veto usage`

**Description:** Add local aggregation by run, session, role, provider, and model
with `--run`, `--session`, and `--since` filters plus human and JSON output.

**Acceptance criteria:**

- Aggregation reads saved ledger data and performs no provider calls.
- Unknown cost or token values are never summed as zero.
- Empty results are successful and explicit; malformed ledger data follows the
  repository's existing fail/skip policy.
- Output avoids objectives, prompts, credentials, and other sensitive content.

**Verification:** Golden aggregation fixtures, mixed known/unknown values,
time-boundary tests, deterministic ordering, corrupt records, and CLI tests.

**Dependencies:** Task 7
**Likely files:** `pkg/ledger`, `cmd/veto`, focused tests and CLI documentation
**Scope:** Medium

### Checkpoint: Run the demand experiment

Run an eight-user free-choice experiment over at least two days. Continue to the
later product phases only if all of the following are met:

- at least 4 of 8 users use the workflow for at least three unprompted tasks;
- users accept at least two non-default orchestration choices;
- users report at least two concrete benefits;
- no repeated correctness or safety regressions occur; and
- manual overrides stay at or below 25%.

If capacity interruption is the only demonstrated benefit, build capacity-aware
advice rather than a broader role classifier.

## Phase 4: Reviewer Diversity (Evidence-Gated)

### Task 9: Add explicit reviewer diversity policies

**Description:** Extend acceptance review selection with `model`,
`provider-preferred`, and `provider-required` diversity policies. The default
retains current behavior.

**Acceptance criteria:**

- The author model is never selected as its own reviewer.
- `provider-preferred` falls back visibly when provider diversity is unavailable.
- `provider-required` fails closed when a different provider cannot review.
- Provider identity comes from centralized metadata, not string matching.

**Verification:** Selection matrices across model/provider availability,
unavailable reviewer tests, and requested-review fail-closed integration tests.

**Dependencies:** Tasks 1 and 8 plus demand-gate approval
**Likely files:** `internal/application/review.go`, review policy types, tests,
user documentation
**Scope:** Medium

## Phase 5: Project-Scoped Codex Pack (Evidence-Gated)

### Task 10: Specify and implement a managed integration pack

**Description:** Only after the demand gate, define a separate project-scoped
pack of namespaced role profiles and skills. Provide explicit `install`, `status`,
and `uninstall` operations backed by a manifest and checksums.

**Acceptance criteria:**

- Installation targets only an explicit project directory.
- Existing files, `AGENTS.md`, and Codex configuration are never overwritten.
- No profile hard-codes a model; selection remains a Veto decision.
- Uninstall removes only files proven to be managed and unchanged.
- Dry-run and status modes are read-only.

**Verification:** Temporary-home install/status/uninstall tests, collision tests,
checksum mismatch tests, permissions tests, and repeat-install idempotency tests.

**Dependencies:** Tasks 6 and 8 plus a separately approved specification
**Scope:** Split into multiple small tasks during its specification

## Phase 6: Capacity Profiles (Evidence-Gated)

### Task 11: Add explicit advice profiles

**Description:** Add user-selected `conserve`, `balanced`, and `quality` profiles
to deterministic advice before allowing them to affect routing.

**Acceptance criteria:**

- Profiles express declared user preference, never inferred subscription state.
- No quota scraping or billing claim is introduced.
- Advice explains the trade-off and records the selected profile for evaluation.
- Routing behavior remains unchanged until a later, separately approved policy.

**Verification:** Deterministic profile matrices, omitted-profile compatibility,
and JSON contract tests.

**Dependencies:** Task 8 and evidence that capacity is a repeated user problem
**Scope:** Small

## Phase 7: Dependency-Aware Parallel Execution (Last, Evidence-Gated)

### Task 12: Enforce plan dependencies sequentially

**Description:** Before adding concurrency, make `depends_on` operational in the
existing sequential executor: validate missing/cyclic dependencies, run only
ready steps, and skip dependents after failure according to explicit policy.

**Acceptance criteria:**

- Invalid and cyclic plans fail before execution.
- A dependent step never runs before or after a failed prerequisite unless the
  plan explicitly allows that behavior.
- Existing dependency-free plans preserve current order.

**Verification:** Graph validation, failure propagation, cancellation, legacy
plan, and deterministic-order tests.

**Dependencies:** Phase 3 demand evidence
**Likely files:** `cmd/veto/plan.go`, `cmd/veto/exec.go`, focused tests
**Scope:** Medium

### Task 13: Add dry-run execution waves

**Description:** Compute and display dependency-safe execution waves without
running tasks.

**Acceptance criteria:**

- Wave membership is deterministic.
- Every dependency is in an earlier wave.
- Unknown write ownership is surfaced as preventing safe parallel execution.

**Verification:** DAG fixtures, stable-order snapshots, and shared-write cases.

**Dependencies:** Task 12
**Likely files:** plan graph package or existing plan module, CLI output, tests
**Scope:** Small

### Task 14: Add opt-in bounded parallel execution

**Description:** Add `--max-parallel` with a default of `1`. Permit concurrent
execution only for ready steps with explicitly non-overlapping writes.

**Acceptance criteria:**

- Default execution remains sequential.
- Parallelism is capped, cancellable, and race-free.
- Output/event ordering is deterministic where promised and otherwise explicitly
  identified by step.
- A failure stops or skips dependent work according to the plan policy.
- No objective text is treated as write authorization.

**Verification:** Race tests, bounded worker tests, cancellation and signal tests,
shared-write rejection, failure propagation, and onboarding smoke coverage.

**Dependencies:** Tasks 12-13 plus separate owner approval after successful dry
runs
**Scope:** Split into multiple medium tasks before implementation

## Recommended Delivery Sequence

1. Tasks 1-3: optional role contract and propagation.
2. Tasks 4-6: deterministic `veto advise`.
3. Tasks 7-8: local usage evidence.
4. Run the eight-user experiment and review the evidence.
5. Approve only the later phase that solves a demonstrated problem.

Each numbered task should land as an independently reviewable change and leave
the repository buildable. Do not combine later gated phases into the first pull
request.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Role names silently alter routing | High | Keep role non-scoring in the first slice and test rank equivalence. |
| Optional fields break old plans or ledger files | High | Additive fields, legacy fixtures, and explicit schema compatibility tests. |
| Advice is mistaken for authorization | High | Pure read-only command; retain execution and file-write boundaries. |
| Usage reports misstate unknown cost as free | High | Preserve known flags through aggregation and rendering. |
| Delegation adds overhead without user value | Medium | Enforce the eight-user demand gate before expanding. |
| Integration pack modifies user configuration | High | Project scope, manifest ownership, dry run, collision refusal, no overwrite. |
| Parallel steps corrupt shared work | High | Sequential dependency correctness first; require explicit non-overlapping writes. |
| Dirty checkout contaminates implementation | High | Use a clean isolated worktree and port only the approved plan. |

## Definition of Done for Each Implementation Slice

- Acceptance criteria and focused tests pass.
- `go test -race -timeout 120s ./...`, `go vet ./...`, and
  `go build ./cmd/veto` pass.
- Onboarding smoke and routing benchmark run when CLI, onboarding, routing,
  persistence, or execution behavior changes.
- Public CLI, JSON, plan, ledger, and control-plane changes are documented.
- Backward compatibility and known/unknown evidence semantics are reviewed.
- Diff contains no unrelated conflict resolution or user-owned changes.
- Local verification, real-provider verification, human trials, CI, release,
  deployment, and production acceptance are reported as separate evidence.

## Open Questions Deferred to Their Gates

- Whether users need roles as routing signals, or only as reporting labels.
- Whether provider-diverse review improves outcomes enough to justify reduced
  reviewer availability.
- Whether a Codex pack belongs in this repository or a separately versioned
  integration repository.
- Whether capacity preference should ever affect routing rather than advice.
- Whether real workloads contain enough safe independent writes to justify a
  parallel executor.
