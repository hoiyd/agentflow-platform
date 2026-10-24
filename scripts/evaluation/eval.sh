#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT_DIR}/scripts/script-env.sh"
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go

cd "${ROOT_DIR}/apps/api"
exec go run ./cmd/eval "$@"
