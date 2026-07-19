#!/usr/bin/env bash
# regression-test.sh — Full API regression suite for Azimuthal (the L2 gate,
# alongside scripts/verify-api.sh which covers HTTP smoke).
#
# Usage:
#   DATABASE_URL=... ./scripts/regression-test.sh
#   (defaults target the test database from .env.test — postgres :5433)
#
# Drives the real binary against the real database and asserts API contracts
# for each module's core loop: spaces, Beacon (tickets), Vector (project
# items), Codex (wiki), comments, and notifications. Assertions here state
# the *contract* — a failing check means the code is wrong, never the check.
# Do not weaken an assertion to get this script green.
#
# Expects the server NOT to be running; builds, starts, tests, and stops it.

set -uo pipefail

FAILURES=0
CHECKS=0
SERVER_PID=""
HTTP_CODE=""
RESPONSE=""

cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -f /tmp/azimuthal-regression
}
trap cleanup EXIT

# ── Defaults (test database, matches .env.test) ──────────────
: "${DATABASE_URL:=postgres://azimuthal_test:testpassword@localhost:5433/azimuthal_test?sslmode=disable}"
: "${STORAGE_ENDPOINT:=http://localhost:9001}"
: "${STORAGE_ACCESS_KEY:=minioadmin}"
: "${STORAGE_SECRET_KEY:=minioadmin}"
: "${STORAGE_BUCKET:=azimuthal-test}"
: "${APP_ENV:=test}"
: "${REGRESSION_PORT:=8083}"
BASE_URL="http://localhost:${REGRESSION_PORT}"

# Unique suffix per run so reruns against the same DB never collide with
# earlier data. Uses PID + seconds-since-epoch.
RUN="r$$$(date +%s | tail -c 5)"

# ── Helpers ──────────────────────────────────────────────────
# request METHOD PATH [JSON_BODY] → sets $RESPONSE (body) and $HTTP_CODE.
# Deliberately NOT used in command substitution: a subshell would discard
# both variables.
request() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-sS -X "$method" "${BASE_URL}${path}"
              -H "Authorization: Bearer ${TOKEN:-}"
              -H "Content-Type: application/json"
              -w $'\n%{http_code}')
  [ -n "$body" ] && args+=(-d "$body")
  local out
  out=$(curl "${args[@]}" 2>/dev/null)
  HTTP_CODE=$(echo "$out" | tail -1)
  RESPONSE=$(echo "$out" | sed '$d')
}

# jsonval FIELD → first string value of "field":"value" in $RESPONSE
jsonval() { echo "$RESPONSE" | grep -o "\"$1\":\"[^\"]*\"" | head -1 | cut -d'"' -f4; }

check() { # check DESCRIPTION CONDITION_RESULT(0/1)
  CHECKS=$((CHECKS + 1))
  if [ "$2" -eq 0 ]; then
    echo "  ✓ $1"
  else
    echo "  ✗ FAIL: $1"
    FAILURES=$((FAILURES + 1))
  fi
}

expect_code() { # expect_code DESCRIPTION EXPECTED
  if [ "$HTTP_CODE" = "$2" ]; then check "$1" 0; else check "$1 (expected HTTP $2, got $HTTP_CODE)" 1; fi
}

# ── Build and start ──────────────────────────────────────────
echo "=== Build and start server (port ${REGRESSION_PORT}) ==="
go build -o /tmp/azimuthal-regression ./cmd/server

DATABASE_URL="$DATABASE_URL" \
STORAGE_ENDPOINT="$STORAGE_ENDPOINT" \
STORAGE_ACCESS_KEY="$STORAGE_ACCESS_KEY" \
STORAGE_SECRET_KEY="$STORAGE_SECRET_KEY" \
STORAGE_BUCKET="$STORAGE_BUCKET" \
APP_ENV="$APP_ENV" \
APP_PORT="$REGRESSION_PORT" \
LOG_LEVEL=error \
/tmp/azimuthal-regression serve &
SERVER_PID=$!

for _ in $(seq 1 30); do
  curl -fsS "${BASE_URL}/health" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "${BASE_URL}/health" >/dev/null || { echo "Server failed to start."; exit 1; }
echo "Server running (PID $SERVER_PID)."

