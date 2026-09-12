#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_DIR="${BENCHMARK_REPORT_DIR:-${ROOT_DIR}/.cache/benchmark-suite}"
MODE="${DEMO_MODE:-online}"

for file in \
  benchmark-suite.v1.json \
  rag-offline.json \
  tool-task-protocol.json \
  tool-failure-paths.jsonl \
  recovery-paths.jsonl \
  benchmark-recovery-before-replay.json \
  benchmark-recovery-replay.json; do
  [[ -s "${REPORT_DIR}/${file}" ]] || {
    printf 'Missing demo evidence: %s (run make benchmark-evidence)\n' "${REPORT_DIR}/${file}" >&2
    exit 1
  }
done

if [[ -n "${LIVE_BENCHMARK_DIR:-}" ]]; then
  for file in manifest.json rag-semantic.json tool-context-vs-tools.json route-query-match.json route-llm-ranking.json; do
    [[ -s "${LIVE_BENCHMARK_DIR}/${file}" ]] || {
      printf 'Missing live benchmark evidence: %s\n' "${LIVE_BENCHMARK_DIR}/${file}" >&2
      exit 1
    }
  done
fi

if [[ "${MODE}" == "online" ]]; then
  curl --fail --silent --show-error "${API_BASE_URL:-http://localhost:8080}/health" >/dev/null
  curl --fail --silent --show-error "${WEB_BASE_URL:-http://localhost:3000}/workspace" >/dev/null
elif [[ "${MODE}" != "offline" ]]; then
  printf 'DEMO_MODE must be online or offline\n' >&2
  exit 1
fi

printf 'Demo preflight passed (%s): %s\n' "${MODE}" "${REPORT_DIR}"
