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

: "${OPENAI_BASE_URL:=https://dashscope.aliyuncs.com/compatible-mode/v1}"
: "${OPENAI_MODEL_NAME:=qwen3-coder-plus}"
: "${SANDBOX_URL:=http://localhost:8082}"

if [ -z "${OPENAI_API_KEY:-}" ]; then
  echo "OPENAI_API_KEY not set. Configure backend.env or export it before running." >&2
  exit 1
fi

mkdir -p "$GOCACHE_DIR"

export OPENAI_API_KEY
export OPENAI_BASE_URL
export OPENAI_MODEL_NAME
export SANDBOX_URL
export GOCACHE="$GOCACHE_DIR"
export HTTP_PROXY=""
export HTTPS_PROXY=""
export ALL_PROXY=""
export http_proxy=""
export https_proxy=""
export all_proxy=""
export NO_PROXY="localhost,127.0.0.1,::1"
export no_proxy="localhost,127.0.0.1,::1"

cd "$ROOT/backend"
exec go run cmd/api/main.go
