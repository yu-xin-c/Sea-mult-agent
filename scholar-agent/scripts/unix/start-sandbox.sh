#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
GOCACHE_DIR="$ROOT/.gocache"
DOTENV_FILE="$ROOT/backend.env"

find "$ROOT" -maxdepth 1 -type d -name '.gocache_verify*' -exec rm -rf {} + 2>/dev/null || true

if [ -f "$DOTENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$DOTENV_FILE"
  set +a
fi

: "${OPEN_SANDBOX_URL:=http://localhost:8081}"
: "${ENABLE_OPENSANDBOX_FALLBACK:=false}"

mkdir -p "$GOCACHE_DIR"

export OPEN_SANDBOX_URL
export ENABLE_OPENSANDBOX_FALLBACK
export GOCACHE="$GOCACHE_DIR"
export HTTP_PROXY=""
export HTTPS_PROXY=""
export ALL_PROXY=""
export http_proxy=""
export https_proxy=""
export all_proxy=""
export NO_PROXY="localhost,127.0.0.1,::1"
export no_proxy="localhost,127.0.0.1,::1"

cd "$ROOT/docker-sandbox"
exec go run main.go
