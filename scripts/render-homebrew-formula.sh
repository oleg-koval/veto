#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 3 ]]; then
    printf 'usage: %s vMAJOR.MINOR.PATCH checksum-manifest output-file\n' "$0" >&2
    exit 2
fi

tag_name=$1
checksum_manifest=$2
output_file=$3

if [[ ! "${tag_name}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
    printf 'release tag must be vMAJOR.MINOR.PATCH (got %s)\n' "${tag_name}" >&2
    exit 1
fi
if [[ ! -f "${checksum_manifest}" ]]; then
    printf 'checksum manifest does not exist: %s\n' "${checksum_manifest}" >&2
    exit 1
fi

version=${tag_name#v}

read_checksum() {
    local asset=$1
    local checksum
    checksum=$(awk -v asset="${asset}" '$2 == asset {print $1}' "${checksum_manifest}")
    if [[ ! "${checksum}" =~ ^[0-9a-fA-F]{64}$ ]]; then
        printf 'checksum manifest has no unique checksum for %s\n' "${asset}" >&2
        exit 1
    fi
    printf '%s' "${checksum}" | tr '[:upper:]' '[:lower:]'
}

darwin_amd64=$(read_checksum "veto_${version}_darwin_amd64.tar.gz")
darwin_arm64=$(read_checksum "veto_${version}_darwin_arm64.tar.gz")
linux_amd64=$(read_checksum "veto_${version}_linux_amd64.tar.gz")
linux_arm64=$(read_checksum "veto_${version}_linux_arm64.tar.gz")

output_dir=$(dirname -- "${output_file}")
mkdir -p "${output_dir}"
temporary_file=$(mktemp "${output_file}.tmp.XXXXXX")
cleanup() {
    rm -f -- "${temporary_file}"
}
trap cleanup EXIT

cat >"${temporary_file}" <<EOF
# typed: false
# frozen_string_literal: true

class Veto < Formula
  desc "Cost-aware AI model router with structured admission decisions"
  homepage "https://github.com/oleg-koval/veto"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/oleg-koval/veto/releases/download/${tag_name}/veto_${version}_darwin_amd64.tar.gz"
      sha256 "${darwin_amd64}"

      define_method(:install) do
        bin.install "veto"
      end
    end

    if Hardware::CPU.arm?
      url "https://github.com/oleg-koval/veto/releases/download/${tag_name}/veto_${version}_darwin_arm64.tar.gz"
      sha256 "${darwin_arm64}"

      define_method(:install) do
        bin.install "veto"
      end
    end
  end

  on_linux do
    if Hardware::CPU.intel? && Hardware::CPU.is_64_bit?
      url "https://github.com/oleg-koval/veto/releases/download/${tag_name}/veto_${version}_linux_amd64.tar.gz"
      sha256 "${linux_amd64}"

      define_method(:install) do
        bin.install "veto"
      end
    end

    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/oleg-koval/veto/releases/download/${tag_name}/veto_${version}_linux_arm64.tar.gz"
      sha256 "${linux_arm64}"

      define_method(:install) do
        bin.install "veto"
      end
    end
  end

  def post_install
    return unless OS.mac?

    executable = bin/"veto"
    return unless quiet_system "/usr/bin/xattr", "-p", "com.apple.quarantine", executable

    executable.chmod 0755
    system "/usr/bin/xattr", "-d", "com.apple.quarantine", executable
    executable.chmod 0555
  end

  test do
    assert_match "veto #{version}", shell_output("#{bin}/veto version")
  end
end
EOF

chmod 0644 "${temporary_file}"
mv -- "${temporary_file}" "${output_file}"
trap - EXIT
