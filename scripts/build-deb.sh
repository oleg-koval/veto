#!/usr/bin/env bash
set -euo pipefail

usage() {
    printf 'usage: %s VERSION ARCH BINARY OUTPUT_DIR\n' "$0" >&2
    exit 2
}

[[ $# -eq 4 ]] || usage
version=$1
arch=$2
binary=$3
output_dir=$4

[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.]+)?$ ]] || {
    printf 'invalid version: %s\n' "$version" >&2
    exit 2
}
[[ $arch == amd64 || $arch == arm64 ]] || {
    printf 'unsupported Debian architecture: %s\n' "$arch" >&2
    exit 2
}
[[ -f $binary && -x $binary ]] || {
    printf 'binary is missing or not executable: %s\n' "$binary" >&2
    exit 1
}
command -v dpkg-deb >/dev/null || {
    printf 'dpkg-deb is required\n' >&2
    exit 1
}
[[ -f packaging/debian/control.in && -f README.md ]] || {
    printf 'run this script from the repository root\n' >&2
    exit 1
}

deb_version=${version//-/~}
package_root=$(mktemp -d)
trap 'rm -rf "$package_root"' EXIT

mkdir -p "$package_root/DEBIAN" "$package_root/usr/bin" "$package_root/usr/share/doc/veto"
sed \
    -e "s/@VERSION@/$deb_version/g" \
    -e "s/@ARCH@/$arch/g" \
    packaging/debian/control.in >"$package_root/DEBIAN/control"
install -m 0755 "$binary" "$package_root/usr/bin/veto"
install -m 0644 README.md "$package_root/usr/share/doc/veto/README.md"
mkdir -p "$output_dir"
dpkg-deb --build --root-owner-group "$package_root" "$output_dir/veto_${version}_${arch}.deb" >/dev/null
printf '%s\n' "$output_dir/veto_${version}_${arch}.deb"
