#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${TEST_DATABASE_URL:?Set a dedicated test database with CREATEDB privileges}"
image="${KEYCLOAK_TEST_IMAGE:-quay.io/keycloak/keycloak:26.7.5}"
name="agentflow-auth-test-$$"
cleanup() { docker rm -f "${name}" >/dev/null 2>&1 || true; }
trap cleanup EXIT
version="$(docker run --rm "${image}" --version)"
if [[ "${version}" != *"Keycloak 26.7.5"* ]]; then
  printf 'This theme gate is pinned to Keycloak 26.7.5.\n' >&2
  exit 1
fi
docker run --detach --name "${name}" -p 127.0.0.1:19081:8080 \
  --mount "type=bind,src=${root}/deploy/keycloak/themes/agentflow,dst=/opt/keycloak/themes/agentflow,readonly" \
  --mount "type=bind,src=${root}/apps/web/e2e/fixtures/keycloak-realm.json,dst=/opt/keycloak/data/import/realm.json,readonly" \
  "${image}" start-dev --import-realm --hostname=http://127.0.0.1:19081 >/dev/null
ready=false
for _ in {1..90}; do
  if curl --fail --silent http://127.0.0.1:19081/realms/agentflow-test/.well-known/openid-configuration >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "${ready}" != true ]]; then
  printf 'Disposable Keycloak did not become ready.\n' >&2
  exit 1
fi
AGENTFLOW_KEYCLOAK_TEST=1 bash "${root}/scripts/test-browser.sh" keycloak-onboarding.spec.ts "$@"
