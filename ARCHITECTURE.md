# veto — Architecture

> Diagrams first. Code second. Words only when neither suffices.

---

## 1. System Overview

```mermaid
flowchart TD
    U(["user"])

    subgraph CLI["cmd/veto — CLI layer"]
        RUN["veto run"]
        EXEC["veto exec"]
        ROUTE["veto route"]
        LOGIN["veto login"]
    end

    subgraph PKG_R["pkg/router — routing pipeline"]
        CI["InferComplexity()"]
        HF["HardFilter()"]
        SC["RankCandidates()"]
        AG["AdmissionGate.Ask()"]
    end

    subgraph PKG_E["pkg/executor — model transports"]
        ANT["AnthropicExecutor\nHTTP → api.anthropic.com"]
        OAI["OpenAIExecutor\nHTTP → api.openai.com"]
        ORT["OpenRouterExecutor\nHTTP → openrouter.ai"]
        CLICLI["CLIExecutor\nsubprocess → claude -p"]
        LOC["OpenAICompatibleExecutor\nHTTP → localhost / custom"]
    end

    subgraph STATE["~/.veto — persisted state"]
        CREDS["credentials.json\nAPI keys"]
        MODS["models.json\nlocal model definitions"]
        HIST["history.json\nadmission decisions"]
        SKILLS["skills/\nskill .md files"]
        CFG["config.json\ndisabled models, on_failure, …"]
        CKP["checkpoints/\ninterrupted run state"]
    end

    U --> RUN & EXEC & ROUTE & LOGIN
    RUN & EXEC & ROUTE --> CI --> HF --> SC --> AG
    AG -->|"accept → execute"| PKG_E
    AG -->|"reject → next model"| AG
    PKG_E -->|output| CLI
    STATE -->|keys| PKG_E
    STATE -->|caps + disabled| PKG_R
    STATE -->|skill bodies| CLI
    LOGIN --> STATE
```

---

## 2. Routing Pipeline

Every command routes through the same four stages. Stages 1–3 are pure functions (no I/O). Stage 4 makes real model calls.

```mermaid
flowchart TD
    IN["TaskSpec\n(objective, kind, risk, maxCost, complexity)"]

    subgraph S1["Stage 1 — Infer Complexity  (pkg/router/complexity.go)"]
        IC["InferComplexity(objective, kind)\nkeyword scoring → simple / moderate / complex"]
    end

    subgraph S2["Stage 2 — Hard Filter  (pkg/router/filter.go)"]
        T1{"missing\nrequired tool?"}
        T2{"context window\ntoo small?"}
        T3{"cost >\nMaxCostUSD?"}
        T4{"tier below\ncomplexity floor?"}
        T5{"task kind in\nmodel Weaknesses?"}
        PRUNE["prune → emit EventFilterFail"]
        PASS["survivor list"]
    end

    subgraph S3["Stage 3 — Score & Rank  (pkg/router/scorer.go)"]
        SCORE["Score(task, model, signal)\ncost 35% · success 25% · kind 20% · reject 10% · eval 10%"]
        SORT["sort descending → ranked []ModelCapabilities"]
    end

    subgraph S4["Stage 4 — Admission Gate  (pkg/router/admission.go)"]
        ASK["gate.Ask(model₁)\nsend structured prompt\nparse JSON response"]
        ACC{"accept\n≥ 0.7 conf?"}
        REJ["reject → try model₂"]
        WIN["→ (ModelCapabilities, AdmissionDecision)"]
        NONE["ErrNoCandidate"]
    end

    IN --> IC --> S2
    T1 -->|yes| PRUNE
    T2 -->|yes| PRUNE
    T3 -->|yes| PRUNE
    T4 -->|yes| PRUNE
    T5 -->|yes| PRUNE
    T1 -->|no| T2 --> T3 --> T4 --> T5 -->|no| PASS
    PASS --> SCORE --> SORT --> ASK
    ASK --> ACC
    ACC -->|yes| WIN
    ACC -->|no| REJ --> ASK
    REJ -->|"all tried\n(max 3)"| NONE
```

