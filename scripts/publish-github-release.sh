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

# Check for CLI tool or curl
if ! command -v gh >/dev/null 2>&1 && ! command -v curl >/dev/null 2>&1; then
  echo "::warning::Neither gh CLI nor curl found in PATH; skipping GitHub release mirror."
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

if command -v gh >/dev/null 2>&1; then
  export GH_TOKEN="$TOKEN"
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
elif command -v curl >/dev/null 2>&1; then
  echo "==> gh CLI not found; falling back to GitHub REST API via curl..."
  AUTH_HEADER="Authorization: Bearer ${TOKEN}"
  API_HEADER="Accept: application/vnd.github+json"
  API_VERSION_HEADER="X-GitHub-Api-Version: 2022-11-28"

  # Query for existing release by tag
  RELEASE_RESP=$(curl -s -H "$AUTH_HEADER" -H "$API_HEADER" -H "$API_VERSION_HEADER" "https://api.github.com/repos/${TARGET_REPO}/releases/tags/${RELEASE_TAG}")
  RELEASE_ID=""
  if command -v jq >/dev/null 2>&1; then
    RELEASE_ID=$(echo "$RELEASE_RESP" | jq -r '.id // empty' 2>/dev/null || true)
  elif command -v python3 >/dev/null 2>&1; then
    RELEASE_ID=$(python3 -c "import json, sys; d=json.loads(sys.argv[1]); print(d.get('id', ''))" "$RELEASE_RESP" 2>/dev/null || true)
  fi

  if [ -z "$RELEASE_ID" ] || [ "$RELEASE_ID" = "null" ]; then
    echo "==> Release ${RELEASE_TAG} not found in ${TARGET_REPO}; creating..."
    PAYLOAD=$(printf '{"tag_name":"%s","name":"%s","generate_release_notes":true}' "$RELEASE_TAG" "$RELEASE_TAG")
    CREATE_RESP=$(curl -s -X POST -H "$AUTH_HEADER" -H "$API_HEADER" -H "$API_VERSION_HEADER" -H "Content-Type: application/json" -d "$PAYLOAD" "https://api.github.com/repos/${TARGET_REPO}/releases")
    if command -v jq >/dev/null 2>&1; then
      RELEASE_ID=$(echo "$CREATE_RESP" | jq -r '.id // empty' 2>/dev/null || true)
    elif command -v python3 >/dev/null 2>&1; then
      RELEASE_ID=$(python3 -c "import json, sys; d=json.loads(sys.argv[1]); print(d.get('id', ''))" "$CREATE_RESP" 2>/dev/null || true)
    fi
  fi

  if [ -z "$RELEASE_ID" ] || [ "$RELEASE_ID" = "null" ]; then
    echo "::error::Failed to find or create GitHub release ${RELEASE_TAG} for ${TARGET_REPO}." >&2
    exit 1
  fi

  echo "==> Uploading assets to release ID ${RELEASE_ID}..."
  ASSETS_RESP=$(curl -s -H "$AUTH_HEADER" -H "$API_HEADER" -H "$API_VERSION_HEADER" "https://api.github.com/repos/${TARGET_REPO}/releases/${RELEASE_ID}/assets")

  for f in "${FILES[@]}"; do
    FNAME="$(basename "$f")"
    EXISTING_ID=""
    if command -v jq >/dev/null 2>&1; then
      EXISTING_ID=$(echo "$ASSETS_RESP" | jq -r ".[] | select(.name==\"$FNAME\") | .id" 2>/dev/null || true)
    elif command -v python3 >/dev/null 2>&1; then
      EXISTING_ID=$(python3 -c "import json, sys; assets=json.loads(sys.argv[1]); [print(a['id']) for a in assets if a.get('name')==sys.argv[2]]" "$ASSETS_RESP" "$FNAME" 2>/dev/null || true)
    fi

    if [ -n "$EXISTING_ID" ] && [ "$EXISTING_ID" != "null" ]; then
      echo "    Overwriting existing asset ${FNAME} (id ${EXISTING_ID})..."
      curl -s -X DELETE -H "$AUTH_HEADER" -H "$API_HEADER" -H "$API_VERSION_HEADER" "https://api.github.com/repos/${TARGET_REPO}/releases/assets/${EXISTING_ID}" >/dev/null 2>&1 || true
    fi

    echo "    Uploading ${FNAME}..."
    UPLOAD_URL="https://uploads.github.com/repos/${TARGET_REPO}/releases/${RELEASE_ID}/assets?name=${FNAME}"
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
      -H "$AUTH_HEADER" \
      -H "$API_VERSION_HEADER" \
      -H "Content-Type: application/octet-stream" \
      --data-binary "@$f" \
      "$UPLOAD_URL")
    if [ "$HTTP_CODE" -lt 200 ] || [ "$HTTP_CODE" -ge 300 ]; then
      echo "::error::Failed to upload ${FNAME} to GitHub release (HTTP ${HTTP_CODE})" >&2
      exit 1
    fi
    echo "    Uploaded ${FNAME} successfully."
  done
fi

echo "==> Successfully mirrored release artifacts to https://github.com/${TARGET_REPO}/releases/tag/${RELEASE_TAG}"
