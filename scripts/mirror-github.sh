#!/usr/bin/env bash
# scripts/mirror-github.sh — DRAFT ONLY. DO NOT RUN.
#
# Code-only push of the 2fado tree to the private GitHub mirror.
# Modeled on the workspace mirror script in the main monorepo
# (isolated-index approach): build a pruned tree from HEAD in a temp
# index, commit it on top of the mirror's HEAD for a clean
# fast-forward, push the resulting commit. History never ships — only
# the tree. No remotes are added; when the `github` remote is absent
# we push to $GITHUB_URL directly (never `git remote add`).
#
# Operator owns repo creation (token cannot create repos). This script
# stays a draft until the operator approves the first push.
#
# Preflight model (fail closed, two gates):
#   1. no-PII gate — delegated to the platform-owned scanner, which is
#      intentionally UNTRACKED here (lives in the platform repo, never
#      mirrors). This file carries no handle list and names no handles,
#      so the draft itself can never leak or trip the gate. The scanner
#      path is configurable; missing scanner == abort. Extra patterns
#      may be appended later in the platform repo without touching this
#      tree. See issue #33 for the gate definition.
#   2. secrets audit — inline entropy/file patterns only (no handle
#      list); aborts on any hit in tree or history.
#
# Usage:
#   scripts/mirror-github.sh [--dry-run] [--force]
#
# Env:
#   GITHUB_REMOTE  remote name to prefer (default: github)
#   GITHUB_BRANCH  mirror branch (default: main)
#   GITHUB_URL     push URL (required — operator provides after creating
#                  the private repo, e.g. git@github.com:OWNER/2fado.git)
#   PII_SCANNER    platform-owned preflight executable (default:
#                  ../platform/bin/pii-preflight.sh, resolved relative
#                  to the repo root). Must be executable. Never tracked
#                  in this repo.

set -euo pipefail

DRY_RUN=0
FORCE=0
for arg in "$@"; do
	case "$arg" in
	--dry-run) DRY_RUN=1 ;;
	--force) FORCE=1 ;;
	-h | --help)
		echo "Usage: $0 [--dry-run] [--force]"
		echo "DRAFT ONLY. DO NOT RUN without operator approval."
		exit 0
		;;
	*)
		echo "unknown arg: $arg" >&2
		exit 2
		;;
	esac
done

REMOTE="${GITHUB_REMOTE:-github}"
BRANCH="${GITHUB_BRANCH:-main}"
URL="${GITHUB_URL:?set GITHUB_URL to the private mirror URL (operator provides)}"
REPO_ROOT="$(git rev-parse --show-toplevel)"
SCANNER="${PII_SCANNER:-$REPO_ROOT/../platform/bin/pii-preflight.sh}"

# --- Preflight 1: no-PII gate via platform-owned scanner (fail closed).
# The pattern list lives entirely in the platform repo. This script never
# spells it out, so the draft stays clean and the scanner itself (untracked
# here) never ships to the mirror.
if [ ! -x "$SCANNER" ]; then
	echo "[mirror-github] PII scanner missing/executable-bit unset: $SCANNER" >&2
	echo "[mirror-github] Gate lives in the platform repo (untracked here by" >&2
	echo "[mirror-github] design). Point PII_SCANNER at it or abort. Refusing to push." >&2
	exit 1
fi
"$SCANNER" --tree HEAD --allowlist LICENSE || {
	echo "[mirror-github] PII preflight FAILED — refusing to push." >&2
	exit 1
}
echo "[mirror-github] Preflight 1 ok: platform PII scanner passed."

# --- Preflight 2: secrets audit (fail closed).
if git grep -I -n -E 'ghp_|github_pat_|xox[bpas]-|AKIA|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|sk-ant-' --cached -- . ':!plugin/node_modules' | grep -v '^Binary' ||
	git log --all -G'ghp_|github_pat_|xox[bpas]-|AKIA|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|sk-ant-' --oneline -- . ':!plugin/node_modules' | grep .; then
	echo "[mirror-github] SECRET PATTERN HIT — refusing to push." >&2
	exit 1
fi
echo "[mirror-github] Preflight 2 ok: no secret patterns in tree/history."

# --- Code-only prune list: forge/agent-internal paths never mirror.
# (Issues, CI, internal links stay on the internal forge.)
PRUNE=(
	AGENTS.md
	.agents
	.forgejo
	.serena
	opencode.jsonc
	paseo.json
	scripts/forgejo-hook.mjs
	plugin/node_modules
	bin
	daemon.log
)

# --- Isolated temp index (monorepo pattern): never touches the worktree index.
TMP_INDEX="$(mktemp -u)/git-index-mirror-$$"
export GIT_INDEX_FILE="$TMP_INDEX"
trap 'rm -f "$GIT_INDEX_FILE"' EXIT

git read-tree HEAD
for p in "${PRUNE[@]}"; do
	git rm -r --cached --quiet --ignore-unmatch -- "$p" || true
done

TREE_ID="$(git write-tree)"
echo "[mirror-github] Prepared tree: $TREE_ID"

# --- Parent = mirror HEAD for a clean fast-forward.
DEST="$REMOTE"
if ! git remote get-url "$REMOTE" >/dev/null 2>&1; then
	DEST="$URL"
fi
PARENT_ARGS=()
REMOTE_HEAD="$(git ls-remote "$DEST" "refs/heads/$BRANCH" 2>/dev/null | awk '{print $1}' || true)"
if [[ "$REMOTE_HEAD" =~ ^[0-9a-f]{40}$ ]]; then
	PARENT_ARGS=(-p "$REMOTE_HEAD")
	echo "[mirror-github] Remote parent: $REMOTE_HEAD"
else
	echo "[mirror-github] No remote HEAD found; committing as root." >&2
fi

HEAD_SUBJECT="$(git log -1 --format=%s HEAD)"
COMMIT_ID="$(git commit-tree "$TREE_ID" "${PARENT_ARGS[@]}" -m "sync(code-only): $HEAD_SUBJECT")"
echo "[mirror-github] Created commit: $COMMIT_ID"

PUSH_ARGS=(push)
[ "$DRY_RUN" -eq 1 ] && PUSH_ARGS+=(--dry-run)
[ "$FORCE" -eq 1 ] && PUSH_ARGS+=(--force)
PUSH_ARGS+=("$DEST" "$COMMIT_ID:refs/heads/$BRANCH")

echo "[mirror-github] Executing: git ${PUSH_ARGS[*]}"
git "${PUSH_ARGS[@]}"
echo "[mirror-github] Done: code-only tree synced to $DEST:$BRANCH"