---

## 3. Complexity Inference

The manager auto-infers complexity before any filtering. No user input required.

```mermaid
flowchart LR
    OBJ["objective text\n(lowercased)"]

    KW_H("+3 each\ncqrs · event sourcing · event-driven\nmicroservices · distributed system\nmulti-tenant")
    KW_M("+2 each\narchitecture · infrastructure\nscalable · enterprise · system design\nfrom scratch · production-grade")
    KW_L("+1 each\ne2e · pipeline · service\ndeploy · integrate · infra")
    KW_S("-2 each\nsimple · basic · quick\nhello world")

    SCORE["total score"]

    KIND{"task kind"}
    KP["+2 plan"]
    KD["+1 debug"]
    KE["-2 extract / summarize"]

    TH{"threshold"}
    C1["≥ 4 → complex\nlarge tier only"]
    C2["≥ 1 → moderate\nmid or large tier"]
    C3["< 1 → simple\nany tier"]

    OBJ --> KW_H & KW_M & KW_L & KW_S --> SCORE
    KIND --> KP & KD & KE --> SCORE
    SCORE --> TH --> C1 & C2 & C3
```

**Code:**
```go
// pkg/router/complexity.go
func InferComplexity(objective string, kind TaskKind) Complexity {
    s := strings.ToLower(objective)
    score := 0
    for _, kw := range highComplexKeywords { if strings.Contains(s, kw) { score += 3 } }
    for _, kw := range medComplexKeywords  { if strings.Contains(s, kw) { score += 2 } }
    for _, kw := range lowComplexKeywords  { if strings.Contains(s, kw) { score++ } }
    for _, kw := range simpleKeywords      { if strings.Contains(s, kw) { score -= 2 } }
    switch kind {
    case KindPlan:               score += 2
    case KindDebug:              score++
    case KindExtract, KindSummarize: score -= 2
    }
    switch { case score >= 4: return ComplexityComplex
             case score >= 1: return ComplexityModerate
             default:         return ComplexitySimple }
}
```

**Tier enforcement in hard filter:**
```go
// pkg/router/filter.go
func tierMeetsComplexity(tier string, complexity Complexity) bool {
    switch complexity {
    case ComplexityComplex:  return tier == "large"
    case ComplexityModerate: return tier == "mid" || tier == "large"
    default:                 return true
    }
}
```

**Examples:**

| Objective | Kind | Score | Complexity | Eligible tiers |
|-----------|------|-------|------------|----------------|
| `"build e2e CQRS infrastructure with event sourcing"` | code-change | +1+3+2+1=7 | complex | large only |
| `"implement authentication service"` | code-change | +1+1=2 | moderate | mid, large |
| `"create simple html page"` | code-change | -2=−2 | simple | all |
| `"design microservices architecture"` | plan | +3+2+2=7 | complex | large only |

---

## 4. Scoring Formula

Models that survive the hard filter are ranked by a weighted score. Cheapest viable model wins by default.

```mermaid
xychart-beta horizontal
    title "Score weight distribution"
    x-axis ["cost fit", "success rate", "kind match", "reject rate", "eval score"]
    bar [35, 25, 20, 10, 10]
```

```go
// pkg/router/scorer.go
func Score(task TaskSpec, model ModelCapabilities, signal RoutingSignal) float64 {
    return kindMatch(model, task.Kind) * 0.20 +
        signal.HistoricalSuccessRate   * 0.25 +
        costFit(model, task)           * 0.35 +
        (1.0 - signal.HistoricalRejectRate) * 0.10 +
        signal.AvgEvalScore            * 0.10
}
```