# ── Auth ─────────────────────────────────────────────────────
echo ""
echo "=== Auth ==="
EMAIL="regression-${RUN}@azimuthal.dev"
DATABASE_URL="$DATABASE_URL" /tmp/azimuthal-regression admin create-user \
  --email "$EMAIL" --name "Regression ${RUN}" --password "regression-pass-123" >/dev/null 2>&1

TOKEN=""
request POST /api/v1/auth/login "{\"email\":\"${EMAIL}\",\"password\":\"regression-pass-123\"}"
expect_code "login returns 200" 200
TOKEN=$(jsonval token)
check "login returns a token" $([ -n "$TOKEN" ] && echo 0 || echo 1)

request GET /api/v1/auth/me
expect_code "GET /auth/me returns 200" 200
ORG_ID=$(jsonval org_id)
USER_ID=$(jsonval id)
check "/auth/me returns org_id and id" $([ -n "$ORG_ID" ] && [ -n "$USER_ID" ] && echo 0 || echo 1)

# ── Spaces ───────────────────────────────────────────────────
echo ""
echo "=== Spaces ==="
request POST "/api/v1/orgs/${ORG_ID}/spaces" \
  "{\"name\":\"Desk ${RUN}\",\"type\":\"beacon\",\"slug\":\"desk-${RUN}\"}"
expect_code "create beacon space returns 201" 201
DESK_ID=$(jsonval id)

request POST "/api/v1/orgs/${ORG_ID}/spaces" \
  "{\"name\":\"Wiki ${RUN}\",\"type\":\"codex\",\"slug\":\"wiki-${RUN}\"}"
expect_code "create wiki space returns 201" 201
WIKI_ID=$(jsonval id)

request POST "/api/v1/orgs/${ORG_ID}/spaces" \
  "{\"name\":\"Proj ${RUN}\",\"type\":\"vector\",\"slug\":\"proj-${RUN}\"}"
expect_code "create project space returns 201" 201
PROJ_ID=$(jsonval id)

# Contract: two spaces whose names share a first word must both be creatable.
# The derived key must be de-duplicated (or the collision surfaced as 409 on a
# truly explicit duplicate) — never a 500.
request POST "/api/v1/orgs/${ORG_ID}/spaces" \
  "{\"name\":\"Shared ${RUN} One\",\"type\":\"codex\",\"slug\":\"shared-${RUN}-1\"}"
expect_code "create first 'Shared…' space returns 201" 201
request POST "/api/v1/orgs/${ORG_ID}/spaces" \
  "{\"name\":\"Shared ${RUN} Two\",\"type\":\"vector\",\"slug\":\"shared-${RUN}-2\"}"
expect_code "create second 'Shared…' space (same first word) returns 201 — derived key must not collide" 201

# ── Beacon (tickets) ─────────────────────────────────────────
echo ""
echo "=== Beacon — tickets ==="
request POST "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets" \
  '{"title":"Regression ticket","priority":"high"}'
expect_code "create ticket returns 201" 201
TICKET_ID=$(jsonval id)
check "created ticket has status open" $([ "$(jsonval status)" = "open" ] && echo 0 || echo 1)

request GET "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}"
expect_code "get ticket returns 200" 200
check "ticket priority persists as high" $([ "$(jsonval priority)" = "high" ] && echo 0 || echo 1)

# Assign to self, then unassign with explicit null — the null-vs-absent contract.
request POST "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}/assign" \
  "{\"assignee_id\":\"${USER_ID}\"}"
expect_code "assign ticket returns 200" 200
request GET "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}"
check "assignee_id set after assign" $([ "$(jsonval assignee_id)" = "$USER_ID" ] && echo 0 || echo 1)

# PATCH without assignee field must NOT clear the assignee (absent ≠ null).
request PATCH "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}" \
  '{"title":"Regression ticket renamed","description":"updated","priority":"high"}'
expect_code "update ticket returns 200" 200
request GET "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}"
check "assignee survives an update that omits assignee (absent ≠ clear)" \
  $([ "$(jsonval assignee_id)" = "$USER_ID" ] && echo 0 || echo 1)

request DELETE "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}/assign"
expect_code "unassign ticket returns 200" 200
request GET "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}"
check "assignee_id empty after explicit unassign" $([ -z "$(jsonval assignee_id)" ] && echo 0 || echo 1)

request POST "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}/status" \
  '{"status":"in_progress"}'
expect_code "transition ticket to in_progress returns 200" 200

