#!/usr/bin/env bash
# push-private.sh — push current branch to private repo
set -euo pipefail
cd "$(dirname "$0")/.."
git push private main