**`costFit` — the key function:**
```go
func costFit(m ModelCapabilities, task TaskSpec) float64 {
    if task.MaxCostUSD > 0 {
        est := estimatedCost(m, task)
        if est >= task.MaxCostUSD { return 0.0 }
        return 1.0 - (est / task.MaxCostUSD)
    }
    const ref = 0.015 // opus input cost as reference baseline
    if m.CostPer1kInputUSD <= 0 { return 1.0 } // local / free
    return max(0.05, 1.0-(m.CostPer1kInputUSD/ref))
}
```

**Resulting costFit scores (no ceiling set):**

| Model | $/1k input | costFit |
|-------|-----------|---------|
| local ollama | $0.000 | **1.000** |
| haiku | $0.00025 | 0.983 |
| gpt-4.1-mini | $0.0004 | 0.973 |
| gpt-4.1 | $0.002 | 0.867 |
| sonnet | $0.003 | 0.800 |
| opus | $0.015 | **0.050** |

---

## 5. Admission Gate — Self-Admitting Receivers

Each candidate model receives a structured prompt and must respond with JSON only. The gate never silently accepts on ambiguity.

```mermaid
sequenceDiagram
    participant M as Manager
    participant G as AdmissionGate
    participant E as Executor
    participant LLM as Model API

    M->>G: Ask(ctx, task, model₁)
    note over G: wrap ctx with 20s timeout
    G->>E: Run(ctx, buildAdmissionPrompt(task, model₁))
    E->>LLM: POST /chat/completions<br/>{"model": "haiku", "messages": [{"role":"user","content":"..."}]}
    LLM-->>E: {"accept":false,"confidence":0.6,"reason_codes":["TASK_KIND_OUTSIDE_STRENGTHS"]}
    E-->>G: Result{Output: "..."}
    G->>G: parseAdmissionJSON → confidence < 0.7 → reject
    G-->>M: AdmissionDecision{Accept: false}, nil

    M->>G: Ask(ctx, task, model₂)
    G->>E: Run(ctx, buildAdmissionPrompt(task, model₂))
    E->>LLM: POST /chat/completions
    LLM-->>E: {"accept":true,"confidence":0.94,"estimated_tokens":1200,"estimated_cost_usd":0.0023}
    E-->>G: Result{Output: "..."}
    G->>G: parseAdmissionJSON → accept, confidence ≥ 0.7
    G-->>M: AdmissionDecision{Accept: true, Confidence: 0.94}, nil
    M-->>M: emit EventAskAccept → return model₂
```

**Admission prompt structure:**
```
You are the haiku model (small tier). A task router has selected you as a candidate.

TASK:
  kind: code-change
  objective: create a simple HTML page with history of Amsterdam
  constraints: none
  required tools: none
  risk: medium

YOUR PROFILE:
  max context tokens: 200000
  supported tools: bash, read, write, edit

Respond with ONLY valid JSON:
{
  "accept": <bool>,
  "confidence": <float 0.0-1.0>,
  "reason_codes": [...],
  "estimated_tokens": <int>,
  "estimated_cost_usd": <float>,
  "suggested_alternative_model": <string>,
  "required_task_changes": [...]
}
```

**Two failure paths:**

```mermaid
flowchart LR
    R["executor result"]
    E{"result.Error\n!= nil?"}
    J{"valid JSON\nin output?"}
    C{"confidence\n≥ 0.7?"}

    ERR["surface real error\nEventAskError{Detail: err}\ncontinue to next model"]
    SOFT["soft reject\nAdmissionDecision{Accept:false}\nReasonCodes:[PARSE_FAILURE]"]
    LOW["soft reject\nReasonCodes:[LOW_CONFIDENCE]"]
    ACC["AdmissionDecision{Accept:true}"]

    R --> E
    E -->|yes| ERR
    E -->|no| J
    J -->|no| SOFT
    J -->|yes| C
    C -->|no| LOW
    C -->|yes| ACC
```

---

## 6. Executors

