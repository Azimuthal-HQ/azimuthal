#!/usr/bin/env bash
# remote-test-host-deploy.sh — push to private repo, pull on server, build + restart via docker compose
#
# First-time server setup (run once):
#   bash scripts/remote-test-host-deploy.sh --setup
#   Then add the printed deploy key to:
#   https://github.com/Azimuthal-HQ/azimuthal-private/settings/keys
#
# Usage:
#   bash scripts/remote-test-host-deploy.sh

set -euo pipefail

cd "$(dirname "$0")/.."

REMOTE_HOST="root@159.223.190.255"
REMOTE_DIR="/root/azimuthal"
REPO="git@github.com:Azimuthal-HQ/azimuthal-private.git"

# ── First-time setup ──────────────────────────────────────────────────────────
if [[ "${1:-}" == "--setup" ]]; then
  echo ""
  echo "=== Server setup ==="
  ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<EOF
    set -e

    # Generate deploy key if not already present
    if [ ! -f ~/.ssh/azimuthal_deploy ]; then
      ssh-keygen -t ed25519 -C "azimuthal-deploy" -f ~/.ssh/azimuthal_deploy -N ""
      echo ""
      echo "Host github.com" >> ~/.ssh/config
      echo "  IdentityFile ~/.ssh/azimuthal_deploy" >> ~/.ssh/config
      echo "  StrictHostKeyChecking no" >> ~/.ssh/config
    fi

    echo ""
    echo "=== Add this deploy key to GitHub ==="
    echo "https://github.com/Azimuthal-HQ/azimuthal-private/settings/keys"
    echo ""
    cat ~/.ssh/azimuthal_deploy.pub
    echo ""
EOF
  echo "Once the key is added to GitHub, run without --setup to deploy."
  exit 0
fi

# ── Deploy ────────────────────────────────────────────────────────────────────

echo ""
echo "=== 1/3  Pushing to private repo ==="
git push private main

echo ""
echo "=== 2/3  Pulling on server ==="
ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<EOF
  set -e

  if [ ! -d "$REMOTE_DIR/.git" ]; then
    echo "--- Cloning repo (first time) ---"
    rm -rf "$REMOTE_DIR"
    git clone "$REPO" "$REMOTE_DIR"
  else
    echo "--- Pulling latest ---"
    cd "$REMOTE_DIR"
    git pull origin main
  fi
EOF

echo ""
echo "=== 3/3  Building and restarting via docker compose ==="
ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<EOF
  set -e
  cd "$REMOTE_DIR"
  docker compose -f build/docker-compose.yml up --build -d app
  echo "--- Status ---"
  docker compose -f build/docker-compose.yml ps app
EOF

echo ""
echo "=== Deploy complete ==="
echo "  Host : http://159.223.190.255:8080"
