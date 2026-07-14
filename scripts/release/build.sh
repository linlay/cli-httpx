#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/release/build.sh

Example:
  scripts/release/build.sh

This script reads the release version from the repository VERSION file,
builds release archives locally, and does not upload anything.
EOF
}

if [[ "${1:-}" == "-h" ]] || [[ "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ "$#" -ne 0 ]]; then
  usage
  exit 1
fi

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
version_file="$repo_root/VERSION"

if [[ ! -f "$version_file" ]]; then
  echo "VERSION file not found: $version_file" >&2
  exit 1
fi

version="$(<"$version_file")"
if [[ -z "$version" ]]; then
  echo "VERSION file is empty: $version_file" >&2
  exit 1
fi

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid version: $version" >&2
  echo "expected VERSION to contain a git tag style version such as v0.1.0" >&2
  exit 1
fi

dist_dir="$repo_root/dist/$version"
stage_dir="$dist_dir/.stage"
build_time="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
commit="$(git -C "$repo_root" rev-parse --short HEAD)"

mkdir -p "$dist_dir"
rm -rf "$stage_dir"
mkdir -p "$stage_dir"
trap 'rm -rf "$stage_dir"' EXIT

include_license=false
if [[ -f "$repo_root/LICENSE" ]]; then
  include_license=true
else
  echo "warning: LICENSE not found; archives will not include it" >&2
fi

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
  "windows amd64"
  "windows arm64"
)

archives=()

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  binary_name="httpx"
  archive_ext="tar.gz"
  if [[ "$goos" == "windows" ]]; then
    binary_name="httpx.exe"
    archive_ext="zip"
  fi
  archive_name="httpx_${version}_${goos}_${goarch}.${archive_ext}"
  archives+=("$archive_name")
  package_dir="$stage_dir/httpx_${version}_${goos}_${goarch}"

  rm -rf "$package_dir"
  mkdir -p "$package_dir"

  echo "building $goos/$goarch"
  env \
    CGO_ENABLED=0 \
    GOOS="$goos" \
    GOARCH="$goarch" \
    go build \
      -trimpath \
      -ldflags "-s -w -X github.com/linlay/cli-httpx/internal/buildinfo.Version=$version -X github.com/linlay/cli-httpx/internal/buildinfo.Commit=$commit -X github.com/linlay/cli-httpx/internal/buildinfo.BuildTime=$build_time" \
      -o "$package_dir/$binary_name" \
      ./cmd/httpx

  cp "$repo_root/README.md" "$package_dir/README.md"
  if [[ "$include_license" == "true" ]]; then
    cp "$repo_root/LICENSE" "$package_dir/LICENSE"
  fi

  if [[ "$archive_ext" == "zip" ]]; then
    command -v zip >/dev/null 2>&1 || {
      echo "zip is required to package Windows releases" >&2
      exit 1
    }
    rm -f "$dist_dir/$archive_name"
    (
      cd "$package_dir"
      zip -qr "$dist_dir/$archive_name" .
    )
  else
    tar -C "$package_dir" -czf "$dist_dir/$archive_name" .
  fi
done

(
  cd "$dist_dir"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${archives[@]}" > "httpx_${version}_checksums.txt"
  else
    sha256sum "${archives[@]}" > "httpx_${version}_checksums.txt"
  fi
)

echo "release artifacts written to $dist_dir"
