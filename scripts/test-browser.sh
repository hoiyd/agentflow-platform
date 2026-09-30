#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -z "${TEST_DATABASE_URL:-}" ]]; then
  printf 'Set TEST_DATABASE_URL to a dedicated Postgres test database with CREATEDB privileges.\n' >&2
  exit 1
fi

# Preserve only files Next automatically rewrites, including their existing edits.
backup="$(mktemp -d)"
cp "${root}/apps/web/next-env.d.ts" "${backup}/next-env.d.ts"
cp "${root}/apps/web/tsconfig.json" "${backup}/tsconfig.json"
restore_generated() {
  cp "${backup}/next-env.d.ts" "${root}/apps/web/next-env.d.ts"
  cp "${backup}/tsconfig.json" "${root}/apps/web/tsconfig.json"
  rm -rf "${backup}"
}
trap restore_generated EXIT
cd "${root}/apps/web"
npx playwright test "$@"
