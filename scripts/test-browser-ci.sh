#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$root/scripts/script-env.sh"

# Next rewrites these during the shared build, before per-profile wrappers run.
backup="$(mktemp -d)"
cp "$root/apps/web/next-env.d.ts" "$backup/next-env.d.ts"
cp "$root/apps/web/tsconfig.json" "$backup/tsconfig.json"
restore_generated() {
  cp "$backup/next-env.d.ts" "$root/apps/web/next-env.d.ts"
  cp "$backup/tsconfig.json" "$root/apps/web/tsconfig.json"
  rm -rf "$backup"
}
trap restore_generated EXIT
node "$root/scripts/browser-gates.mjs" "$@"
