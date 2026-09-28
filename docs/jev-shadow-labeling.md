# Jev paired-label protocol

The v0.14 JSONL stream records what Jev and sequential admission chose, but
normal execution observes only the authoritative choice. A different Jev
choice has no task-success outcome merely because the authoritative run
succeeded or failed. Never copy the authoritative label to that candidate or
turn an unexecuted alternative into a known failure.

## Collecting comparable outcomes

1. Freeze a consented evaluation corpus before looking at Jev outcomes. Record
   task kind, risk, success criteria, candidate versions, tool access, output
   budget, and timeout. Keep raw tasks and the private route-ID-to-task mapping
   outside the redacted shadow JSONL. Do not use sensitive production tasks
   without authorization.
2. Replay only tasks that can be made side-effect-free. Run the authoritative
   and Jev-selected candidates on the **same** frozen task in separate,
   isolated workspaces. Randomize execution order, use the same safe tools
   and budgets, and do not let either run see the other's output. Deny or mock
   remote writes, deployments, outbound messages, and account credentials;
   restrict network egress to explicitly authorized model endpoints. A
   shadow-only execution must never mutate the live user task or any external
   service. Skip a task if these boundaries cannot be enforced. Billable
   provider calls require separate owner authorization and a spending cap;
   this protocol itself grants neither.
3. Grade both outputs against the same predeclared criteria, preferably with
   deterministic tests and a reviewer blind to which decision engine selected
   each candidate. Append an `execution_label` for each actually evaluated
   route/candidate pair. A completed, reviewable failure is `known: true,
   value: false`; an unrun, unavailable, or inconclusive alternative remains
   `known: false`. Preserve measured zero and unknown cost separately.
4. Audit the materialized dataset for duplicate scenarios, missing task kinds,
   unmatched route IDs, divergent choices without outcomes, and label
   provenance. Run `veto shadow-report` only on the redacted JSONL. Keep the
   private mapping and review artifacts access-controlled; do not publish them
   by default.

Policy v2 requires all divergent Jev-selected candidates on
authority-labeled routes to have known outcomes, in addition to the existing
95% paired-label and statistical gates. If a safe paired run cannot be made,
leave the label unknown and readiness at `insufficient_data`. A report cannot
prove consent, isolation, blindness, or provenance; those are separate human
acceptance checks. It also cannot turn a launch price into measured billed
cost.

`internal/eval/paired` now provides an offline replay core with injected runner
and grader interfaces, randomized execution order, separate temporary
workspaces, bounded contexts, and atomic redacted label output. Injected
runners and graders must honor those contexts. Its fake-runner tests are harness
evidence, not Jev-quality evidence. The low-level replay call cannot verify
that a private model name belongs to an opaque candidate key. The CLI still
does not export a private route-ID-to-task mapping or ship a production replay
runner. No real TypeSafe account, paid call, or paired live run has been
completed.

This offline slice adds an opt-in route-time `PrivateRouteWitnessRecorder`
and a private manifest. The witness stores a route-scoped key,
opaque-key-to-model-identity bindings, the known tool snapshot, and a
fingerprint of the replayable task fields, not raw objective text. The manifest supplies the frozen objective and criteria
separately; validation recomputes candidate keys and checks the fingerprint,
route timestamp, kind, risk, provider/model/runtime identity, and exact known
tool snapshot before either fake candidate runs. Replay passes those captured
values to the runner, which must reject current configuration that differs.
Private manifest files require a trusted, stable directory
without group or other permissions outside a Git worktree, use 0600 file
permissions, and cannot overwrite an existing path. File operations pin the
checked directory; a failed write may leave a partial private file for manual
inspection and removal. The route key checks model bindings against the
redacted candidate keys, but a holder of the private key can forge the task fingerprint.
Confirm that a witness was captured by the route-time recorder before using it
as provenance. No production command enables capture, and the runner
and grader remain injected fakes in tests.
