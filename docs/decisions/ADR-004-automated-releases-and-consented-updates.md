# ADR-004: Automate releases and require consent for upgrades

**Status:** Accepted  
**Date:** 2026-08-29

## Context

Manually choosing versions, editing release notes, tagging, and updating the
Homebrew tap allowed the shipped binary to lag behind `main`. Veto also had no
way to tell an interactive user that a verified newer release was available.
The updater must not add latency or prompts to scripts, JSON consumers, CI, or
development builds, and it must preserve package-manager ownership and the
release-provenance boundary from ADR-003.

## Decision

Release Please maintains a version-and-changelog pull request from conventional
commits on `main`. Merging that explicit release gate creates the tag and GitHub
release, then dispatches the existing GoReleaser workflow at that immutable
tag. GoReleaser keeps the existing release notes, verifies all artifacts, and
updates the separate Homebrew tap when `HOMEBREW_TAP_TOKEN` is configured.

Every interactive stable build consults a private update cache. It refreshes the
latest GitHub release at most once per 24 hours and offers only a newer stable
release whose complete expected artifact set is visible. Installation requires
an explicit `y`; declining or any check failure continues the original command.
Homebrew performs Homebrew upgrades, source builds use exact versioned
`go install`, and verified standalone binaries reuse the ADR-003 replacement
checks. Other package-manager installs are never overwritten.

## Consequences

- Code merges automatically prepare release notes and the next version; the
  release PR remains the human publication and rollback gate.
- Artifact publication starts even though GitHub suppresses recursive tag-push
  workflow events created by its built-in token.
- Homebrew automation has a one-time, least-privilege tap-token dependency.
- Interactive users can upgrade immediately without silent self-modification;
  automated consumers keep stable output and startup behavior.
- A release is not offered until all expected archives and checksum manifests
  are published.
