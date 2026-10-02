#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${root}/scripts/go-env.sh"
activate_agentflow_go
cd "${root}/apps/api"
exec go run ./cmd/workspace "$@"
