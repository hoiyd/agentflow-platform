#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_DIR="${BENCHMARK_REPORT_DIR:-${ROOT_DIR}/.cache/benchmark-suite}"

# shellcheck disable=SC1091
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go
mkdir -p "${REPORT_DIR}"
cp "${ROOT_DIR}/examples/benchmark-suite.v1.json" "${REPORT_DIR}/benchmark-suite.v1.json"

cd "${ROOT_DIR}/apps/api"
EVALUATION_REPORT_DIR="${REPORT_DIR}" go test ./internal/evaluation/rageval \
  -run '^TestCanonicalOfflineReportArtifact$' -count=1
EVALUATION_REPORT_DIR="${REPORT_DIR}" go test ./internal/evaluation/tooleval \
  -run '^TestTaskEvaluationProductionPathAndReport$' -count=1
EVALUATION_REPORT_DIR="${REPORT_DIR}" go test ./internal/httpapi \
  -run '^TestResumeRecoverableRunThroughAPIStreamsAndCompletes$' -count=1
go test -json ./internal/evaluation/tooleval \
  -run '^TestTaskEvaluationFailuresStayInDenominator$' -count=1 \
  > "${REPORT_DIR}/tool-failure-paths.jsonl"
go test -json ./internal/httpapi \
  -run '^(TestResumeRecoverableRunThroughAPIStreamsAndCompletes|TestResumeRunRejectsStaleAndUnreconciledActions)$' -count=1 \
  > "${REPORT_DIR}/recovery-paths.jsonl"

printf 'CASE-001 benchmark evidence written to %s\n' "${REPORT_DIR}"
