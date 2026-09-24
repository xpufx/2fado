#!/usr/bin/env bash
# scripts/publish-github-release.sh — Mirror release artifacts to GitHub Releases.
#
# Accepts:
#   - target repo (default: xpufx/2fado)
#   - release version/tag
#   - artifact directory (default: dist/)
#
# Environment variables honored:
#   - GH_RELEASE_TOKEN or GITHUB_TOKEN (required for authentication)
#   - GITHUB_REPO (fallback for target repo)
#   - RELEASE_TAG / VERSION / BUILD_VERSION / GITHUB_REF_NAME (fallback for tag)
#   - DIST_DIR (fallback for artifact directory)
#
# Usage:
#   scripts/publish-github-release.sh [--repo <repo>] [--tag <tag>] [--dist-dir <dir>]
#   scripts/publish-github-release.sh [target_repo] [release_tag] [artifact_dir]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TARGET_REPO=""
RELEASE_TAG=""
ARTIFACT_DIR=""
POSITIONAL=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo|-r)
      TARGET_REPO="$2"
      shift 2
      ;;
    --tag|-t|--version|-v)
      RELEASE_TAG="$2"
      shift 2
      ;;
    --dist-dir|-d)
      ARTIFACT_DIR="$2"
      shift 2
      ;;
    -h|--help)
      echo "Usage: $0 [options] [target_repo] [release_tag] [artifact_dir]"
      echo ""
      echo "Options:"
      echo "  -r, --repo <repo>        Target GitHub repository (default: xpufx/2fado or \$GITHUB_REPO)"
      echo "  -t, --tag <tag>          Release version or tag"
      echo "  -d, --dist-dir <dir>     Artifact directory (default: dist/ or \$DIST_DIR)"
      echo "  -h, --help               Show this help message"
      exit 0
      ;;
    *)
      POSITIONAL+=("$1")
      shift
      ;;
  esac
done

if [ ${#POSITIONAL[@]} -eq 1 ]; then
  if [[ "${POSITIONAL[0]}" == *"/"* ]]; then
    [ -z "$TARGET_REPO" ] && TARGET_REPO="${POSITIONAL[0]}"
  else
    [ -z "$RELEASE_TAG" ] && RELEASE_TAG="${POSITIONAL[0]}"
  fi
elif [ ${#POSITIONAL[@]} -eq 2 ]; then
  if [[ "${POSITIONAL[0]}" == *"/"* ]]; then
    [ -z "$TARGET_REPO" ] && TARGET_REPO="${POSITIONAL[0]}"
    [ -z "$RELEASE_TAG" ] && RELEASE_TAG="${POSITIONAL[1]}"
  else
    [ -z "$RELEASE_TAG" ] && RELEASE_TAG="${POSITIONAL[0]}"
    [ -z "$ARTIFACT_DIR" ] && ARTIFACT_DIR="${POSITIONAL[1]}"
  fi
elif [ ${#POSITIONAL[@]} -ge 3 ]; then
  [ -z "$TARGET_REPO" ] && TARGET_REPO="${POSITIONAL[0]}"
  [ -z "$RELEASE_TAG" ] && RELEASE_TAG="${POSITIONAL[1]}"
  [ -z "$ARTIFACT_DIR" ] && ARTIFACT_DIR="${POSITIONAL[2]}"
fi

TARGET_REPO="${TARGET_REPO:-${GITHUB_REPO:-xpufx/2fado}}"
ARTIFACT_DIR="${ARTIFACT_DIR:-${DIST_DIR:-dist}}"

# Check for authentication token before proceeding
TOKEN="${GH_RELEASE_TOKEN:-${GITHUB_TOKEN:-}}"
if [ -z "$TOKEN" ]; then
  echo "::warning::Neither GH_RELEASE_TOKEN nor GITHUB_TOKEN is set; skipping GitHub release mirror."
  exit 0
fi

# Check for gh CLI
if ! command -v gh >/dev/null 2>&1; then
  echo "::warning::gh CLI not found in PATH; skipping GitHub release mirror."
  exit 0
fi

# Fallback resolution for release tag
if [ -z "$RELEASE_TAG" ]; then
  if [ -n "${VERSION:-}" ]; then
    RELEASE_TAG="$VERSION"
  elif [ -n "${BUILD_VERSION:-}" ]; then
    RELEASE_TAG="$BUILD_VERSION"
  elif [ -n "${GITHUB_REF_NAME:-}" ] && [ "${GITHUB_REF_NAME}" != "main" ]; then
    RELEASE_TAG="$GITHUB_REF_NAME"
  fi
fi

# Ensure tag has standard 'v' prefix if purely numeric/semver
if [[ -n "$RELEASE_TAG" && "$RELEASE_TAG" =~ ^[0-9] ]]; then
  RELEASE_TAG="v${RELEASE_TAG}"
fi

if [ -z "$RELEASE_TAG" ]; then
  echo "::error::Release version or tag must be specified." >&2
  exit 1
fi

# Resolve artifact directory relative to repo root if needed
if [ ! -d "$ARTIFACT_DIR" ] && [ -d "$REPO_ROOT/$ARTIFACT_DIR" ]; then
  ARTIFACT_DIR="$REPO_ROOT/$ARTIFACT_DIR"
fi

if [ ! -d "$ARTIFACT_DIR" ]; then
  echo "::error::Artifact directory '$ARTIFACT_DIR' does not exist." >&2
  exit 1
fi

export GH_TOKEN="$TOKEN"

shopt -s nullglob
FILES=("$ARTIFACT_DIR"/2fado-*.tar.gz)
if [ -f "$ARTIFACT_DIR/SHA256SUMS" ]; then
  FILES+=("$ARTIFACT_DIR/SHA256SUMS")
fi
shopt -u nullglob

if [ ${#FILES[@]} -eq 0 ]; then
  echo "::error::No release artifacts (2fado-*.tar.gz, SHA256SUMS) found in $ARTIFACT_DIR." >&2
  exit 1
fi

echo "==> Mirroring release artifacts to GitHub (${TARGET_REPO}) for tag ${RELEASE_TAG}..."
echo "    Found ${#FILES[@]} artifact(s) in ${ARTIFACT_DIR}:"
for f in "${FILES[@]}"; do
  echo "    - $(basename "$f") ($(wc -c < "$f" | tr -d ' ') bytes)"
done

if gh release view "$RELEASE_TAG" --repo "$TARGET_REPO" >/dev/null 2>&1; then
  echo "==> Release ${RELEASE_TAG} exists in ${TARGET_REPO}; uploading assets..."
  gh release upload "$RELEASE_TAG" "${FILES[@]}" --repo "$TARGET_REPO" --clobber
else
  echo "==> Creating release ${RELEASE_TAG} in ${TARGET_REPO} and uploading assets..."
  if ! gh release create "$RELEASE_TAG" "${FILES[@]}" --repo "$TARGET_REPO" --title "$RELEASE_TAG" --generate-notes; then
    echo "==> Retrying release creation with fallback title/notes..."
    if ! gh release create "$RELEASE_TAG" "${FILES[@]}" --repo "$TARGET_REPO" --title "$RELEASE_TAG" --notes "Release $RELEASE_TAG"; then
      echo "==> Release creation failed or already exists; trying gh release upload..."
      gh release upload "$RELEASE_TAG" "${FILES[@]}" --repo "$TARGET_REPO" --clobber
    fi
  fi
fi

echo "==> Successfully mirrored release artifacts to https://github.com/${TARGET_REPO}/releases/tag/${RELEASE_TAG}"
