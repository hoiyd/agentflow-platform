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

mkdir -p "${ROOT_DIR}/.cache/go-build"

printf 'Downloading Go modules...\n'
(
  cd "${ROOT_DIR}/apps/api"
  GOCACHE="${ROOT_DIR}/.cache/go-build" go mod download
)

printf 'Installing locked frontend dependencies...\n'
(
  cd "${ROOT_DIR}/apps/web"
  npm ci
)

printf 'Setup complete. Run make dev to start AgentFlow.\n'
