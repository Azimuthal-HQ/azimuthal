#!/usr/bin/env bash
# release-public.sh — push a clean, stripped version to the public repo
#
# Uses an orphan branch so the public repo never accumulates history
# containing internal-only files. Each release is a single fresh commit.
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

ORPHAN_BRANCH="release/public-$(date +%Y%m%d-%H%M%S)"
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
echo "  Orphan branch : $ORPHAN_BRANCH"
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
  git branch -D "$ORPHAN_BRANCH" 2>/dev/null || true
}
trap cleanup EXIT

# Create an orphan branch — no history, clean slate
git checkout --orphan "$ORPHAN_BRANCH"

# Remove internal files from the index (keeps them on disk)
for f in "${STRIP_FILES[@]}"; do
  git rm --cached "$f" 2>/dev/null || true
done
for d in "${STRIP_DIRS[@]}"; do
  git rm -r --cached "$d" 2>/dev/null || true
done

# Append strip list to .gitignore so they don't reappear
{
  echo ""
  echo "# === public-release strips ==="
  for f in "${STRIP_FILES[@]}"; do echo "$f"; done
  for d in "${STRIP_DIRS[@]}"; do echo "$d/"; done
} >> .gitignore

git add .gitignore
git commit -m "chore: public release — internal files stripped

Removed: .gitleaks.toml, CLAUDE.md, current-agent-progress/,
docs/agent-briefs.md, docs/github-setup-checklist.md,
docs/project-state.md, docs/regression-test-checklist.md,
scripts/local-test.sh, scripts/push-private.sh,
scripts/release-public.sh, scripts/remote-test-host-deploy.sh"

echo "  Pushing $ORPHAN_BRANCH → public main (force, orphan)..."
git push git@github.com:Azimuthal-HQ/azimuthal.git "$ORPHAN_BRANCH:main" --force

echo ""
echo "=== Done — public repo updated (fresh history, no internal files) ==="
