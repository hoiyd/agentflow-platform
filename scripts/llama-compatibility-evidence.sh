#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPORT_DIR="${LLAMA_CPP_REPORT_DIR:-${ROOT_DIR}/.cache/llama-cpp-compatibility}"

: "${LLAMA_CPP_MODEL:?set LLAMA_CPP_MODEL to the model ID sent to llama.cpp}"
: "${LLAMA_CPP_MODEL_ARTIFACT:?set LLAMA_CPP_MODEL_ARTIFACT to the stable model file or repository ID}"
: "${LLAMA_CPP_QUANTIZATION:?set LLAMA_CPP_QUANTIZATION to the loaded quantization}"
: "${LLAMA_CPP_CONTEXT_WINDOW_TOKENS:?set LLAMA_CPP_CONTEXT_WINDOW_TOKENS to the server context limit}"
: "${LLAMA_CPP_MAX_OUTPUT_TOKENS:?set LLAMA_CPP_MAX_OUTPUT_TOKENS to the route output limit}"
: "${LLAMA_CPP_HARDWARE:?set LLAMA_CPP_HARDWARE to the machine/device identity}"

if [[ -z "${LLAMA_CPP_BACKEND_VERSION:-}" ]]; then
  LLAMA_CPP_BACKEND_VERSION="$(llama-server --version 2>&1 | head -n 1)"
fi

# llama.cpp does not require authentication by default. A non-secret placeholder
# keeps AgentFlow on the configured provider path instead of local fallback.
export LLAMA_CPP_API_KEY="${LLAMA_CPP_API_KEY:-local}"
mkdir -p "${REPORT_DIR}"

# shellcheck disable=SC1091
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go

args=(
  inference
  --base-url "${LLAMA_CPP_BASE_URL:-http://127.0.0.1:18081/v1}"
  --model "${LLAMA_CPP_MODEL}"
  --backend-version "${LLAMA_CPP_BACKEND_VERSION}"
  --model-artifact "${LLAMA_CPP_MODEL_ARTIFACT}"
  --quantization "${LLAMA_CPP_QUANTIZATION}"
  --context-window-tokens "${LLAMA_CPP_CONTEXT_WINDOW_TOKENS}"
  --max-output-tokens "${LLAMA_CPP_MAX_OUTPUT_TOKENS}"
  --hardware "${LLAMA_CPP_HARDWARE}"
  --route-id "${LLAMA_CPP_ROUTE_ID:-llama-cpp}"
  --request-timeout "${LLAMA_CPP_REQUEST_TIMEOUT:-3m}"
)

if [[ "${LLAMA_CPP_TOOL_CALLING:-false}" == "true" ]]; then
  args+=(--tool-calling)
fi
if [[ "${LLAMA_CPP_STRUCTURED_OUTPUT:-true}" == "true" ]]; then
  args+=(--structured-output)
fi
if [[ -n "${AGENTFLOW_BASE_URL:-}" ]]; then
  : "${AGENTFLOW_WORKSPACE_ID:?set AGENTFLOW_WORKSPACE_ID with AGENTFLOW_BASE_URL}"
  : "${AGENTFLOW_AGENT_ID:?set AGENTFLOW_AGENT_ID with AGENTFLOW_BASE_URL}"
  args+=(--agentflow-base-url "${AGENTFLOW_BASE_URL}" --workspace-id "${AGENTFLOW_WORKSPACE_ID}" --agent-id "${AGENTFLOW_AGENT_ID}")
  args+=(--enforce)
fi

cd "${ROOT_DIR}/apps/api"
go run ./cmd/eval "${args[@]}" > "${REPORT_DIR}/report.json"
printf 'INF-001 compatibility evidence written to %s\n' "${REPORT_DIR}/report.json"
