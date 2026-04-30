#!/usr/bin/env bash
# local-test.sh — dev server helper
#
# Usage:
#   bash scripts/local-test.sh           → full reset (wipes DB, rebuilds everything)
#   bash scripts/local-test.sh --restart → fast restart (keep DB, rebuild + bounce server)

set -e

cd "$(dirname "$0")/.."

BINARY=/tmp/azimuthal
EMAIL="test@test.com"
NAME="Tyler"
PASSWORD="password123"

RESTART=0
[[ "${1:-}" == "--restart" ]] && RESTART=1

serve() {
  DATABASE_URL="postgres://azimuthal:dev@localhost:5432/azimuthal_dev?sslmode=disable" \
  JWT_SECRET=supersecretkey123 \
  STORAGE_ENDPOINT=http://localhost:9000 \
  STORAGE_ACCESS_KEY=minioadmin \
  STORAGE_SECRET_KEY=minioadmin \
  STORAGE_BUCKET=azimuthal \
  APP_ENV=development \
  $BINARY serve
}

if [[ $RESTART -eq 1 ]]; then
  echo ""
  echo "=== Fast restart (keeping DB) ==="

  echo ""
  echo "--- Killing existing server ---"
  pkill -f "$BINARY serve" 2>/dev/null && sleep 1 || true

  echo ""
  echo "--- Building frontend ---"
  cd web && node_modules/.bin/vite build && cd ..

  echo ""
  echo "--- Building binary ---"
  go build -o $BINARY ./cmd/server

  echo ""
  echo "  URL:      http://localhost:8080"
  echo "  Email:    $EMAIL"
  echo "  Password: $PASSWORD"
  echo ""

  serve
  exit 0
fi

# ── Full reset ────────────────────────────────────────────────────────────────

echo ""
echo "=== 1/5  Wiping database ==="
docker compose -f build/docker-compose.dev.yml down -v
docker compose -f build/docker-compose.dev.yml up -d postgres minio
echo "Waiting for postgres..."
sleep 5

echo ""
echo "=== 2/5  Building frontend ==="
cd web && node_modules/.bin/vite build
cd ..

echo ""
echo "=== 3/5  Building binary ==="
go build -o $BINARY ./cmd/server
echo "Binary: $BINARY"

echo ""
echo "=== 4/5  Creating test user ==="
DATABASE_URL="postgres://azimuthal:dev@localhost:5432/azimuthal_dev?sslmode=disable" \
JWT_SECRET=supersecretkey123 \
$BINARY admin create-user \
  --email "$EMAIL" \
  --name "$NAME" \
  --password "$PASSWORD"

echo ""
echo "=== 5/5  Starting server ==="
echo ""
echo "  URL:      http://localhost:8080"
echo "  Email:    $EMAIL"
echo "  Password: $PASSWORD"
echo ""
echo "  Also clear localStorage in your browser before logging in:"
echo "  F12 → Storage → Local Storage → trash icon"
echo ""

serve