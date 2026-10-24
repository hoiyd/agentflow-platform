#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
source "$root/scripts/script-env.sh"
source "$root/scripts/go-env.sh"
activate_agentflow_go
# Build once per invocation rather than go run: preserve CLI exit codes/signals.
binary="$(mktemp "${TMPDIR:-/tmp}/agentflow-client.XXXXXX")"
trap 'rm -f "$binary"' EXIT
go -C "$root/apps/api" build -o "$binary" ./cmd/agentflow
"$binary" "$@"
