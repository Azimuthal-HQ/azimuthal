#!/usr/bin/env bash
# remote-test-host-deploy.sh — build locally, scp binary, swap container on server
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
BINARY=/tmp/azimuthal-linux

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
echo "=== 1/4  Building locally ==="
cd web && node_modules/.bin/vite build && cd ..
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags="-s -w -X main.Version=$(git rev-parse --short HEAD) -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o "$BINARY" \
  ./cmd/server
echo "Binary: $BINARY ($(du -sh $BINARY | cut -f1))"

echo ""
echo "=== 2/4  Pushing to private repo ==="
git push private main

echo ""
echo "=== 3/4  Copying binary to server ==="
scp -o StrictHostKeyChecking=no "$BINARY" "$REMOTE_HOST:/tmp/azimuthal-new"

echo ""
echo "=== 4/4  Swapping app container ==="
ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<'EOF'
  set -e

  # Build a minimal image around the pre-compiled binary (takes ~1s)
  mkdir -p /tmp/azimuthal-deploy
  cp /tmp/azimuthal-new /tmp/azimuthal-deploy/azimuthal
  cat > /tmp/azimuthal-deploy/Dockerfile <<'DOCKERFILE'
FROM gcr.io/distroless/static:nonroot
COPY azimuthal /azimuthal
ENTRYPOINT ["/azimuthal"]
DOCKERFILE

  docker build -t azimuthal:local /tmp/azimuthal-deploy
  rm -rf /tmp/azimuthal-deploy /tmp/azimuthal-new

  docker stop azimuthal-app-1 2>/dev/null || true
  docker rm   azimuthal-app-1 2>/dev/null || true

  docker run -d \
    --name azimuthal-app-1 \
    --network azimuthal_default \
    --restart unless-stopped \
    -p 8080:8080 \
    --env-file /root/azimuthal-env \
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
