# Veto launch playbook

Status: v0.1.0 public beta. Release mechanics and offline routing behavior are
verified independently from provider access, calibration, and multi-user
onboarding, which remain post-release beta validation in
[release-readiness.md](release-readiness.md).

## Launch angle

For developers building workflows across multiple AI providers, Veto replaces
hardcoded model selection with an auditable routing decision: filter candidates
that cannot fit, rank the rest cheapest-viable-first, then let each model accept
or reject the task before execution.

Why now: model catalogs and provider choices keep expanding, making manual or
one-model-for-everything routing harder to justify. Veto differs from a static
gateway by making admission explicit and machine-readable.

## Repository changes

- Lead with the user problem and target audience in the README.
- Keep a clearly labeled example routing trace and a three-command quickstart in
  the first screen; replace the example with a redacted real-provider recording
  before launch.
- Make checksummed release archives the recommended install path; retain
  `go install` as a supported source-build path.
- Link the architecture, release gates, launch plan, license, and contribution
  expectations from the README.
- After owner approval, add GitHub topics such as `ai`, `llm`, `model-router`,
  `go`, and `cli`; do not treat stars or topic placement as product proof.

## Launch assets

### Short tagline

Stop hardcoding which AI model gets every task.

### Announcement

Veto is an open-source Go CLI for developers using more than one AI provider.
It filters models that cannot meet a task's constraints, ranks viable candidates
with a cost bias, and asks each candidate for a structured accept/reject decision
before execution. Use the terminal UI interactively or `--json` in scripts and
agent infrastructure. The project is a beta: routing and release mechanics have
offline test coverage, while real-provider quality and savings claims remain
deliberately ungrounded until user trials and calibration are complete.

### Social variants

1. I built Veto because “send every task to the biggest model” is an expensive
   default. It filters impossible candidates, asks the rest to accept or reject
   the task, and exposes the routing decision as JSON. Open-source Go CLI:
   https://github.com/oleg-koval/veto

2. What if an AI model could veto a task before it ran? Veto combines hard
   constraints, cheapest-viable-first ranking, and structured model
   self-admission. When nothing fits, you get rejection reasons instead of a
   silent fallback: https://github.com/oleg-koval/veto

3. Multi-model stacks create a new engineering problem: model choice becomes
   application logic. Veto turns that choice into an inspectable pipeline—hard
   filter, adaptive rank, structured admission, then execution. I would value
   feedback from people operating more than one provider:
   https://github.com/oleg-koval/veto

4. Veto routes AI tasks without pretending an offline benchmark proves real
   production savings. The router and failure paths are testable; provider
   quality still needs labeled workloads. If you care about honest routing
   evaluation, the design and limits are here: https://github.com/oleg-koval/veto

5. Local models and hosted APIs can participate in the same Veto routing pass.
   Transport capabilities stay explicit: HTTP paths are text-only, while the
   Claude CLI path can expose executable tools. No invented tool support, no
   silent file writes: https://github.com/oleg-koval/veto

### Product Hunt

Tagline: Let AI models accept or reject tasks before execution.

Description: Veto is a scriptable model router for developers using multiple AI
providers. It filters candidates by constraints, ranks viable models with a cost
bias, and asks each one for a structured admission decision. Route only, execute
the winner, or consume a one-line JSON result in your own agent infrastructure.

### Hacker News title candidates

- Show HN: Veto – let models reject tasks before routing
- Show HN: A Go model router with structured self-admission
- Show HN: Veto, an auditable router for multi-provider AI tasks

### Direct outreach note

I saw your work on multi-model routing or AI infrastructure and thought the
admission approach in Veto might be relevant. It filters impossible candidates,
ranks the rest, then asks them to explicitly accept or reject a task. I am looking
for technical criticism and fresh-user onboarding feedback, not promotion. If
that is in your area, the repo and known limits are here:
https://github.com/oleg-koval/veto

