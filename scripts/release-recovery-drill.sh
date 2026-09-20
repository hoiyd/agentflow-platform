#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_PATH="${RELEASE_DRILL_REPORT_PATH:-${ROOT_DIR}/.cache/release-drill/latest.json}"

# shellcheck disable=SC1091
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go

if [[ -z "${RELEASE_DRILL_DATABASE_URL:-${TEST_DATABASE_URL:-}}" ]]; then
  printf 'RELEASE_DRILL_DATABASE_URL or TEST_DATABASE_URL must name a disposable PostgreSQL database\n' >&2
  exit 1
fi

cd "${ROOT_DIR}/apps/api"
RELEASE_DRILL_DATABASE_URL="${RELEASE_DRILL_DATABASE_URL:-${TEST_DATABASE_URL}}" \
RELEASE_DRILL_REPO_ROOT="${ROOT_DIR}" \
RELEASE_DRILL_REPORT_PATH="${REPORT_PATH}" \
  go test ./internal/evaluation/releasedrill \
    -run '^TestReleaseRecoveryDrill$' -count=1 -timeout=2m -v

printf 'Release recovery drill evidence written to %s\n' "${REPORT_PATH}"
