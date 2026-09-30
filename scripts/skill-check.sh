#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [[ "$(pwd -P)" != "$root" ]]; then
  printf 'Run scripts/skill-check.sh from the AgentFlow repository root.\n' >&2
  exit 1
fi

source "$root/scripts/go-env.sh"
activate_agentflow_go
go -C apps/api run ./cmd/skill check "$@"
