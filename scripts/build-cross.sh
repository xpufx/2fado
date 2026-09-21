#!/usr/bin/env bash
# scripts/build-cross.sh — Cross-compile 2fado companion binary for supported platforms.
#
# Supported target platforms:
#   - linux/amd64
#   - linux/arm64
#   - darwin/amd64
#   - darwin/arm64
#
# Output:
#   Binaries and release archives are placed in dist/ (or $DIST_DIR),
#   strictly untracked and gitignored.
#
# Usage:
#   scripts/build-cross.sh [--package] [--clean] [--dist-dir <dir>] [--version <ver>]
#

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-$REPO_ROOT/dist}"
DEFAULT_PLATFORMS=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64")
PLATFORMS=("${PLATFORMS[@]:-${DEFAULT_PLATFORMS[@]}}")

VERSION="${VERSION:-0.1.0-dev}"
GIT_COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

PACKAGE=0
CLEAN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --package|-p)
      PACKAGE=1
      shift
      ;;
    --clean)
      CLEAN=1
      shift
      ;;
    --dist-dir)
      DIST_DIR="$2"
      shift 2
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    --help|-h)
      echo "Usage: $0 [--package] [--clean] [--dist-dir <dir>] [--version <ver>]"
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if [[ "$CLEAN" -eq 1 ]]; then
  echo "==> Cleaning $DIST_DIR..."
  rm -rf "$DIST_DIR"
fi

mkdir -p "$DIST_DIR"

LDFLAGS="-X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME} -X main.Version=${VERSION}"

echo "==> Cross-compiling 2fado (version: ${VERSION}, commit: ${GIT_COMMIT})..."
for pair in "${PLATFORMS[@]}"; do
  os="${pair%/*}"
  arch="${pair#*/}"
  bin_name="2fado-${os}-${arch}"
  out_path="${DIST_DIR}/${bin_name}"

  echo "    -> Building ${os}/${arch} -> ${bin_name}"
  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" \
    go build -C "${REPO_ROOT}/go" \
      -ldflags "${LDFLAGS}" \
      -o "${out_path}" \
      ./cmd/2fado
  chmod 0755 "${out_path}"
done

if [[ "$PACKAGE" -eq 1 ]]; then
  echo "==> Packaging release archives into $DIST_DIR..."
  TMP_STAGE="$(mktemp -d)"
  trap 'rm -rf "$TMP_STAGE"' EXIT

  for pair in "${PLATFORMS[@]}"; do
    os="${pair%/*}"
    arch="${pair#*/}"
    bin_name="2fado-${os}-${arch}"
    archive_name="2fado-${VERSION}-${os}-${arch}.tar.gz"

    stage_dir="${TMP_STAGE}/${os}-${arch}"
    mkdir -p "${stage_dir}"
    cp "${DIST_DIR}/${bin_name}" "${stage_dir}/2fado"
    cp "${REPO_ROOT}/LICENSE" "${stage_dir}/" 2>/dev/null || true
    cp "${REPO_ROOT}/README.md" "${stage_dir}/" 2>/dev/null || true

    tar -czf "${DIST_DIR}/${archive_name}" -C "${stage_dir}" 2fado LICENSE README.md
    echo "    -> Packaged ${archive_name}"
  done

  echo "==> Generating SHA256SUMS..."
  (
    cd "$DIST_DIR"
    rm -f SHA256SUMS
    sha256sum 2fado-* > SHA256SUMS
  )
  echo "    -> Wrote ${DIST_DIR}/SHA256SUMS"
fi

echo "==> Build complete in $DIST_DIR"