```mermaid
classDiagram
    class Executor {
        <<interface>>
        +Run(ctx, prompt) Result
    }
    class AnthropicExecutor {
        -apiKey string
        -model string
        +Run(ctx, prompt) Result
    }
    class OpenAIExecutor {
        -apiKey string
        -model string
        -endpoint string
        +Run(ctx, prompt) Result
    }
    class CLIExecutor {
        -binary string
        -args []string
        +Run(ctx, prompt) Result
        +Stream(ctx, prompt, w) error
    }

    Executor <|.. AnthropicExecutor
    Executor <|.. OpenAIExecutor
    Executor <|.. CLIExecutor

    note for OpenAIExecutor "Also used for:\n• OpenRouter (openrouter.ai)\n• Ollama (localhost:11434)\n• LM Studio\n• any OpenAI-compatible server"
    note for CLIExecutor "Shells out to: claude -p --model X\nCost: $0 — flat subscription\nSupports streaming to stdout"
```

**Executor selection in `buildProviderRegistry`:**

```mermaid
flowchart TD
    START["buildProviderRegistry()"]
    SUB{"CLAUDE_SUBSCRIPTION\n= true?"}
    ANT_KEY{"ANTHROPIC_API_KEY\nset?"}
    OAI_KEY{"OPENAI_API_KEY\nset?"}
    ORT_KEY{"OPENROUTER_API_KEY\nset?"}
    LOCAL["load ~/.veto/models.json\nOpenAICompatibleExecutor per entry"]
    DISABLED["skip if name in\ndisabled_models (config.json)"]

    START --> SUB
    SUB -->|yes| CLI_E["CLIExecutor\nhaiku, sonnet, opus"]
    SUB -->|no| ANT_KEY
    ANT_KEY -->|yes| ANT["AnthropicExecutor\nhaiku, sonnet, opus"]
    ANT_KEY -->|no| OAI_KEY
    OAI_KEY -->|yes| OAI_E["OpenAIExecutor\ngpt-4.1, gpt-4.1-mini"]
    OAI_KEY -->|no| ORT_KEY
    ORT_KEY -->|yes| ORT_E["OpenRouterExecutor\nmeta-llama/llama-4-maverick"]
    START --> LOCAL
    DISABLED --> LOCAL
```

**Ollama auto-start (no manual `ollama serve` needed):**

```mermaid
sequenceDiagram
    participant R as Run()
    participant H as HTTP client
    participant O as ollama binary

    R->>H: POST localhost:11434 (attempt 1)
    H-->>R: connection refused
    R->>O: tryStartOllama(endpoint)
    O->>O: exec.LookPath("ollama") ✓
    O->>O: ollama serve &  (background)
    loop poll every 500ms, max 10×
        O->>H: GET localhost:11434
        H-->>O: 200 OK
    end
    O-->>R: true (server ready)
    R->>H: POST localhost:11434 (retry)
    H-->>R: 200 OK
```

---

## 7. Plan Execution — `veto exec`

Each step routes independently. A planning step goes to a large-tier model; a summarise step goes to haiku.

```mermaid
sequenceDiagram
    participant U as user
    participant E as exec.go
    participant M as Manager
    participant G as AdmissionGate
    participant EX as Executor

    U->>E: veto exec plan.md
    E->>E: ParsePlan → []PlanStep

    loop for each step (sequential)
        E->>E: InferComplexity(step.Task, step.Kind)
        E->>M: Route(ctx, TaskSpec{Kind, Complexity, Objective})
        M->>M: HardFilter → RankCandidates
        M->>G: Ask(cheapest candidate)
        G-->>M: accept / reject
        M-->>E: winning ModelCapabilities
        E->>EX: Run(withSkills(step.Task, skills))
        EX-->>E: output
        E->>E: reviewOutput (if success_criteria set)
        E-->>U: print output + ✓/✗
    end

    E->>E: final integrator review\n(all criteria + regression guard)
    E-->>U: PASS / FAIL
```

