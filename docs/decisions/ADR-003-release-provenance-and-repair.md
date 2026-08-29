# ADR-003: Separate official release provenance from source builds

## Status
Accepted

## Date
2026-08-29

## Context

Veto can be installed from a GitHub release archive, with `go install`, or from
a source checkout. A version string alone cannot distinguish those paths, and
only release archives have a project-produced binary checksum manifest.
Automatic repair also places untrusted network and archive data next to the
running executable.

## Decision

Release packaging sets both the normalized version and an `official` build
marker. Versioned `go install` builds resolve their public version from Go build
metadata but retain source-build provenance. `veto doctor` only compares a
binary to `BINARY_SHA256SUMS` or offers automatic reinstallation when the
official marker is present.

Repair downloads the exact same-version archive and both checksum manifests
over bounded HTTPS requests. Redirects stay on GitHub-owned hosts. The archive
must contain exactly the expected regular binary, and the archive checksum,
binary checksum, and candidate `veto version` must all agree before a same-
directory atomic replacement. Symlinks, package-manager paths, source builds,
and unwritable or foreign-owned targets are never replaced. Windows keeps a
verified staged binary for manual replacement because replacing the running
executable is not reliably atomic there.

## Alternatives considered

### Treat any matching version as official

Rejected: a locally rebuilt or `go install` binary could claim checksum-backed
provenance it does not have.

### Download and replace from the latest release

Rejected: recovery would become an implicit upgrade and would not prove that
the repaired executable matches the version the user was running.

### Let package managers and symlinks be replaced automatically

Rejected: this crosses ownership boundaries and can corrupt an installation
managed by another tool.

## Consequences

- Release archives are the recommended verifiable installation path.
- `go install` remains supported and reports its module version honestly.
- Checksums detect corruption and release mismatch, but are not signatures.
- Windows recovery needs one explicit manual replacement step.
- Release packaging must publish six archive checksums and six extracted-binary
  checksums for every version.
