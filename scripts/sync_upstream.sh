#!/usr/bin/env bash
# Sync GenaClaw with the upstream PicoClaw repository.
#
#   bash scripts/sync_upstream.sh --dry-run   # show what would come in, change nothing
#   bash scripts/sync_upstream.sh             # merge upstream/main and push the branch
#
# Protected paths (ours, never overwritten by upstream):
#   README.md, README.ru.md, assets/genaclaw.svg, assets/genaclaw-512.png,
#   .github/workflows/upstream-sync.yml, scripts/sync_upstream.sh
#
# On a conflict outside the protected set the merge is aborted and nothing is
# pushed — a human resolves it on a branch.
set -euo pipefail

REMOTE="upstream"
REMOTE_URL="https://github.com/sipeed/picoclaw.git"
TARGET="main"
PROTECTED=(
  "README.md"
  "README.ru.md"
  "assets/genaclaw.svg"
  "assets/genaclaw-512.png"
  ".github/workflows/upstream-sync.yml"
  "scripts/sync_upstream.sh"
)

DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help)
      sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

log() { printf '[sync] %s\n' "$*"; }
fail() { printf '[sync] ERROR: %s\n' "$*" >&2; exit 1; }

command -v git >/dev/null || fail "git not found"
git rev-parse --git-dir >/dev/null 2>&1 || fail "not a git repository"
[ -z "$(git status --porcelain)" ] || fail "working tree is dirty — commit or stash first"

if ! git remote get-url "$REMOTE" >/dev/null 2>&1; then
  log "adding remote $REMOTE -> $REMOTE_URL"
  git remote add "$REMOTE" "$REMOTE_URL"
fi

log "fetching $REMOTE"
git fetch --prune "$REMOTE" "+refs/heads/*:refs/remotes/$REMOTE/*"

BASE=$(git merge-base HEAD "$REMOTE/$TARGET")
UPSTREAM_HEAD=$(git rev-parse "$REMOTE/$TARGET")
BEHIND=$(git rev-list --count "$BASE..$UPSTREAM_HEAD")
AHEAD=$(git rev-list --count "$BASE..HEAD")
log "behind upstream: $BEHIND commit(s); local-only: $AHEAD commit(s)"

if [ "$BEHIND" -eq 0 ]; then
  log "already up to date with $REMOTE/$TARGET"
  exit 0
fi

git log --oneline --no-decorate "$BASE..$UPSTREAM_HEAD" | sed 's/^/  /'

if [ "$DRY_RUN" -eq 1 ]; then
  log "dry run: nothing merged, nothing pushed"
  exit 0
fi

STAMP=$(date +%Y%m%d)
BRANCH="sync/upstream-$STAMP"
git rev-parse --verify "$BRANCH" >/dev/null 2>&1 && BRANCH="$BRANCH-$(date +%H%M)"
git switch -c "$BRANCH"

log "merging $REMOTE/$TARGET into $BRANCH"
if git merge --no-ff --no-commit "$REMOTE/$TARGET" >/dev/null 2>&1; then
  git commit -m "sync: upstream $REMOTE/$TARGET @ ${UPSTREAM_HEAD:0:8}"
else
  CONFLICTS=$(git diff --name-only --diff-filter=U)
  UNRESOLVED=""
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    keep=0
    for protected in "${PROTECTED[@]}"; do
      [ "$path" = "$protected" ] && keep=1
    done
    if [ "$keep" -eq 1 ]; then
      log "keeping ours: $path"
      git checkout --ours -- "$path"
      git add -- "$path"
    else
      UNRESOLVED="$UNRESOLVED $path"
    fi
  done <<< "$CONFLICTS"

  if [ -n "$UNRESOLVED" ]; then
    git merge --abort || true
    git switch "$TARGET"
    git branch -D "$BRANCH" >/dev/null 2>&1 || true
    fail "conflicts need a human:$UNRESOLVED (merge aborted, nothing pushed)"
  fi

  git commit -m "sync: upstream $REMOTE/$TARGET @ ${UPSTREAM_HEAD:0:8} (protected paths kept)"
fi

log "merged on $BRANCH; pushing to origin"
git push -u origin "$BRANCH"
log "done. Open a PR into $TARGET, or merge locally: git switch $TARGET && git merge --ff-only $BRANCH && git push origin $TARGET"
