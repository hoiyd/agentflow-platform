#!/usr/bin/env bash

activate_agentflow_go() {
  if [[ -s "${HOME}/.gvm/scripts/gvm" ]]; then
    # gvm is a shell function, so it must be sourced in each script process.
    # gvm reads optional shell variables and is not compatible with nounset.
    local restore_errexit=0
    local restore_nounset=0
    if [[ "$-" == *e* ]]; then
      restore_errexit=1
      set +e
    fi
    if [[ "$-" == *u* ]]; then
      restore_nounset=1
      set +u
    fi
    # shellcheck disable=SC1091
    source "${HOME}/.gvm/scripts/gvm"
    gvm use go1.26.5 >/dev/null
    local gvm_status=$?
    # gvm wraps cd to discover local package sets. The wrapper depends on
    # optional variables and breaks strict scripts, so keep the selected Go
    # environment but restore Bash's built-in directory change.
    unset -f cd 2>/dev/null || true
    unset -f __gvm_oldcd 2>/dev/null || true
    if [[ "${restore_nounset}" -eq 1 ]]; then
      set -u
    fi
    if [[ "${restore_errexit}" -eq 1 ]]; then
      set -e
    fi
    if [[ "${gvm_status}" -ne 0 ]]; then
      printf 'Unable to activate go1.26.5 through gvm.\n' >&2
      return "${gvm_status}"
    fi
  fi

  if ! command -v go >/dev/null 2>&1; then
    printf 'Go is required. Install go1.26.5 or configure it through gvm.\n' >&2
    return 1
  fi

  local version
  version="$(go env GOVERSION)"
  if [[ "${version}" != "go1.26.5" ]]; then
    printf 'AgentFlow requires go1.26.5; active version is %s.\n' "${version}" >&2
    return 1
  fi
}
