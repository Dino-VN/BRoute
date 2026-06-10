#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

kill_process_group() {
  local pid="$1"
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
    kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
}

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

export PORT="${PORT:-20128}"
export WEB_PORT="${WEB_PORT:-5173}"
export DATA_DIR="${DATA_DIR:-$ROOT_DIR/.data}"
export WEB_DEV_PROXY="${WEB_DEV_PROXY:-http://127.0.0.1:$WEB_PORT}"

if [[ -z "${JWT_SECRET:-}" ]]; then
  export JWT_SECRET="dev-${HOSTNAME:-local}-omniroute-go"
  echo "[dev] JWT_SECRET was not set; using a local development secret."
fi

cleanup() {
  kill_process_group "${GO_PID:-}"
  kill_process_group "${WEB_PID:-}"
}
trap cleanup EXIT INT TERM

if [[ ! -d web/node_modules ]]; then
  echo "[dev] Installing web dependencies..."
  (cd web && npm install)
fi

echo "[dev] Starting Vite UI on http://127.0.0.1:$WEB_PORT"
setsid bash -c 'cd "$1" && npm run dev -- --host 127.0.0.1 --port "$2"' bash "$ROOT_DIR/web" "$WEB_PORT" &
WEB_PID=$!

go_signature() {
  find cmd internal -type f -name '*.go' -printf '%T@ %p\n' | sort | sha256sum | cut -d' ' -f1
}

start_go() {
  echo "[dev] Starting Go API on http://localhost:$PORT"
  echo "[dev] UI is proxied through Go from $WEB_DEV_PROXY"
  setsid go run ./cmd/omniroute &
  GO_PID=$!
}

restart_go() {
  kill_process_group "${GO_PID:-}"
  start_go
}

last_sig="$(go_signature)"
start_go

while true; do
  sleep 1
  if ! kill -0 "$WEB_PID" 2>/dev/null; then
    echo "[dev] Vite UI process exited."
    exit 1
  fi
  if [[ -n "${GO_PID:-}" ]] && ! kill -0 "$GO_PID" 2>/dev/null; then
    echo "[dev] Go process exited; waiting for file change before restart."
    wait "$GO_PID" 2>/dev/null || true
    GO_PID=""
  fi
  sig="$(go_signature)"
  if [[ "$sig" != "$last_sig" ]]; then
    last_sig="$sig"
    echo "[dev] Go source changed; restarting API..."
    restart_go
  fi
done
