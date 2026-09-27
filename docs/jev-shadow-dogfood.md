# Jev shadow dogfood findings

This is the separate issue ledger requested for the v0.13/v0.14 dogfood run.
The underlying Veto feedback artifacts are local, redacted, and unpublished.
No GitHub issue, external message, credential change, or provider call was made
while collecting them.

| Finding | Scope | Current state |
| --- | --- | --- |
| `exec` flags after the plan path are ignored | CLI parsing | Fixed in the v0.14 follow-up parser and covered by argument tests |
| Provider readiness and Codex admission disagree | Provider discovery/admission | Open |
| Unsupported Codex output limits fail after admission | Execution preflight | Open |
| Claude can admit before an unsafe permission rule prevents execution | Provider preflight | Open |
| Codex advertised write tools but launched read-only | Execution sandbox | Fixed in the current code and covered by executor argument tests |
| Codex cannot commit from a linked worktree under workspace-write | Sandbox/worktree Git metadata | Open |
| Onboarding PTY resize smoke blocks after its timeout | Test harness; reproduced on the baseline | Open, pre-existing |
| Codex admission fails under a workspace sandbox before routing | Sandbox/app-server startup | Open; not retried to avoid another model route |
| Divergent Jev choices cannot yet receive real paired outcome labels from the CLI | Shadow evaluation harness | Open; private route-to-task mapping and isolated alternative replay are needed before promotion evidence |

The v0.14 onboarding run reached the same PTY `waitpid` hang and was terminated
after the bounded observation window. That result is unavailable, not a pass,
and did not indicate a Jev-shadow regression. The Jev integration itself has
only local synthetic and HTTP-server coverage; no real TypeSafe account or
labeled live traffic was used.
