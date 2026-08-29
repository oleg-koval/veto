#!/usr/bin/env bash

set -euo pipefail

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

if [[ "${1:-}" == --verify-binary ]]; then
    if [[ $# -ne 6 ]]; then
        printf 'usage: %s --verify-binary manifest binary version os arch\n' "$0" >&2
        exit 2
    fi
    manifest=$2
    binary=$3
    version=$4
    target_os=$5
    target_arch=$6
    if [[ -z "${manifest}" ]]; then
        exit 0
    fi
    binary_name=veto
    if [[ "${target_os}" == windows ]]; then
        binary_name=veto.exe
    fi
    member="veto_${version}_${target_os}_${target_arch}/${binary_name}"
    expected=$(awk -v member="${member}" '$2 == member {print $1}' "${manifest}")
    if [[ ! "${expected}" =~ ^[0-9a-fA-F]{64}$ ]]; then
        printf 'binary manifest has no unique checksum for %s\n' "${member}" >&2
        exit 1
    fi
    actual=$(sha256_file "${binary}")
    if [[ "${actual}" != "${expected}" ]]; then
        printf 'GoReleaser binary differs from package manifest: %s\n' "${member}" >&2
        exit 1
    fi
    exit 0
fi

for required_command in go python3 tar zip unzip; do
    if ! command -v "${required_command}" >/dev/null 2>&1; then
        printf 'release packaging requires %s\n' "${required_command}" >&2
        exit 1
    fi
done

if [[ $# -lt 1 || $# -gt 2 ]]; then
    printf 'usage: %s vMAJOR.MINOR.PATCH [dist-dir]\n' "$0" >&2
    exit 2
fi

tag_name=$1
if [[ ! "${tag_name}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
    printf 'release tag must be vMAJOR.MINOR.PATCH (got %s)\n' "${tag_name}" >&2
    exit 1
fi
version=${tag_name#v}

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
dist_dir=${2:-"${repo_root}/dist"}
mkdir -p "${dist_dir}"
dist_dir=$(cd -- "${dist_dir}" && pwd)
if [[ "${dist_dir}" == "${repo_root}" ]]; then
    printf '%s\n' 'refusing to use the repository root as the release output directory' >&2
    exit 1
fi
if find "${dist_dir}" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
    printf 'release output directory must be empty: %s\n' "${dist_dir}" >&2
    exit 1
fi

stage_dir=$(mktemp -d "${TMPDIR:-/tmp}/veto-release.XXXXXX")
smoke_home=''
cleanup() {
    rm -rf -- "${stage_dir}"
    if [[ -n "${smoke_home}" ]]; then
        rm -rf -- "${smoke_home}"
    fi
}
trap cleanup EXIT

sha256_stream() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum | awk '{print $1}'
    else
        shasum -a 256 | awk '{print $1}'
    fi
}

archive_manifest=${dist_dir}/SHA256SUMS
binary_manifest=${dist_dir}/BINARY_SHA256SUMS
: >"${archive_manifest}"
: >"${binary_manifest}"

host_os=$(go env GOOS)
host_arch=$(go env GOARCH)
host_binary=''

while IFS=' ' read -r target_os target_arch; do
    artifact="veto_${version}_${target_os}_${target_arch}"
    artifact_root=${stage_dir}/${artifact}
    mkdir -p "${artifact_root}"
    binary_name=veto
    if [[ "${target_os}" == windows ]]; then
        binary_name=veto.exe
    fi
    binary_path=${artifact_root}/${binary_name}

    CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}" go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${version} -X main.buildProvenance=official" \
        -o "${binary_path}" ./cmd/veto
    chmod 0755 "${binary_path}"
    printf '%s  %s/%s\n' "$(sha256_file "${binary_path}")" "${artifact}" "${binary_name}" >>"${binary_manifest}"

    if [[ "${target_os}" == "${host_os}" && "${target_arch}" == "${host_arch}" ]]; then
        host_binary=${binary_path}
    fi

    if [[ "${target_os}" == windows ]]; then
        (cd "${stage_dir}" && zip -q -r "${dist_dir}/${artifact}.zip" "${artifact}")
        archive_entries=$(unzip -Z1 "${dist_dir}/${artifact}.zip")
    else
        COPYFILE_DISABLE=1 tar -C "${stage_dir}" -czf "${dist_dir}/${artifact}.tar.gz" "${artifact}"
        archive_entries=$(tar -tzf "${dist_dir}/${artifact}.tar.gz")
    fi
    expected_entries=$(printf '%s/\n%s/%s' "${artifact}" "${artifact}" "${binary_name}")
    if [[ "${archive_entries}" != "${expected_entries}" ]]; then
        printf 'archive contains unexpected entries: %s\n' "${artifact}" >&2
        exit 1
    fi
done <<'TARGETS'
darwin arm64
darwin amd64
linux amd64
linux arm64
windows amd64
windows arm64
TARGETS

while IFS= read -r archive_path; do
    printf '%s  %s\n' "$(sha256_file "${archive_path}")" "$(basename -- "${archive_path}")" >>"${archive_manifest}"
done < <(find "${dist_dir}" -maxdepth 1 -type f \( -name 'veto_*.tar.gz' -o -name 'veto_*.zip' \) | sort)

if [[ $(wc -l <"${archive_manifest}" | tr -d ' ') -ne 6 ]]; then
    printf '%s\n' 'archive checksum manifest must contain six entries' >&2
    exit 1
fi
if [[ $(wc -l <"${binary_manifest}" | tr -d ' ') -ne 6 ]]; then
    printf '%s\n' 'binary checksum manifest must contain six entries' >&2
    exit 1
fi
if [[ $(find "${dist_dir}" -maxdepth 1 -type f | wc -l | tr -d ' ') -ne 8 ]]; then
    printf '%s\n' 'release output must contain six archives and two manifests' >&2
    exit 1
fi

while read -r expected asset; do
    actual=$(sha256_file "${dist_dir}/${asset}")
    if [[ "${actual}" != "${expected}" ]]; then
        printf 'archive checksum verification failed: %s\n' "${asset}" >&2
        exit 1
    fi
done <"${archive_manifest}"

while read -r expected member; do
    artifact=${member%/*}
    if [[ "${member}" == *.exe ]]; then
        actual=$(unzip -p "${dist_dir}/${artifact}.zip" "${member}" | sha256_stream)
    else
        actual=$(tar -xOzf "${dist_dir}/${artifact}.tar.gz" "${member}" | sha256_stream)
    fi
    if [[ "${actual}" != "${expected}" ]]; then
        printf 'archived binary checksum verification failed: %s\n' "${member}" >&2
        exit 1
    fi
done <"${binary_manifest}"

if [[ -z "${host_binary}" ]]; then
    printf 'host platform %s/%s is not in the release target list\n' "${host_os}" "${host_arch}" >&2
    exit 1
fi
if [[ "$("${host_binary}" version)" != "veto ${version}" ]]; then
    printf '%s\n' 'native release binary reports the wrong version' >&2
    exit 1
fi
smoke_home=$(mktemp -d "${TMPDIR:-/tmp}/veto-release-home.XXXXXX")
doctor_json=$(env -i HOME="${smoke_home}" PATH="${PATH}" "${host_binary}" doctor --json --offline)
python3 - "${version}" "${doctor_json}" <<'PY'
import json
import sys

version, raw = sys.argv[1:]
report = json.loads(raw)
assert report["ok"] is True, report
checks = {check["id"]: check for check in report["checks"]}
assert checks["build.version"]["message"] == f"version {version}", checks
assert checks["build.provenance"]["status"] == "PASS", checks
assert checks["release.integrity"]["status"] == "WARN", checks
PY

printf 'release artifacts ready: %s\n' "${dist_dir}"
