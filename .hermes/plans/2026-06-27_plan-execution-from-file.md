# Plan: veto exec — execute a plan file with model routing

## Goal

Allow users to pass a structured plan file to veto. Each step in the plan is routed to the best model and executed. If the plan doesn't match veto's spec, the best available model converts it first (saving the converted version alongside the original).

---

## The Veto Plan Spec

A valid veto plan is a Markdown file with YAML frontmatter:

```markdown
---
title: "Human-readable plan title"
version: 1
---

## Steps

### 1. Step name
- **task**: "exact objective passed to veto run"
- **kind**: code-change|debug|refactor|summarize|extract|review|plan
- **risk**: low|medium|high
- **depends_on**: []
- **success_criteria**: "how to know this step succeeded"

### 2. Another step
- **task**: "..."
- **kind**: code-change
- **risk**: medium
- **depends_on**: [1]
- **success_criteria**: "..."
```

### Required fields per step
| Field | Type | Notes |
|-------|------|-------|
| `task` | string | Passed verbatim to `veto run` |
| `kind` | enum | One of the 7 valid kinds |
| `risk` | enum | `low`, `medium`, or `high` |

### Optional fields per step
| Field | Default | Notes |
|-------|---------|-------|
| `depends_on` | `[]` | Step indices (1-based) that must complete first |
| `success_criteria` | `""` | Human hint shown after execution |

### Validation rules
1. Frontmatter must parse as valid YAML with `title` and `version: 1`
2. At least one `### N.` step heading exists
3. Each step has `task`, `kind` (valid value), and `risk` (valid value)
4. `depends_on` indices must refer to earlier steps (no forward refs, no cycles)

---

## Current Context

- `veto run` routes + executes a single task (done)
- `providerRegistry` + `CLIExecutor` are in place
- No plan parsing or multi-step execution exists yet

---

## Proposed Approach

New command: `veto exec <plan.md>`

```
veto exec plan.md
         │
         ├─ parse & validate against spec
         │   valid → proceed
         │   invalid → ask user, use best model to convert
         │             save to ~/.veto/plans/[datetime]-[slug]-converted.md
         │             proceed with converted plan
         │
         └─ execute steps in order
             for each step: veto run --kind <k> --risk <r> "<task>"
             show per-step output, respect depends_on
             on failure: show error, ask continue/abort
```

---

## Step-by-Step Implementation Plan

### Step 1 — Plan spec + parser (`cmd/veto/plan.go`, new ~120 lines)

```go
type VetoPlan struct {
    Title   string
    Version int
    Steps   []PlanStep
}

type PlanStep struct {
    Index           int
    Name            string
    Task            string
    Kind            string
    Risk            string
    DependsOn       []int
    SuccessCriteria string
}

func ParsePlan(data []byte) (*VetoPlan, error)        // parse frontmatter + steps
func ValidatePlan(p *VetoPlan) []string               // returns list of violations
func planSlug(title string) string                    // "My Plan" → "my-plan"
```

Parser reads the file, splits YAML frontmatter (`---` blocks), then walks `### N. Name` headings to extract step fields.

### Step 2 — Plan conversion (`cmd/veto/plan.go`, ~30 lines)

```go
func convertedPlanPath(originalPath string, title string) string
// → ~/.veto/plans/2026-06-27T14:05:00-my-plan-converted.md
```

Conversion flow:
1. Read original plan text
2. Route a `plan` task to the best model: `"Convert this plan to veto plan spec. Original:\n\n<content>"`
3. Parse the model's response as a VetoPlan
4. If still invalid, show the violations and exit with a clear error
5. Write to `~/.veto/plans/[datetime]-[slug]-converted.md`
6. Print the saved path

Use the `opus` model explicitly for conversion (most capable at structured output).

### Step 3 — Execution engine (`cmd/veto/exec.go`, new ~120 lines)

```go
func cmdExec(args []string)
```

Flags: `--quiet`, `--dry-run` (print steps without executing), `--risk` (override all steps), `--timeout`

Execution loop:
```
for each step (topological order via depends_on):
    print "── Step N: <name> ──"
    call veto run logic inline (reuse cmdRun internals)
    on success: print success_criteria hint
    on failure: print error, prompt continue/abort (skip in --quiet)
```

Steps with no `depends_on` run sequentially by default. Parallel execution is a future enhancement.

### Step 4 — Wire into main (`cmd/veto/main.go`)

```go
case "exec":
    cmdExec(os.Args[2:])
```

Update `printUsage` to document `exec`.

---

## Files to Change

| File | Change |
|------|--------|
| `cmd/veto/plan.go` | New — spec, parser, validator, converter |
| `cmd/veto/exec.go` | New — `cmdExec`, execution loop |
| `cmd/veto/main.go` | Add `case "exec"`, update usage |
| `cmd/veto/run.go` | Extract `runTask(ctx, reg, objective, kind, risk)` helper so exec.go can reuse it without shell-out |

---

## Example UX

### Happy path (valid plan)

```
$ veto exec migrate-auth.md

  Plan: Migrate auth middleware to JWT (4 steps)

  ── Step 1: Review existing auth ────────────────────────────
  kind: review  risk: medium
  [routing + execution output]
  ✓ Done

  ── Step 2: Write JWT middleware ────────────────────────────
  kind: code-change  risk: high
  [routing + execution output]
  ✓ Done

  ── Step 3: Write tests ─────────────────────────────────────
  ...
```

### Invalid plan (needs conversion)

```
$ veto exec notes.md

  Plan validation failed (2 issues):
    • step 1: missing "kind"
    • step 2: "risk" has unknown value "critical"

  Convert notes.md to veto plan spec using opus? [y/N]: y

  Converting... saved to ~/.veto/plans/2026-06-27T14:05:12-notes-converted.md
  (original notes.md unchanged)

  ── Step 1: ...
```

### Dry run

```
$ veto exec migrate-auth.md --dry-run

  Plan: Migrate auth middleware to JWT (4 steps)
  Step 1  review    medium  "Review the existing session-based auth"
  Step 2  code-change  high  "Write JWT middleware replacing sessions"
  Step 3  code-change  medium  "Update all route handlers to use JWT"
  Step 4  review    low   "Review the final JWT implementation"
```

---

## Conversion Prompt (sent to model)

```
You are converting a plan to veto plan spec format.

VETO PLAN SPEC:
[spec text embedded]

ORIGINAL PLAN:
[file content]

Return ONLY the converted plan as a valid markdown file with YAML frontmatter.
No explanation, no prose before or after. The output must be parseable by veto.
```

---

## Validation

- Unit tests for `ParsePlan` and `ValidatePlan` covering: valid plan, missing fields, bad kind, cycle in depends_on
- Integration test: `veto exec --dry-run` against a known valid plan file
- Manual test: run a 2-step plan against the veto codebase itself

---

## Risks & Tradeoffs

| Risk | Mitigation |
|------|-----------|
| Model produces non-parseable conversion | Parse + re-validate; show error with violations rather than silently proceeding |
| Large plan files exceed context | Warn if plan > 100 steps or > 50k chars; recommend splitting |
| depends_on cycles | Detect in ValidatePlan; reject with clear message |
| User's plan has sensitive content | Conversion uses local CLIExecutor (subscription) when available — stays on-device |

---

## Open Questions

1. Should steps with no `depends_on` run in parallel (goroutines) or always serial? → Start serial, add `--parallel` flag later.
2. Should `veto exec` save a run log (which steps passed/failed)? → Nice to have; skip for now.
3. Should `--dry-run` also validate and trigger conversion? → Yes, dry-run should still catch invalid plans.