Contact only people who have publicly discussed model routing, AI cost controls,
local-model orchestration, or agent infrastructure. Prefer maintainers and
practitioners with a visible feedback channel. Send one relevant note, avoid
scraped lists and automated follow-ups, and record opt-outs.

## Distribution sequence

1. Launch on Hacker News first because Veto's architecture, trade-offs, and Go
   implementation are the strongest fit for a technical audience.
2. Post the shorter technical variants on X, Bluesky, or Mastodon, then use the
   longer context-setting variant on LinkedIn. Link to the repository, not a
   generic landing page.
3. Use Product Hunt as a secondary discovery channel only after a versioned
   binary and clean terminal recording exist.
4. Send the direct note to a small, hand-selected group after the public post so
   recipients can inspect the discussion and known limitations.

Choose a launch window with at least four uninterrupted hours for answering
questions, reproducing installation failures, and labeling issues. Do not
schedule channel posts unless the owner can cover that response window.

## Share loops

- Shareable decision artifact: `veto route --json "<task>"` produces a compact
  routing result that can be pasted into an issue, benchmark note, or build log
  after proprietary task text is removed.
- Before/after proof: compare a hardcoded model assignment with Veto's terminal
  trace showing filters, rejection reasons, and the accepted candidate.
- Reusable benchmark: publish the checked-in offline corpus result as evidence of
  deterministic policy behavior only. Add real labeled workloads before making
  quality, calibration, or savings claims.
- Community recipe: invite users to contribute sanitized task-kind examples and
  provider compatibility findings through GitHub issues or pull requests.
- No referral loop is planned. Veto has no waitlist or account system, and adding
  one would not improve the current open-source user experience.

## Proof and metrics

Veto has no hidden product analytics. Measure the beta with explicit,
privacy-preserving evidence:

- Activation: a trial participant completes one successful `veto route --json`
  and one `veto run --quiet` using the onboarding protocol.
- Share: a user voluntarily posts a sanitized terminal trace, JSON decision, or
  reproducible routing example that links back to the repository.
- Conversion: a repository visitor downloads a release artifact or reports a
  successful Go install and first route. GitHub release downloads are a proxy;
  they do not prove activation.
- Retention proxy: the same opted-in user runs Veto on a second day or submits a
  second distinct routing example within 14 days.
- Feedback: GitHub issues and the structured fresh-user trial notes are the
  canonical feedback paths.

Capture a baseline immediately before launch and review at 24 hours, 7 days,
and 14 days: GitHub unique visitors/referrers when available, release downloads,
issues, external routing examples, completed activations, and returning users.
Record counts separately; do not collapse stars, page views, installs, and
successful routes into one vanity metric.

## Risks and assumptions

- Self-reported confidence can be miscalibrated. The 70% gate is a routing rule,
  not proof that accepted work is correct.
- Cost and token values may be estimates or unknown depending on the provider.
- Provider model catalogs, access, and pricing can drift; verify release accounts
  immediately before launch.
- API and local OpenAI-compatible transports are text-only through Veto. Only the
  current Claude CLI transport exposes executable tools.
- v0.1.0 is a beta, not a production-readiness claim. Archive and binary
  checksums detect corruption and release mismatch but are not signatures.
- Launch preparation improves comprehension and distribution; it does not
  guarantee virality, adoption, savings, or routing quality.

## Next actions

The `v0.1.0` beta was published and its six archives, `SHA256SUMS`,
`BINARY_SHA256SUMS`, native official binary, online doctor result, and tagged
Go installation were verified on 2026-08-29. Homebrew was not published.

1. Complete provider-account verification and review current model IDs and
   pricing without committing raw account inventories.
2. Complete three fresh-user trials across at least two operating systems and
   resolve every blocking onboarding failure.
3. Run labeled real-provider calibration before publishing quality or savings
   claims.
4. Capture one clean terminal recording from install through `veto doctor` and
   the first JSON route;
   redact task text, usernames, paths, keys, and provider account data.
5. Set repository topics, publish the
   channel-native copy, and reserve time to answer feedback on launch day.
