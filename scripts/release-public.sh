#!/usr/bin/env bash
# release-public.sh — push a clean, stripped version to the public repo
#
# Branches from the current public main, overlays private main's files,
# removes internal-only files, and pushes. History accumulates normally.
#
# What it strips (internal-only files not suitable for public OSS):
#   .gitleaks.toml                     — internal secret-scan config with allowlists
#   .claude/                           — agent settings (local + project)
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
  ".claude"
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

# Fetch current public main so we build on top of its history
git fetch git@github.com:Azimuthal-HQ/azimuthal.git main:refs/remotes/public/main 2>/dev/null || true

# Branch from public/main if it exists, otherwise from current HEAD
if git rev-parse --verify refs/remotes/public/main >/dev/null 2>&1; then
  git checkout -b "$TEMP_BRANCH" refs/remotes/public/main
  # Overlay all files from private main (stages them into the index)
  git checkout "$CURRENT_BRANCH" -- .
else
  git checkout -b "$TEMP_BRANCH"
fi

# Remove internal files from index AND disk so git add -A can't re-pick them up
for f in "${STRIP_FILES[@]}"; do
  git rm -f "$f" 2>/dev/null || rm -f "$f"
done
for d in "${STRIP_DIRS[@]}"; do
  git rm -rf "$d" 2>/dev/null || rm -rf "$d"
done

# Remove any untracked build artifacts that shouldn't go public
rm -f server

# Ensure strip entries are in .gitignore
for f in "${STRIP_FILES[@]}"; do
  grep -qxF "$f" .gitignore 2>/dev/null || echo "$f" >> .gitignore
done
for d in "${STRIP_DIRS[@]}"; do
  grep -qxF "$d/" .gitignore 2>/dev/null || echo "$d/" >> .gitignore
done
grep -qxF "server" .gitignore 2>/dev/null || echo "server" >> .gitignore

git add -A
git commit -m "chore: public release — internal files stripped

Removed: .gitleaks.toml, .claude/, CLAUDE.md, current-agent-progress/,
docs/agent-briefs.md, docs/github-setup-checklist.md,
docs/project-state.md, docs/regression-test-checklist.md,
scripts/local-test.sh, scripts/push-private.sh,
scripts/release-public.sh, scripts/remote-test-host-deploy.sh"

echo "  Pushing $TEMP_BRANCH → public main..."
git push git@github.com:Azimuthal-HQ/azimuthal.git "$TEMP_BRANCH:main"

echo ""
echo "=== Done — public repo updated ==="
