#!/usr/bin/env bash
set -euo pipefail

usage() {
    printf 'usage: %s VERSION ARCHIVE OUTPUT_DIR\n' "$0" >&2
    exit 2
}

[[ $# -eq 3 ]] || usage
version=$1
archive=$2
output_dir=$3

[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
    printf 'Arch packages require a stable semver version: %s\n' "$version" >&2
    exit 2
}
[[ -f $archive ]] || {
    printf 'archive is missing: %s\n' "$archive" >&2
    exit 1
}
command -v makepkg >/dev/null || {
    printf 'makepkg is required\n' >&2
    exit 1
}
command -v sha256sum >/dev/null || {
    printf 'sha256sum is required\n' >&2
    exit 1
}

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
template="$script_dir/../packaging/arch/PKGBUILD.in"
[[ -f $template ]] || {
    printf 'missing package template: %s\n' "$template" >&2
    exit 1
}

build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
mkdir -p "$build_dir/package"
tar -xzf "$archive" -C "$build_dir"
binary=$(find "$build_dir" -type f -name veto -perm -u+x -print -quit)
[[ -n $binary ]] || {
    printf 'archive does not contain an executable veto binary\n' >&2
    exit 1
}
install -m 0755 "$binary" "$build_dir/package/veto"

binary_sha256=$(sha256sum "$build_dir/package/veto" | awk '{print $1}')
sed \
    -e "s/@VERSION@/$version/g" \
    -e "s/@BINARY_SHA256@/$binary_sha256/g" \
    "$template" >"$build_dir/package/PKGBUILD"

(cd "$build_dir/package" && makepkg --nodeps --noconfirm --clean --cleanbuild)
mkdir -p "$output_dir"
package_file=$(find "$build_dir/package" -maxdepth 1 -type f -name "veto-bin-${version}-1-*.pkg.tar.zst" -print -quit)
[[ -n $package_file ]] || {
    printf 'makepkg did not produce a package\n' >&2
    exit 1
}
install -m 0644 "$package_file" "$output_dir/"
printf '%s\n' "$output_dir/$(basename "$package_file")"
