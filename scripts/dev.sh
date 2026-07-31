#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# shellcheck disable=SC1091
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go

if ! command -v npm >/dev/null 2>&1; then
  printf 'Node.js and npm are required. Install Node.js 22 or newer.\n' >&2
  exit 1
fi
if [[ ! -d "${ROOT_DIR}/apps/web/node_modules" ]]; then
  printf 'Frontend dependencies are missing. Run make setup first.\n' >&2
  exit 1
fi

mkdir -p "${ROOT_DIR}/.cache/go-build"

api_port="${API_PORT:-8080}"
web_port="${WEB_PORT:-3000}"
bind_address="${BIND_ADDRESS:-127.0.0.1}"
api_pid=""
web_pid=""

cleanup() {
  trap - EXIT INT TERM
  if [[ -n "${api_pid}" ]]; then
    kill "${api_pid}" 2>/dev/null || true
    wait "${api_pid}" 2>/dev/null || true
  fi
  if [[ -n "${web_pid}" ]]; then
    kill "${web_pid}" 2>/dev/null || true
    wait "${web_pid}" 2>/dev/null || true
  fi
}

trap cleanup EXIT
trap 'cleanup; exit 0' INT TERM

(
  cd "${ROOT_DIR}/apps/api"
  BIND_ADDRESS="${bind_address}" \
    PORT="${api_port}" \
    ALLOWED_ORIGINS="${ALLOWED_ORIGINS:-http://localhost:${web_port}}" \
    GOCACHE="${ROOT_DIR}/.cache/go-build" \
    go run ./cmd/server
) &
api_pid=$!

(
  cd "${ROOT_DIR}/apps/web"
  NEXT_PUBLIC_API_BASE_URL="${NEXT_PUBLIC_API_BASE_URL:-http://localhost:${api_port}}" \
    exec ./node_modules/.bin/next dev --webpack --port "${web_port}"
) &
web_pid=$!

printf 'AgentFlow API:       http://%s:%s\n' "${bind_address}" "${api_port}"
printf 'AgentFlow workbench: http://localhost:%s\n' "${web_port}"
printf 'Press Ctrl+C to stop both processes.\n'

while kill -0 "${api_pid}" 2>/dev/null && kill -0 "${web_pid}" 2>/dev/null; do
  sleep 1
done

printf 'A development process stopped unexpectedly.\n' >&2
exit 1
