#!/usr/bin/env bash
set -euo pipefail

# Check argument preservation, preview defaults, root checks, and failure propagation.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/scripts" "$fixture/apps/api"
cp "$root/scripts/skill-install.sh" "$fixture/scripts/skill-install.sh"
cat > "$fixture/scripts/go-env.sh" <<'EOF'
activate_agentflow_go() { return "${TEST_GO_STATUS:-0}"; }
go() {
  printf '%s\n' "$@"
  if [[ "${*: -1}" == '--unknown' ]]; then
    return 23
  fi
}
EOF

args=(--repo example/repository --path skills/sample --ref revision \
  --dest "/tmp/skill \"downloads\" 'review'" --timeout 45s --apply)
actual="$(cd "$fixture" && bash scripts/skill-install.sh "${args[@]}")"
expected="$(printf '%s\n' -C apps/api run ./cmd/skill install \
  --repo example/repository --path skills/sample --ref revision \
  --dest "/tmp/skill \"downloads\" 'review'" --timeout 45s --apply)"
[[ "$actual" == "$expected" ]] || { printf 'Skill installer arguments changed.\n' >&2; exit 1; }

for mode in preview apply; do
  args=(--repo example/repository --path skills/sample)
  if [[ "$mode" == apply ]]; then
    args+=(--apply)
  fi
  actual="$(cd "$fixture" && bash scripts/skill-install.sh "${args[@]}")"
  [[ "$actual" == "$(printf '%s\n' -C apps/api run ./cmd/skill install "${args[@]}")" ]]
done

for help in --help -h; do
  actual="$(cd "$fixture" && bash scripts/skill-install.sh "$help")"
  [[ "$actual" == "$(printf '%s\n' -C apps/api run ./cmd/skill install "$help")" ]]
done

if (cd "$fixture" && bash scripts/skill-install.sh --unknown) > "$fixture/failure.log" 2>&1; then
  printf 'Skill installer failure was swallowed.\n' >&2
  exit 1
else
  [[ "$?" == 23 ]]
fi
if (cd "$fixture/apps/api" && bash "$fixture/scripts/skill-install.sh" --help) > "$fixture/failure.log" 2>&1; then
  printf 'Wrong working directory was accepted.\n' >&2
  exit 1
fi
[[ "$(< "$fixture/failure.log")" == *'repository root'* ]]
if (cd "$fixture" && TEST_GO_STATUS=17 bash scripts/skill-install.sh --help) > "$fixture/failure.log" 2>&1; then
  printf 'Go activation failure was swallowed.\n' >&2
  exit 1
else
  [[ "$?" == 17 && ! -s "$fixture/failure.log" ]]
fi
printf 'Skill installation script checks passed.\n'
