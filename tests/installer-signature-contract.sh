#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d /tmp/net-probe-installer-signature.XXXXXX)"
trap 'rm -rf "$work_dir"' EXIT
cd "$repo_dir"

if ! openssl version | grep -Eq '^OpenSSL (3|[4-9])\.'; then
  echo "installer signature contract: SKIP (OpenSSL 3+ unavailable)"
  exit 0
fi

seed_hex=9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60
public_hex=d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a
private_b64="$(python3 - "$seed_hex" "$public_hex" <<'PY'
import base64, sys
print(base64.b64encode(bytes.fromhex(sys.argv[1] + sys.argv[2])).decode())
PY
)"
printf '%s' 'verified installer artifact' > "$work_dir/net-probe"
GOCACHE="${GOCACHE:-$work_dir/go-cache}" NET_PROBE_RELEASE_SIGNING_KEY_B64="$private_b64" go run ./cmd/net-probe-release \
  -artifact "$work_dir/net-probe" -version v1.2.4 -os linux -arch amd64 \
  -url https://github.com/s2005lg/net-probe/releases/download/v1.2.4/net-probe_linux_amd64 \
  -minimum-panel v1.2.3 -manifest "$work_dir/manifest.json" -signature "$work_dir/manifest.sig.b64" >/dev/null

python3 - "$public_hex" "$work_dir/public.der" "$work_dir/manifest.json" <<'PY'
import pathlib, sys
pathlib.Path(sys.argv[2]).write_bytes(bytes.fromhex("302a300506032b6570032100" + sys.argv[1]))
path = pathlib.Path(sys.argv[3])
body = path.read_bytes()
if not body.endswith(b"\n"):
    raise SystemExit("published manifest lacks expected newline")
path.write_bytes(body[:-1])
PY
base64 --decode < "$work_dir/manifest.sig.b64" > "$work_dir/manifest.sig"
openssl pkeyutl -verify -pubin -inkey "$work_dir/public.der" -keyform DER -rawin \
  -in "$work_dir/manifest.json" -sigfile "$work_dir/manifest.sig" >/dev/null

printf ' ' >> "$work_dir/manifest.json"
if openssl pkeyutl -verify -pubin -inkey "$work_dir/public.der" -keyform DER -rawin \
  -in "$work_dir/manifest.json" -sigfile "$work_dir/manifest.sig" >/dev/null 2>&1; then
  echo "FAIL: modified manifest passed installer signature verification" >&2
  exit 1
fi

echo "installer signature contract: PASS"
