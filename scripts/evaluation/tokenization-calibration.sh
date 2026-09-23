#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BASE_URL="${LLAMA_CPP_BASE_URL:-http://127.0.0.1:18081/v1}"
REPORT_DIR="${LLAMA_CPP_CALIBRATION_REPORT_DIR:-${ROOT_DIR}/.cache/tokenization-calibration}"

: "${LLAMA_CPP_MODEL:?set the model ID sent to llama.cpp}"
: "${LLAMA_CPP_MODEL_ARTIFACT:?set the pinned GGUF artifact ID}"
: "${LLAMA_CPP_CONTEXT_WINDOW_TOKENS:?set the server context window}"
: "${LLAMA_CPP_OUTPUT_RESERVE_TOKENS:?set the AgentFlow output reserve}"
: "${LLAMA_CPP_SAFETY_MARGIN_TOKENS:?set the AgentFlow safety margin}"

if [[ -z "${LLAMA_CPP_GGUF_SHA256:-}" ]]; then
  model_path="$(curl -fsS "${BASE_URL%/v1}/props" | jq -r '.model_path // empty')"
  if [[ ! -f "${model_path}" ]]; then
    printf 'Set LLAMA_CPP_GGUF_SHA256 when the llama.cpp model file is not local.\n' >&2
    exit 2
  fi
  LLAMA_CPP_GGUF_SHA256="$(shasum -a 256 "${model_path}" | awk '{print $1}')"
fi

export LLAMA_CPP_API_KEY="${LLAMA_CPP_API_KEY:-local}"
mkdir -p "${REPORT_DIR}"

# shellcheck disable=SC1091
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go

cd "${ROOT_DIR}/apps/api"
go run ./cmd/eval tokenization \
  --base-url "${BASE_URL}" \
  --model "${LLAMA_CPP_MODEL}" \
  --model-artifact "${LLAMA_CPP_MODEL_ARTIFACT}" \
  --gguf-sha256 "${LLAMA_CPP_GGUF_SHA256}" \
  --context-window-tokens "${LLAMA_CPP_CONTEXT_WINDOW_TOKENS}" \
  --output-reserve-tokens "${LLAMA_CPP_OUTPUT_RESERVE_TOKENS}" \
  --safety-margin-tokens "${LLAMA_CPP_SAFETY_MARGIN_TOKENS}" \
  --request-timeout "${LLAMA_CPP_CALIBRATION_TIMEOUT:-2m}" \
  --enforce > "${REPORT_DIR}/report.json"
printf 'INF-008 calibration report written to %s\n' "${REPORT_DIR}/report.json"
