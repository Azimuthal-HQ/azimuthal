#!/usr/bin/env bash
# setup-dev-env-linux.sh — Bootstrap a Linux dev container for Azimuthal.
#
# Idempotent. Fixes the three paper cuts found in sandboxed Linux sessions
# (remote Claude Code containers, fresh CI-like boxes):
#   1. Docker daemon not running        → starts dockerd in the background
#   2. goose CLI missing                → go install
#   3. Playwright browser build mismatch → shims the preinstalled build
#
# Windows/macOS developers don't need this script — Docker Desktop and
# `npx playwright install` cover the same ground there.

set -uo pipefail

echo "→ Checking Docker daemon..."
if ! docker info >/dev/null 2>&1; then
  if command -v dockerd >/dev/null 2>&1; then
    echo "  starting dockerd in background..."
    # setsid + </dev/null fully detaches dockerd so it survives this script
    # (and the invoking shell) exiting.
    sudo setsid dockerd >/tmp/dockerd.log 2>&1 </dev/null &
    for _ in $(seq 1 30); do
      docker info >/dev/null 2>&1 && break
      sleep 1
    done
  fi
fi
docker info >/dev/null 2>&1 && echo "  ✓ Docker up" || echo "  ✗ Docker unavailable — start it manually"

echo "→ Checking goose CLI..."
if ! command -v goose >/dev/null 2>&1 && ! [ -x "$(go env GOPATH)/bin/goose" ]; then
  echo "  installing goose..."
  go install github.com/pressly/goose/v3/cmd/goose@latest
fi
[ -x "$(go env GOPATH)/bin/goose" ] || command -v goose >/dev/null 2>&1 \
  && echo "  ✓ goose available (ensure \$(go env GOPATH)/bin is on PATH)" \
  || echo "  ✗ goose install failed"

echo "→ Checking Playwright browsers..."
PW_DIR="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}"
if [ -d "$PW_DIR" ] && [ -d "$(dirname "$0")/../web/node_modules/playwright-core" ]; then
  # Version the installed @playwright/test actually wants:
  WANTED=$(grep -rho '"chromium-headless-shell"[^}]*"revision": *"[0-9]*"' \
    "$(dirname "$0")/../web/node_modules/playwright-core/browsers.json" 2>/dev/null \
    | grep -o '[0-9]*' | tail -1)
  if [ -n "${WANTED:-}" ] && [ ! -d "$PW_DIR/chromium_headless_shell-$WANTED" ]; then
    HAVE=$(ls "$PW_DIR" 2>/dev/null | grep -o 'chromium_headless_shell-[0-9]*' | grep -o '[0-9]*' | head -1)
    if [ -n "${HAVE:-}" ]; then
      echo "  shimming chromium build $HAVE as $WANTED (preinstalled browsers, no download)..."
      mkdir -p "$PW_DIR/chromium_headless_shell-$WANTED/chrome-headless-shell-linux64" \
               "$PW_DIR/chromium-$WANTED/chrome-linux64"
      ln -sf "$PW_DIR/chromium_headless_shell-$HAVE/chrome-linux/headless_shell" \
             "$PW_DIR/chromium_headless_shell-$WANTED/chrome-headless-shell-linux64/chrome-headless-shell"
      ln -sf "$PW_DIR/chromium-$HAVE/chrome-linux/chrome" \
             "$PW_DIR/chromium-$WANTED/chrome-linux64/chrome"
      touch "$PW_DIR/chromium_headless_shell-$WANTED/INSTALLATION_COMPLETE" \
            "$PW_DIR/chromium-$WANTED/INSTALLATION_COMPLETE"
    else
      echo "  no preinstalled chromium found — run 'npx playwright install chromium' if downloads are allowed"
    fi
  else
    echo "  ✓ browsers match (or web/node_modules not installed yet — run npm ci first)"
  fi
fi

echo "Done."
