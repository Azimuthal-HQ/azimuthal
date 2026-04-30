#!/usr/bin/env bash
# remote-test-host-deploy.sh — push to private repo, build image on server, swap app container
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
    if [ ! -f ~/.ssh/azimuthal_deploy ]; then
      ssh-keygen -t ed25519 -C "azimuthal-deploy" -f ~/.ssh/azimuthal_deploy -N ""
      cat >> ~/.ssh/config <<CFG

Host github.com
  IdentityFile ~/.ssh/azimuthal_deploy
  StrictHostKeyChecking no
CFG
    fi
    echo ""
    echo "=== Add this deploy key to GitHub ==="
    echo "https://github.com/Azimuthal-HQ/azimuthal-private/settings/keys"
    echo ""
    cat ~/.ssh/azimuthal_deploy.pub
EOF
  echo "Once the key is added to GitHub, run without --setup to deploy."
  exit 0
fi

# ── Deploy ────────────────────────────────────────────────────────────────────

echo ""
echo "=== 1/4  Pushing to private repo ==="
git push private main

echo ""
echo "=== 2/4  Pulling latest on server ==="
ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<EOF
  set -e
  if [ ! -d "$REMOTE_DIR/.git" ]; then
    echo "--- Cloning repo (first time) ---"
    git clone "$REPO" "$REMOTE_DIR"
  else
    echo "--- Pulling latest ---"
    cd "$REMOTE_DIR" && git pull origin main
  fi
EOF

echo ""
echo "=== 3/4  Building image on server ==="
ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<EOF
  set -e
  cd "$REMOTE_DIR"
  docker build -f build/Dockerfile -t azimuthal:local .
EOF

echo ""
echo "=== 4/4  Swapping app container ==="
ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<EOF
  set -e
  # Grab current env from running container
  ENV_ARGS=\$(docker inspect azimuthal-app-1 --format '{{range .Config.Env}}-e "{{.}}" {{end}}' 2>/dev/null || echo "")
  NETWORK=\$(docker inspect azimuthal-app-1 --format '{{range \$k,\$v := .NetworkSettings.Networks}}{{\$k}}{{end}}' 2>/dev/null || echo "azimuthal_default")

  docker stop azimuthal-app-1 2>/dev/null || true
  docker rm   azimuthal-app-1 2>/dev/null || true

  docker run -d \
    --name azimuthal-app-1 \
    --network "\$NETWORK" \
    --restart unless-stopped \
    -p 8080:8080 \
    \$ENV_ARGS \
    azimuthal:local serve

  sleep 3
  docker logs azimuthal-app-1 --tail=6

  echo ""
  echo "--- Creating default admin user (skips if already exists) ---"
  docker exec azimuthal-app-1 /azimuthal admin create-user \
    --email "admin@azimuthal.dev" \
    --name "Admin" \
    --password "password123" 2>/dev/null && echo "User created." || echo "User already exists, skipping."
EOF

echo ""
echo "=== Deploy complete ==="
echo "  Host     : http://159.223.190.255:8080"
echo "  Email    : admin@azimuthal.dev"
echo "  Password : password123"
