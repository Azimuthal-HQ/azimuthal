#!/usr/bin/env bash
# release-public.sh — push a clean, agent-stripped version to the public repo
#
# What it strips (internal-only files not suitable for public OSS):
#   CLAUDE.md                          — agent instructions
#   current-agent-progress/            — agent progress tracking
#   docs/agent-briefs.md               — internal agent task specs
#   docs/github-setup-checklist.md     — internal setup checklist
#   docs/project-state.md              — internal project state
#   docs/regression-test-checklist.md  — internal regression tracker
#   scripts/release-public.sh          — this script itself
#
# Usage:
#   bash scripts/release-public.sh            → pushes to origin main
#   bash scripts/release-public.sh --dry-run  → shows what would change, no push

set -euo pipefail

cd "$(dirname "$0")/.."

DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

TEMP_BRANCH="release/public-$(date +%Y%m%d-%H%M%S)"
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)

STRIP_FILES=(
  "CLAUDE.md"
  "docs/agent-briefs.md"
  "docs/github-setup-checklist.md"
  "docs/project-state.md"
  "docs/regression-test-checklist.md"
  "scripts/release-public.sh"
)

STRIP_DIRS=(
  "current-agent-progress"
)

echo ""
echo "=== Public release prep ==="
echo "  Source branch : $CURRENT_BRANCH"
echo "  Temp branch   : $TEMP_BRANCH"
echo "  Target        : origin main (https://github.com/Azimuthal-HQ/azimuthal)"
echo ""
echo "  Stripping:"
for f in "${STRIP_FILES[@]}"; do echo "    - $f"; done
for d in "${STRIP_DIRS[@]}"; do echo "    - $d/"; done
echo ""

if [[ $DRY_RUN -eq 1 ]]; then
  echo "  (dry-run — no changes made)"
  exit 0
fi

# Create temp branch from current HEAD
git checkout -b "$TEMP_BRANCH"

# Remove internal files from the index (keeps them on disk)
for f in "${STRIP_FILES[@]}"; do
  git rm --cached "$f" 2>/dev/null || true
done
for d in "${STRIP_DIRS[@]}"; do
  git rm -r --cached "$d" 2>/dev/null || true
done

# Add them to .gitignore on the temp branch so they don't reappear
{
  echo ""
  echo "# === public-release strips ==="
  for f in "${STRIP_FILES[@]}"; do echo "$f"; done
  for d in "${STRIP_DIRS[@]}"; do echo "$d/"; done
} >> .gitignore

git add .gitignore
git commit -m "chore: strip internal agent files for public release

Removed: CLAUDE.md, current-agent-progress/, docs/agent-briefs.md,
docs/github-setup-checklist.md, docs/project-state.md,
docs/regression-test-checklist.md"

echo "  Pushing $TEMP_BRANCH → origin main..."
git push origin "$TEMP_BRANCH:main"

echo ""
echo "  Cleaning up temp branch..."
git checkout "$CURRENT_BRANCH"
git branch -D "$TEMP_BRANCH"

echo ""
echo "=== Done — public repo updated ==="