**Plan file format:**
```markdown
---
title: Refactor auth middleware
version: 1
steps:
  - task: "List what each function in auth/middleware.go does"
    kind: extract
    risk: low
  - task: "Rewrite token validation using stdlib crypto/hmac"
    kind: code-change
    risk: medium
    depends_on: [1]
    success_criteria: "no third-party JWT dep; existing tests pass"
  - task: "Design the new session storage architecture"
    kind: plan
    risk: high
---
```

**Per-step complexity and tier routing:**

```mermaid
flowchart LR
    S1["Step 1\nkind: extract\n'list what each function does'\n→ complexity: simple\n→ any tier → haiku wins"]
    S2["Step 2\nkind: code-change\n'rewrite token validation'\n→ complexity: moderate\n→ mid+ → sonnet / gpt-4.1"]
    S3["Step 3\nkind: plan\n'design session storage architecture'\n→ complexity: complex\n→ large only → opus / llama"]

    S1 --> S2 --> S3
```

---

## 8. Skills Injection

Skills are markdown files with YAML frontmatter. They are prepended to the executor prompt — not the admission prompt. Routing always uses the clean objective.

```mermaid
flowchart TD
    LOAD["loadSkills()\nread ~/.veto/skills/*.md\n+ user-approved dirs"]
    MATCH["matchSkills(spec)\nkind ∩ task.Kind\n+ optional keyword filter\ncap: 2 skills"]
    EMPTY{"any\nmatches?"}
    INJECT["withSkills(objective, bodies)\n→ ## Relevant skills\n<body>\n## Task\n<objective>"]
    SKIP["objective unchanged"]
    EXEC["Executor.Run(prompt)"]

    LOAD --> MATCH --> EMPTY
    EMPTY -->|yes| INJECT --> EXEC
    EMPTY -->|no| SKIP --> EXEC
```

**Skill file format:**
```markdown
---
name: code-change
kinds: [code-change, refactor]
keywords: []
---
- Prefer the minimal diff — change only what the task requires.
- Preserve existing naming conventions and indentation.
- Write one test per changed behaviour.
```

**Injected prompt structure:**
```
## Relevant skills

- Prefer the minimal diff — change only what the task requires.
- Preserve existing naming conventions and indentation.
- Write one test per changed behaviour.

## Task

rewrite the token validation function to use stdlib crypto/hmac
```

---

## 9. Acceptance-Criteria Review

Runs after execution when `--criteria` is set (on `veto run`) or `success_criteria` is set on a plan step.

```mermaid
sequenceDiagram
    participant C as cmdRun / cmdExec
    participant R as reviewOutput()
    participant M as Manager
    participant REV as Reviewer Model

    C->>C: executor produces output
    C->>R: reviewOutput(ctx, spec, output, executorModel)
    note over R: SkipModels = [executorModel]\n(prevent self-grading)
    R->>M: Route(ctx, TaskSpec{Kind:review, Objective:buildReviewPrompt(...)})
    M-->>R: reviewer model (≠ executor)
    R->>REV: Run(buildReviewPrompt(spec, output))
    REV-->>R: {"passed":true,"score":0.83,"criteria":[...]}
    R->>R: parseReviewJSON
    R-->>C: ReviewResult

    alt passed == false
        C->>C: PrintReview → exit 1
    else passed == true
        C->>C: PrintReview → continue
    end
```

**Reviewer response:**
```json
{
  "passed": false,
  "score": 0.50,
  "criteria": [
    {
      "criterion": "no third-party JWT dep",
      "met": true,
      "note": "only stdlib crypto/hmac used"
    },
    {
      "criterion": "existing tests pass",
      "met": false,
      "note": "TestRefresh fails — HMAC signature mismatch after migration"
    }
  ]
}
```

---

## 10. Event Bus

The manager emits events. The CLI wires `OnEvent` to a renderer and a logger — neither is coupled to the other.

