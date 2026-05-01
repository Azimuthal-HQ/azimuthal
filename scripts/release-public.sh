#!/usr/bin/env bash
# release-public.sh — push a clean, stripped version to the public repo
#
# Branches from current HEAD, strips internal-only files, and pushes to the
# public repo. History accumulates normally on the public repo — each release
# adds one commit on top of the previous.
#
# What it strips (internal-only files not suitable for public OSS):
#   .gitleaks.toml                     — internal secret-scan config with allowlists
#   CLAUDE.md                          — agent instructions
#   current-agent-progress/            — agent progress tracking
#   docs/agent-briefs.md               — internal agent task specs
#   docs/github-setup-checklist.md     — internal setup checklist
#   docs/project-state.md              — internal project state
#   docs/regression-test-checklist.md  — internal regression tracker
#   scripts/local-test.sh              — dev-only DB wipe script
#   scripts/push-private.sh            — references private repo
#   scripts/release-public.sh          — this script itself
#   scripts/remote-test-host-deploy.sh — contains server IP + credentials
#
# Usage:
#   bash scripts/release-public.sh            → pushes to public repo main
#   bash scripts/release-public.sh --dry-run  → shows what would change, no push

set -euo pipefail

cd "$(dirname "$0")/.."

DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

TEMP_BRANCH="release/public-$(date +%Y%m%d-%H%M%S)"
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

STRIP_FILES=(
  ".gitleaks.toml"
  "CLAUDE.md"
  "docs/agent-briefs.md"
  "docs/github-setup-checklist.md"
  "docs/project-state.md"
  "docs/regression-test-checklist.md"
  "scripts/local-test.sh"
  "scripts/push-private.sh"
  "scripts/release-public.sh"
  "scripts/remote-test-host-deploy.sh"
)

STRIP_DIRS=(
  "current-agent-progress"
)

echo ""
echo "=== Public release prep ==="
echo "  Source branch : $CURRENT_BRANCH"
echo "  Temp branch   : $TEMP_BRANCH"
echo "  Target        : git@github.com:Azimuthal-HQ/azimuthal.git main"
echo ""
echo "  Stripping:"
for f in "${STRIP_FILES[@]}"; do echo "    - $f"; done
for d in "${STRIP_DIRS[@]}"; do echo "    - $d/"; done
echo ""

if [[ $DRY_RUN -eq 1 ]]; then
  echo "  (dry-run — no changes made)"
  exit 0
fi

cleanup() {
  git checkout "$CURRENT_BRANCH" 2>/dev/null || true
  git branch -D "$TEMP_BRANCH" 2>/dev/null || true
}
trap cleanup EXIT

# Fetch current public main so we can build on top of it
git fetch git@github.com:Azimuthal-HQ/azimuthal.git main:refs/remotes/public/main 2>/dev/null || true

# Branch from public/main if it exists, otherwise from current HEAD
if git rev-parse --verify refs/remotes/public/main >/dev/null 2>&1; then
  git checkout -b "$TEMP_BRANCH" refs/remotes/public/main
  # Bring in all files from current private HEAD
  git checkout "$CURRENT_BRANCH" -- .
else
  git checkout -b "$TEMP_BRANCH"
fi

# Remove internal files from the index (keeps them on disk)
for f in "${STRIP_FILES[@]}"; do
  git rm --cached "$f" 2>/dev/null || true
done
for d in "${STRIP_DIRS[@]}"; do
  git rm -r --cached "$d" 2>/dev/null || true
done

# Ensure strip list is in .gitignore
for f in "${STRIP_FILES[@]}"; do
  grep -qxF "$f" .gitignore 2>/dev/null || echo "$f" >> .gitignore
done
for d in "${STRIP_DIRS[@]}"; do
  grep -qxF "$d/" .gitignore 2>/dev/null || echo "$d/" >> .gitignore
done

git add -A
git commit -m "chore: public release — internal files stripped

Removed: .gitleaks.toml, CLAUDE.md, current-agent-progress/,
docs/agent-briefs.md, docs/github-setup-checklist.md,
docs/project-state.md, docs/regression-test-checklist.md,
scripts/local-test.sh, scripts/push-private.sh,
scripts/release-public.sh, scripts/remote-test-host-deploy.sh"

echo "  Pushing $TEMP_BRANCH → public main..."
git push git@github.com:Azimuthal-HQ/azimuthal.git "$TEMP_BRANCH:main"

echo ""
echo "=== Done — public repo updated ==="
