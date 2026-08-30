#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
skill_dir=${repo_root}/.agents/skills/veto-routing
skill_file=${skill_dir}/SKILL.md

if [[ ! -f "${skill_file}" ]]; then
    printf 'missing agent skill: %s\n' "${skill_file}" >&2
    exit 1
fi

if ! skill_frontmatter=$(awk '
    NR == 1 {
        if ($0 != "---") exit 1
        next
    }
    $0 == "---" {
        closed = 1
        exit
    }
    { print }
    END {
        if (!closed) exit 1
    }
' "${skill_file}"); then
    printf '%s\n' 'veto-routing skill frontmatter is missing or invalid' >&2
    exit 1
fi

if ! grep -Fqx 'name: veto-routing' <<<"${skill_frontmatter}"; then
    printf '%s\n' 'veto-routing skill name is missing or invalid' >&2
    exit 1
fi

if ! grep -Fq 'compatible with Codex, Claude Code, OpenCode, and Hermes' <<<"${skill_frontmatter}"; then
    printf '%s\n' 'veto-routing compatibility does not include supported coding agents' >&2
    exit 1
fi

if command -v opencode >/dev/null 2>&1; then
    tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/veto-opencode-skill.XXXXXX")
    trap 'rm -rf -- "${tmp_dir}"' EXIT
    mkdir -p "${tmp_dir}/home" "${tmp_dir}/config"

    (
        cd -- "${repo_root}"
        env -i \
            HOME="${tmp_dir}/home" \
            PATH="${PATH}" \
            TMPDIR="${tmp_dir}" \
            XDG_CONFIG_HOME="${tmp_dir}/config" \
            OPENCODE_DISABLE_DEFAULT_PLUGINS=1 \
            opencode debug skill --pure
    ) >"${tmp_dir}/skills.json"

    if ! jq -e \
        --arg skill_name 'veto-routing' \
        --arg skill_file "${skill_file}" \
        '[.[] | select(.name == $skill_name)]
         | length == 1 and .[0].location == $skill_file' \
        "${tmp_dir}/skills.json" >/dev/null; then
        printf '%s\n' 'OpenCode did not discover veto-routing from .agents/skills' >&2
        exit 1
    fi
fi

printf '%s\n' 'agent skill smoke passed'
