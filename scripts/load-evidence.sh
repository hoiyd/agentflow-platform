#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_DIR="${LOAD_REPORT_DIR:-${ROOT_DIR}/.cache/load-soak}"
SOAK_DURATION="${SOAK_DURATION:-2s}"

# shellcheck disable=SC1091
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go
mkdir -p "${REPORT_DIR}"

cd "${ROOT_DIR}/apps/api"
EVALUATION_REPORT_DIR="${REPORT_DIR}" LOAD_SOAK_DURATION="${SOAK_DURATION}" \
  go test ./internal/evaluation/loadtest -run '^TestBoundedLoadAndSoak$' \
  -count=1 -timeout=6m

printf 'PROD-015 load evidence written to %s\n' "${REPORT_DIR}"