```mermaid
flowchart LR
    MG["Manager.Route()\nemits ProgressEvent"]

    subgraph Consumers
        RND["Renderer\n(render.go)\nterminal animation\nspinner · accept/reject lines"]
        LOG["Logger\n(logger.go)\nJSON lines\n~/.veto/logs/veto-YYYY-MM-DD.log"]
        DASH["Dashboard\n(dashboard.go)\nbrowser UI\noptional --dash flag"]
    end

    MG -->|OnEvent callback| RND & LOG & DASH
```

| Event | Carries |
|-------|---------|
| `filter_pass` | model name |
| `filter_fail` | model name, reason code |
| `ask_start` | model name |
| `ask_accept` | model, confidence, est. tokens, est. cost |
| `ask_reject` | model, reason codes |
| `ask_error` | model, `Detail` = real error string |

---

## 11. State Files

```
~/.veto/
├── credentials.json     # { "ANTHROPIC_API_KEY": "sk-ant-...", ... }   mode 0600
├── models.json          # [{ "name": "qwen2.5", "endpoint": "...", ... }]
├── config.json          # { "disabled_models": [...], "on_failure": "abort", "skills": {...} }
├── history.json         # admission decisions (accept/reject per task+model)
├── skills/
│   ├── code-change.md   # auto-generated or hand-written skill files
│   └── review.md
├── plans/
│   └── 20260701-refactor-auth-converted.md
├── checkpoints/
│   └── a3f1b2c4d5e6f7a8.json   # interrupted run state (SHA-256 of task identity)
└── logs/
    └── veto-2026-07-02.log     # JSON lines, pruned after 7 days
```

**Task identity hash** (used for checkpoints and deduplication):
```go
func taskHash(objective, kind, risk string, maxCost float64) string {
    h := sha256.New()
    fmt.Fprintf(h, "%s|%s|%s|%.6f", objective, kind, risk, maxCost)
    return hex.EncodeToString(h.Sum(nil))[:16]
}
```

---

## 12. Package Layout

```mermaid
graph LR
    subgraph CMD["cmd/veto"]
        MAIN["main.go\nentry · provider registry\ndisable/enable"]
        RUN_F["run.go\ncmdRun · routeAndCapture"]
        EXEC_F["exec.go\ncmdExec · plan steps"]
        PLAN_F["plan.go\nParsePlan · ValidatePlan · convertPlan"]
        SKILLS_F["skills.go\nloadSkills · matchSkills · withSkills"]
        REVIEW_F["review.go\nbuildReviewPrompt · reviewOutput"]
        RENDER_F["render.go\nRenderer · PrintTaskHeader · OnEvent"]
        LOGIN_F["login.go\nveto login · Ollama wizard"]
        CKP_F["checkpoint.go\nCheckpoint · save/load"]
    end

    subgraph PKG_ROUTER["pkg/router"]
        TYPES["types.go\nTaskSpec · ModelCapabilities\nComplexity · Risk · TaskKind"]
        COMP["complexity.go\nInferComplexity · tierMeetsComplexity"]
        FILTER["filter.go\nHardFilter · estimatedCost"]
        SCORER["scorer.go\nScore · RankCandidates · costFit"]
        ADMISSION["admission.go\nAdmissionGate · buildAdmissionPrompt\nparseAdmissionJSON"]
        MANAGER["manager.go\nManager.Route · emit"]
        REGISTRY["registry.go\ncatalog · NewRegistry · NewRegistryFromModels"]
        EVENTS["events.go\nProgressEvent · EventKind"]
        STORE["store.go\nFileStore · MemoryStore · LogDecision"]
    end

    subgraph PKG_EXEC["pkg/executor"]
        ANT_E["anthropic.go\nAnthropicExecutor"]
        OAI_E["openai.go\nOpenAIExecutor · tryStartOllama"]
        CLI_E_F["cli.go\nCLIExecutor · Stream"]
        RETRY["retry.go\nretryableStatus · retryAfter"]
        RES["result.go\nResult{Output, Error}"]
    end

    CMD --> PKG_ROUTER
    CMD --> PKG_EXEC
    PKG_ROUTER -->|ExecutorFactory interface| PKG_EXEC
```
