#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

version="${tag#v}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
changelog="$repo_root/CHANGELOG.md"

awk -v version="$version" '
  $0 ~ "^## \\[" version "\\](\\(| |$)" {
    found = 1
    next
  }
  found && /^## / { exit }
  found && /^\[[^]]+\]: / { exit }
  found { print }
  END {
    if (!found) {
      print "CHANGELOG.md has no release entry for " version > "/dev/stderr"
      exit 1
    }
  }
' "$changelog" | sed '/./,$!d'
