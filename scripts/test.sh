#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# shellcheck disable=SC1091
source "${ROOT_DIR}/scripts/go-env.sh"
activate_agentflow_go

if ! command -v npm >/dev/null 2>&1; then
  printf 'Node.js and npm are required. Install Node.js 22 or newer.\n' >&2
  exit 1
fi
if [[ ! -d "${ROOT_DIR}/apps/web/node_modules" ]]; then
  printf 'Frontend dependencies are missing. Run make setup first.\n' >&2
  exit 1
fi

mkdir -p "${ROOT_DIR}/.cache/go-build"

printf 'Running Go tests...\n'
(
  cd "${ROOT_DIR}/apps/api"
  GOCACHE="${ROOT_DIR}/.cache/go-build" go test ./...
)

printf 'Running frontend lint and tests...\n'
(
  cd "${ROOT_DIR}/apps/web"
  npm run lint
  npm test
)

# Next.js rewrites next-env.d.ts for production route types. Preserve the
# checkout's original generated import so verification leaves the tree clean.
next_env="${ROOT_DIR}/apps/web/next-env.d.ts"
next_env_backup="$(mktemp)"
cp "${next_env}" "${next_env_backup}"
restore_next_env() {
  cp "${next_env_backup}" "${next_env}"
  rm -f "${next_env_backup}"
}
trap restore_next_env EXIT

printf 'Running frontend production build...\n'
(
  cd "${ROOT_DIR}/apps/web"
  npm run build
)

restore_next_env
trap - EXIT

printf 'All verification commands passed.\n'
