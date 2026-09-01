#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

scan() {
  local pattern="$1"
  if git grep -q -I -E "$pattern" -- . ':(exclude)tests/secret-scan.sh'; then
    echo "tracked source contains a credential-like value" >&2
    exit 1
  fi
}

scan 'BEGIN (RSA |EC |OPENSSH )?PRIVATE'" KEY"
scan '(^|[^A-Z0-9])(AKIA|ASIA)[A-Z0-9]{16}([^A-Z0-9]|$)'
scan '(^|[^A-Za-z0-9])(gh[pousr]_[A-Za-z0-9]{30,})([^A-Za-z0-9]|$)'
scan 'Root'" Password:[[:space:]]*[^<[:space:]]"

echo "secret scan: PASS"
