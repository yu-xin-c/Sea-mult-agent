#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
HOST="${1:-0.0.0.0}"

cd "$ROOT/frontend"
exec npm run dev -- --host "$HOST"
