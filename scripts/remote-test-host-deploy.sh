#!/usr/bin/env bash
# remote-test-host-deploy.sh — build image, push to ghcr.io, deploy to test host
#
# First-time setup (run once):
#   docker login ghcr.io -u <your-github-username> --password-stdin <<< <your-github-pat>
#   (PAT needs: write:packages scope)
#
# Usage:
#   bash scripts/remote-test-host-deploy.sh          # tag: latest
#   bash scripts/remote-test-host-deploy.sh v1.2.3   # custom tag

set -euo pipefail

cd "$(dirname "$0")/.."

REMOTE_HOST="root@159.223.190.255"
IMAGE="ghcr.io/azimuthal-hq/azimuthal"
TAG="${1:-latest}"

echo ""
echo "=== 1/4  Pushing to private repo ==="
git push private main

echo ""
echo "=== 2/4  Building Docker image ($IMAGE:$TAG) ==="
docker build \
  -f build/Dockerfile \
  -t "$IMAGE:$TAG" \
  --build-arg VERSION="$(git rev-parse --short HEAD)" \
  .

echo ""
echo "=== 3/4  Pushing image to ghcr.io ==="
docker push "$IMAGE:$TAG"

echo ""
echo "=== 4/4  Deploying to $REMOTE_HOST ==="
ssh -o StrictHostKeyChecking=no "$REMOTE_HOST" bash <<EOF
  set -e
  cd /root/azimuthal
  echo "--- Pulling new image ---"
  docker compose pull app
  echo "--- Restarting app ---"
  docker compose up -d app
  echo "--- Status ---"
  docker compose ps app
EOF

echo ""
echo "=== Deploy complete ==="
echo "  Image : $IMAGE:$TAG"
echo "  Host  : http://159.223.190.255:8080"
