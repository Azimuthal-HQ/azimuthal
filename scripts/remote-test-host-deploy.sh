#!/usr/bin/env bash
# deploy.sh — push to private repo then pull and restart on remote server
#
# First-time server setup (run once manually):
#   ssh root@159.223.190.255
#   git clone git@github.com:Azimuthal-HQ/azimuthal-private.git /opt/azimuthal
#   cp /opt/azimuthal/.env.example /opt/azimuthal/.env  # then fill in real values
#
# Usage:
#   bash scripts/deploy.sh

set -euo pipefail

cd "$(dirname "$0")/.."

REMOTE_HOST="root@159.223.190.255"
REMOTE_DIR="/opt/azimuthal"

echo ""
echo "=== 1/3  Building frontend ==="
cd web && node_modules/.bin/vite build && cd ..

echo ""
echo "=== 2/3  Pushing to private repo ==="
git push private main

echo ""
echo "=== 3/3  Deploying to $REMOTE_HOST ==="
ssh "$REMOTE_HOST" bash <<EOF
  set -e
  cd $REMOTE_DIR

  echo "--- Pulling latest ---"
  git pull origin main

  echo "--- Building binary ---"
  go build -o /usr/local/bin/azimuthal ./cmd/server

  echo "--- Restarting service ---"
  if systemctl is-active --quiet azimuthal; then
    systemctl restart azimuthal
  else
    echo "  (no systemd service found — binary updated, restart manually)"
  fi

  echo "--- Done ---"
EOF

echo ""
echo "=== Deploy complete ==="
echo "  Host: $REMOTE_HOST"
echo "  URL:  http://159.223.190.255:8080"
