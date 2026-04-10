#!/usr/bin/env bash
# verify-api.sh — Full API verification suite for Azimuthal
#
# Usage:
#   DATABASE_URL=... JWT_SECRET=... ./scripts/verify-api.sh
#
# Runs Steps 4-7 from the Testing Requirements in CLAUDE.md.
# Expects the server to NOT be running — this script builds, starts,
# tests, and stops it automatically.

set -euo pipefail

FAILURES=0
SERVER_PID=""

cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    echo ""
    echo "=== Step 8 — Clean up ==="
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    echo "Server stopped."
  fi
  rm -f /tmp/azimuthal-test
}
trap cleanup EXIT

check() {
  local label="$1"
  shift
  if "$@"; then
    echo "  $label"
  else
    echo "  $label"
    FAILURES=$((FAILURES + 1))
  fi
}

# ── Defaults ─────────────────────────────────────────────────
: "${DATABASE_URL:=postgres://azimuthal:dev@localhost:5432/azimuthal_dev?sslmode=disable}"
: "${JWT_SECRET:=test-secret-for-local-testing-only}"
: "${STORAGE_ENDPOINT:=http://localhost:9000}"
: "${STORAGE_ACCESS_KEY:=minioadmin}"
: "${STORAGE_SECRET_KEY:=minioadmin}"
: "${STORAGE_BUCKET:=azimuthal}"
: "${APP_ENV:=development}"
: "${APP_PORT:=8080}"
BASE_URL="http://localhost:${APP_PORT}"

# ── Step 3 — Build and start the binary ──────────────────────
echo "=== Step 3 — Start the binary ==="
go build -o /tmp/azimuthal-test ./cmd/server

DATABASE_URL="$DATABASE_URL" \
JWT_SECRET="$JWT_SECRET" \
STORAGE_ENDPOINT="$STORAGE_ENDPOINT" \
STORAGE_ACCESS_KEY="$STORAGE_ACCESS_KEY" \
STORAGE_SECRET_KEY="$STORAGE_SECRET_KEY" \
STORAGE_BUCKET="$STORAGE_BUCKET" \
APP_ENV="$APP_ENV" \
/tmp/azimuthal-test serve &
SERVER_PID=$!
sleep 2

if ! kill -0 "$SERVER_PID" 2>/dev/null; then
  echo "Server failed to start."
  exit 1
fi
echo "Server running (PID $SERVER_PID)."

# ── Step 4 — Create a test user and get a JWT ────────────────
echo ""
echo "=== Step 4 — Create test user and get JWT ==="
/tmp/azimuthal-test admin create-user \
  --email test@azimuthal.dev \
  --name "Test User" \
  --password testpassword123 2>/dev/null || true

TOKEN=$(curl -s -X POST "${BASE_URL}/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"test@azimuthal.dev","password":"testpassword123"}' \
  | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -z "$TOKEN" ]; then
  echo "Failed to obtain JWT token."
  FAILURES=$((FAILURES + 1))
  exit 1
fi
echo "Got token: ${TOKEN:0:20}..."

# ── Step 5 — Get org ID ─────────────────────────────────────
echo ""
echo "=== Step 5 — Get org ID ==="
ORG_ID=$(curl -s "${BASE_URL}/api/v1/me" \
  -H "Authorization: Bearer $TOKEN" \
  | grep -o '"org_id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$ORG_ID" ]; then
  echo "Failed to obtain org ID."
  FAILURES=$((FAILURES + 1))
  exit 1
fi
echo "Org ID: $ORG_ID"

# ── Step 6 — Test create operations (minimum fields) ────────
echo ""
echo "=== Step 6 — Test create operations (minimum fields) ==="

# Service desk space
SPACE=$(curl -s -X POST \
  "${BASE_URL}/api/v1/orgs/$ORG_ID/spaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Desk","type":"service_desk","slug":"test-desk"}')
SPACE_ID=$(echo "$SPACE" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "Space ID: $SPACE_ID"

# Ticket with minimum fields
TICKET_RESULT=$(curl -s -X POST \
  "${BASE_URL}/api/v1/orgs/$ORG_ID/spaces/$SPACE_ID/items" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Test ticket","type":"ticket","status":"open","priority":"medium"}')
if echo "$TICKET_RESULT" | grep -qv '"error"'; then
  echo "  Ticket created"
else
  echo "  Ticket failed"
  FAILURES=$((FAILURES + 1))
fi

# Wiki space
WIKI=$(curl -s -X POST \
  "${BASE_URL}/api/v1/orgs/$ORG_ID/spaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Wiki","type":"wiki","slug":"test-wiki"}')
WIKI_ID=$(echo "$WIKI" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)

# Page with minimum fields
PAGE_RESULT=$(curl -s -X POST \
  "${BASE_URL}/api/v1/orgs/$ORG_ID/spaces/$WIKI_ID/pages" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Test page","slug":"test-page","content":""}')
if echo "$PAGE_RESULT" | grep -qv '"error"'; then
  echo "  Page created"
else
  echo "  Page failed"
  FAILURES=$((FAILURES + 1))
fi

# Project space
PROJ=$(curl -s -X POST \
  "${BASE_URL}/api/v1/orgs/$ORG_ID/spaces" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Project","type":"project","slug":"test-project"}')
PROJ_ID=$(echo "$PROJ" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)

# Item with minimum fields
ITEM_RESULT=$(curl -s -X POST \
  "${BASE_URL}/api/v1/orgs/$ORG_ID/spaces/$PROJ_ID/items" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Test item","type":"task","status":"open","priority":"medium"}')
if echo "$ITEM_RESULT" | grep -qv '"error"'; then
  echo "  Item created"
else
  echo "  Item failed"
  FAILURES=$((FAILURES + 1))
fi

# ── Step 7 — Verify Content-Type headers ────────────────────
echo ""
echo "=== Step 7 — Verify API routes return correct Content-Type ==="

# API routes must return application/json
CT_API=$(curl -s -I \
  "${BASE_URL}/api/v1/orgs/$ORG_ID/spaces/$SPACE_ID/items" \
  -H "Authorization: Bearer $TOKEN" \
  | grep -i content-type || true)
echo "API Content-Type: $CT_API"
if echo "$CT_API" | grep -qi "application/json"; then
  echo "  API returns JSON"
else
  echo "  API returning wrong content type"
  FAILURES=$((FAILURES + 1))
fi

# Frontend routes must return text/html
CT_FE=$(curl -s -I "${BASE_URL}/spaces/$SPACE_ID/tickets" \
  | grep -i content-type || true)
echo "Frontend Content-Type: $CT_FE"
if echo "$CT_FE" | grep -qi "text/html"; then
  echo "  Frontend returns HTML"
else
  echo "  Frontend routing broken"
  FAILURES=$((FAILURES + 1))
fi

# ── Summary ──────────────────────────────────────────────────
echo ""
if [ "$FAILURES" -eq 0 ]; then
  echo "All API verification checks passed."
  exit 0
else
  echo "$FAILURES check(s) failed."
  exit 1
fi
