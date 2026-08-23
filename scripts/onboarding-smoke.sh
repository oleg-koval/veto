#!/usr/bin/env bash

set -euo pipefail

if ! command -v python3 >/dev/null 2>&1; then
    printf '%s\n' 'onboarding smoke requires python3' >&2
    exit 1
fi

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "${repo_root}"
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/veto-onboarding.XXXXXX")
server_pid=''

cleanup() {
    if [[ -n "${server_pid}" ]]; then
        kill "${server_pid}" 2>/dev/null || true
        wait "${server_pid}" 2>/dev/null || true
    fi
    rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT

veto_binary=${VETO_BINARY:-"${tmp_dir}/veto"}
if [[ -z "${VETO_BINARY:-}" ]]; then
    if command -v rtk >/dev/null 2>&1; then
        rtk proxy go build -o "${veto_binary}" ./cmd/veto
    else
        go build -o "${veto_binary}" ./cmd/veto
    fi
fi
if [[ ! -x "${veto_binary}" ]]; then
    printf 'binary is not executable: %s\n' "${veto_binary}" >&2
    exit 1
fi

smoke_home=${tmp_dir}/home
mkdir -p "${smoke_home}"
smoke_workdir=${tmp_dir}/work
mkdir -p "${smoke_workdir}"

veto() {
    (cd "${smoke_workdir}" && env -i \
        HOME="${smoke_home}" \
        PATH="${PATH}" \
        TMPDIR="${tmp_dir}" \
        "${veto_binary}" "$@")
}

assert_contains() {
    local value=$1
    local expected=$2
    if [[ "${value}" != *"${expected}"* ]]; then
        printf 'expected output to contain %q, got:\n%s\n' "${expected}" "${value}" >&2
        exit 1
    fi
}

assert_contains "$(veto --help)" 'USAGE'
assert_contains "$(veto --help)" 'COMMANDS'
assert_contains "$(veto version)" 'veto '
assert_contains "$(veto providers)" 'No providers configured'

if [[ -e "${smoke_home}/.veto/credentials.json" ]]; then
    printf '%s\n' 'providers unexpectedly created credentials.json' >&2
    exit 1
fi

server_info=${tmp_dir}/server.info
python3 scripts/onboarding_fake_provider.py "${server_info}" >"${tmp_dir}/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 50); do
    if [[ -s "${server_info}" ]]; then
        break
    fi
    sleep 0.1
done
if [[ ! -s "${server_info}" ]]; then
    printf '%s\n' 'fake provider did not start' >&2
    cat "${tmp_dir}/server.log" >&2 || true
    exit 1
fi
server_port=$(<"${server_info}")

python3 - "${smoke_home}/.veto/models.json" "${server_port}" <<'PY'
import json
import os
import sys

path, port = sys.argv[1:]
os.makedirs(os.path.dirname(path), mode=0o700, exist_ok=True)
with open(path, "w", encoding="utf-8") as models:
    json.dump([{
        "name": "smoke-local",
        "endpoint": f"http://127.0.0.1:{port}/v1/chat/completions",
        "model": "smoke-model",
        "strengths": ["summarize"],
    }, {
        "name": "review-local",
        "endpoint": f"http://127.0.0.1:{port}/v1/chat/completions",
        "model": "review-model",
        "strengths": ["review"],
        "weaknesses": ["summarize"],
    }], models)
os.chmod(path, 0o600)
PY

python3 - "${smoke_home}/.veto/models.json" <<'PY'
import os
import sys

mode = os.stat(sys.argv[1]).st_mode & 0o777
if mode != 0o600:
    raise SystemExit(f"models.json mode is {mode:o}, want 600")
PY

providers_output=$(veto providers)
assert_contains "${providers_output}" 'smoke-local'
assert_contains "${providers_output}" '2 model(s) available for routing'

route_output=$(veto route --json --timeout 10s 'summarize this example')
python3 - "${route_output}" <<'PY'
import json
import sys

result = json.loads(sys.argv[1])
assert result["model"] == "smoke-local", result
assert result["kind"] == "summarize", result
assert result["confidence"] >= 0.7, result
PY

run_output=$(veto run --quiet --timeout 10s 'summarize this example')
if [[ "${run_output}" != *'SMOKE EXECUTION OK'* ]]; then
    printf 'unexpected run output:\n%s\n' "${run_output}" >&2
    exit 1
fi

output_path=result.txt
veto run --quiet --timeout 10s --output "${output_path}" 'summarize this example' >/dev/null
assert_contains "$(<"${smoke_workdir}/${output_path}")" 'SMOKE EXECUTION OK'
if veto run --quiet --timeout 10s --output "${output_path}" 'summarize this example' >/dev/null 2>/dev/null; then
    printf '%s\n' 'output overwrite unexpectedly succeeded without --force' >&2
    exit 1
fi
veto run --quiet --timeout 10s --output "${output_path}" --force 'summarize this example' >/dev/null

veto run --quiet --timeout 10s --criteria 'output is smoke' 'summarize this example' >/dev/null
if veto run --quiet --timeout 10s --criteria 'output is not smoke' 'summarize this example' >/dev/null 2>/dev/null; then
    printf '%s\n' 'inconsistent review unexpectedly passed' >&2
    exit 1
fi

printf '%s\n' 'onboarding smoke passed'