request GET "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/kanban"
expect_code "kanban returns 200" 200
check "kanban contains the ticket" $(echo "$RESPONSE" | grep -q "$TICKET_ID" && echo 0 || echo 1)

# Comments — org+space scoped polymorphic route.
request POST "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}/comments" \
  '{"content":"regression comment"}'
expect_code "create comment on ticket returns 201" 201
request GET "/api/v1/orgs/${ORG_ID}/spaces/${DESK_ID}/tickets/${TICKET_ID}/comments"
expect_code "list ticket comments returns 200" 200
check "comment content round-trips" $(echo "$RESPONSE" | grep -q "regression comment" && echo 0 || echo 1)

# ── Vector (project items) ───────────────────────────────────
echo ""
echo "=== Vector — project items ==="
request POST "/api/v1/orgs/${ORG_ID}/spaces/${PROJ_ID}/projects/items" \
  '{"title":"Regression item","kind":"task","priority":"medium"}'
expect_code "create project item returns 201" 201
ITEM_ID=$(jsonval id)

request POST "/api/v1/orgs/${ORG_ID}/spaces/${PROJ_ID}/projects/items/${ITEM_ID}/status" \
  '{"status":"in_progress"}'
expect_code "transition item status returns 200" 200
request GET "/api/v1/orgs/${ORG_ID}/spaces/${PROJ_ID}/projects/items/${ITEM_ID}"
check "item status persists as in_progress" $([ "$(jsonval status)" = "in_progress" ] && echo 0 || echo 1)

request GET "/api/v1/orgs/${ORG_ID}/spaces/${PROJ_ID}/projects/backlog"
expect_code "backlog returns 200" 200
check "backlog contains the item" $(echo "$RESPONSE" | grep -q "$ITEM_ID" && echo 0 || echo 1)

# ── Codex (wiki) ─────────────────────────────────────────────
echo ""
echo "=== Codex — wiki ==="
request POST "/api/v1/orgs/${ORG_ID}/spaces/${WIKI_ID}/wiki" \
  '{"title":"Parent page","content":"<p>parent</p>"}'
expect_code "create wiki page returns 201" 201
PAGE_ID=$(jsonval id)

request POST "/api/v1/orgs/${ORG_ID}/spaces/${WIKI_ID}/wiki" \
  "{\"title\":\"Child page\",\"content\":\"<p>child</p>\",\"parent_id\":\"${PAGE_ID}\"}"
expect_code "create child page returns 201" 201
CHILD_ID=$(jsonval id)

request GET "/api/v1/orgs/${ORG_ID}/spaces/${WIKI_ID}/wiki/tree"
expect_code "wiki tree returns 200" 200
check "tree contains parent and child" \
  $(echo "$RESPONSE" | grep -q "$PAGE_ID" && echo "$RESPONSE" | grep -q "$CHILD_ID" && echo 0 || echo 1)

request GET "/api/v1/orgs/${ORG_ID}/spaces/${WIKI_ID}/wiki/${PAGE_ID}"
VERSION=$(echo "$RESPONSE" | grep -o '"version":[0-9]*' | head -1 | cut -d: -f2)
request PUT "/api/v1/orgs/${ORG_ID}/spaces/${WIKI_ID}/wiki/${PAGE_ID}" \
  "{\"title\":\"Parent page\",\"content\":\"<p>edited</p>\",\"expected_version\":${VERSION:-1}}"
expect_code "update page with expected_version returns 200" 200

# Stale version must be rejected (optimistic backstop) — 409, not success.
request PUT "/api/v1/orgs/${ORG_ID}/spaces/${WIKI_ID}/wiki/${PAGE_ID}" \
  "{\"title\":\"Parent page\",\"content\":\"<p>stale</p>\",\"expected_version\":${VERSION:-1}}"
expect_code "update with stale expected_version returns 409" 409

# ── Notifications ────────────────────────────────────────────
echo ""
echo "=== Notifications ==="
request GET "/api/v1/notifications"
expect_code "list notifications returns 200" 200

# ── Summary ──────────────────────────────────────────────────
echo ""
echo "=== Summary ==="
echo "Checks: ${CHECKS}  Failures: ${FAILURES}"
if [ "$FAILURES" -gt 0 ]; then
  echo "REGRESSION SUITE FAILED"
  exit 1
fi
echo "REGRESSION SUITE PASSED"
