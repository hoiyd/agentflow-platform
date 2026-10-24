#!/usr/bin/env bash

# Re-enter once through the dotenv loader; never execute .env contents as Shell.
if [[ "${AGENTFLOW_SCRIPT_ENV_READY:-}" != "1" ]]; then
  exec node "$(dirname "${BASH_SOURCE[0]}")/run-with-env.mjs" "${BASH_SOURCE[1]}" "$@"
fi
