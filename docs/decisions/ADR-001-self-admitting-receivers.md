# ADR-001: Self-admitting receivers as the routing primitive

## Status
Accepted

## Date
2026-06-27

## Context

veto needs to route tasks to the "best" available AI model across multiple providers and capability tiers. The naive approaches have well-known failure modes:

- **Static rules** ("use haiku for summaries, opus for planning") break when task nuance doesn't fit a keyword
- **Cost-only routing** ignores quality — cheap models on hard tasks produce bad output
- **Round-robin or random** ignores capability entirely
- **Router-as-oracle** (a separate model that decides) adds latency, cost, and a single point of failure

The core problem: the router can observe task metadata but not task difficulty. Difficulty only becomes apparent to something that has read the task and knows its own capabilities.

## Decision

Each candidate model is asked directly: *do you accept this task?* The model receives its own capability profile plus the task spec and must respond with structured JSON — accept/reject, confidence, estimated cost, estimated tokens, reason codes if rejecting.

The router's job is limited to:
1. Hard-filter models that provably cannot handle the task (wrong tools, context too large, cost ceiling exceeded, known weakness)
2. Rank survivors by historical signal and task-kind alignment
3. Ask each candidate in rank order; take the first that accepts with ≥ 70% confidence

This pattern is called a **self-admitting receiver**: the receiver (model) decides whether to admit the work, rather than the router deciding on its behalf.

## Alternatives considered

### Static capability matrix
Pre-define which model handles which task kind. Simple to implement, zero LLM calls at routing time.

Rejected: breaks on novel task combinations, requires manual upkeep as model capabilities evolve, cannot account for cost context known only at routing time.

### A separate "router model"
Use a cheap model (e.g. haiku) to evaluate which model should handle the task.

Rejected: adds a latency hop and a cost on every routing call, introduces a second model's failure modes, and the router model has no more visibility into task difficulty than a static rule. The self-admitting approach distributes judgment to the models that will actually do the work.

### User-specified model
Require the user to always pick the model.

Rejected: defeats the purpose of the tool. Users typically don't know which model is best for a given task, and even if they did, cross-provider comparisons change over time.

### Embedding-based similarity routing
Embed the task and compare to historical task embeddings, route to the model that succeeded on similar tasks.

Rejected: requires a vector store, embedding model, and historical data to be useful — too much infrastructure for the current scope. Can be layered on top of the admission gate later as a signal source (Phase 2).

## Consequences

**Positive:**
- Routing decisions are made by models with full self-knowledge of their capabilities and weaknesses
- Confidence gating (< 0.7 treated as rejection) prevents reluctant accepts from producing bad output
- Rejection reason codes give users actionable feedback (`COST_CEILING_EXCEEDED` → raise `--max-cost`)
- The admission prompt can evolve independently of the routing logic

**Negative:**
- Every routing call makes at least one real LLM request, adding latency and cost
- A model might misreport confidence (overconfident rejection, or accepting tasks it can't actually handle) — the router has no way to verify without executing the task
- The 70% confidence threshold is a judgment call with no empirical backing yet; it may need tuning

**Ceiling to watch:** if models start optimizing for acceptance (gaming the protocol), confidence scores become less reliable. The historical success rate signal in the scorer partially mitigates this — models with high accept-but-fail rates will be ranked lower over time.
