# Release Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the next patch release with tested installers, verified release checksums, an MIT license, accurate documentation, and frontend type/bundle gates.

**Architecture:** Keep production dependencies unchanged. Use shell contract tests for repository and workflow guarantees, run the real installers inside a disposable Ubuntu container with deterministic external-command fakes, and use the existing TypeScript/Vite toolchain for route splitting and a byte-based bundle check.

**Tech Stack:** Bash, GitHub Actions, Docker/Ubuntu 24.04, Go 1.23+, React 18, TypeScript 5.6, Vite 5.

## Global Constraints

- Do not create or push a Git tag or GitHub release.
- Do not change authentication, API routes, installer defaults, or runtime behavior.
- Do not introduce new Go modules, npm packages, ESLint, Vitest, or browser E2E infrastructure.
- Keep normal `curl | sudo bash` installation behavior unchanged.
- Enforce a maximum generated JavaScript chunk size of exactly 512,000 bytes.
- Docker tests must use `--rm` and mount the repository read-only.
- Use the MIT license with copyright year 2026 and holder `s2005lg`.

---

### Task 1: Release documentation, license, and checksums

**Files:**
- Create: `tests/release-contract.sh`
- Create: `LICENSE`
- Modify: `README.md:102-111`
- Modify: `.github/workflows/release.yml:24-39`

**Interfaces:**
- Consumes: four release artifact names already emitted by `.github/workflows/release.yml`.
- Produces: `SHA256SUMS`, uploaded beside all four binaries; `tests/release-contract.sh` for CI reuse.

- [ ] **Step 1: Write the failing release contract test**

Create `tests/release-contract.sh` with strict mode and these assertions:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }
not_contains() { ! grep -Fq -- "$2" "$1" || fail "$1 still contains: $2"; }
at_least() {
  local count
  count="$(grep -Fc -- "$2" "$1")"
  [ "$count" -ge "$3" ] || fail "$1 contains '$2' $count times; expected at least $3"
}

[ -f LICENSE ] || fail "LICENSE is missing"
contains LICENSE "MIT License"
contains LICENSE "Copyright (c) 2026 s2005lg"
contains LICENSE "Permission is hereby granted, free of charge"
not_contains README.md "v0.2.0"
contains README.md "NET_PROBE_PANEL_VERSION=v0.1.0"
contains .github/workflows/release.yml "sha256sum net-probe_linux_amd64 net-probe_linux_arm64 net-probe-panel_linux_amd64 net-probe-panel_linux_arm64 > SHA256SUMS"
contains .github/workflows/release.yml "sha256sum --check SHA256SUMS"
at_least .github/workflows/release.yml "SHA256SUMS" 3

echo "release contract: PASS"
```

- [ ] **Step 2: Run the contract test and verify RED**

Run: `bash tests/release-contract.sh`

Expected: exit 1 with `FAIL: LICENSE is missing`.

- [ ] **Step 3: Add the MIT license and correct the README version**

Create `LICENSE` with the complete MIT license text:

```text
MIT License

Copyright (c) 2026 s2005lg

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

Replace both English and Chinese `NET_PROBE_PANEL_VERSION=v0.2.0` examples with `v0.1.0`.

- [ ] **Step 4: Generate and verify release checksums**

Insert after all four build steps:

```yaml
      - name: Generate checksums
        run: |
          sha256sum net-probe_linux_amd64 net-probe_linux_arm64 net-probe-panel_linux_amd64 net-probe-panel_linux_arm64 > SHA256SUMS
          sha256sum --check SHA256SUMS
```

Add `SHA256SUMS` to `softprops/action-gh-release`'s `files` list.

- [ ] **Step 5: Run the contract test and verify GREEN**

Run: `bash tests/release-contract.sh`

Expected: `release contract: PASS` and exit 0.

- [ ] **Step 6: Commit Task 1**

```bash
git add LICENSE README.md .github/workflows/release.yml tests/release-contract.sh
git commit -m "chore: harden release artifacts"
```

---

### Task 2: Disposable installer smoke coverage

**Files:**
- Create: `tests/installers-smoke.sh`
- Create: `tests/installers-smoke-container.sh`

**Interfaces:**
- Consumes: existing `install-panel.sh` and `install.sh` command-line/environment contracts.
- Produces: a host wrapper that runs a root-only Ubuntu test without modifying the host.

- [ ] **Step 1: Create a failing container wrapper**

Create `tests/installers-smoke-container.sh` first with a call to the not-yet-created inner script:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
docker run --rm \
  -v "$repo_dir:/work:ro" \
  -w /work \
  ubuntu:24.04 \
  bash tests/installers-smoke.sh
```

- [ ] **Step 2: Run the wrapper and verify RED**

Run: `bash tests/installers-smoke-container.sh`

Expected: nonzero exit because `tests/installers-smoke.sh` does not exist.

- [ ] **Step 3: Add deterministic external-command fakes**

Create `tests/installers-smoke.sh` with the complete deterministic test:

```bash
#!/usr/bin/env bash
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "installer smoke must run as root" >&2; exit 1; }

test_dir="$(mktemp -d /tmp/net-probe-installer-smoke.XXXXXX)"
fake_bin="$test_dir/bin"
systemctl_log="$test_dir/systemctl.log"
mkdir -p "$fake_bin"

cat > "$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$output" ]; then
  printf '#!/usr/bin/env bash\nexit 0\n' > "$output"
else
  printf '203.0.113.10'
fi
EOF
chmod +x "$fake_bin/curl"

cat > "$fake_bin/systemctl" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$systemctl_log"
EOF
chmod +x "$fake_bin/systemctl"
export PATH="$fake_bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }

NET_PROBE_PANEL_VERSION=v0.1.0 \
NET_PROBE_PANEL_PORT=24443 \
NET_PROBE_PANEL_AGENT_TOKEN=test-agent-token \
NET_PROBE_PANEL_ADMIN_PASSWORD=test-admin-password \
NET_PROBE_PANEL_PUBLIC_URL=https://panel.example.test:24443 \
  bash /work/install-panel.sh > "$test_dir/panel-install.out"

NET_PROBE_VERSION=v0.1.0 bash /work/install.sh > "$test_dir/agent-install.out"

[ -x /usr/local/bin/net-probe-panel ] || fail "panel binary is not executable"
[ -x /usr/local/bin/net-probe ] || fail "agent binary is not executable"
contains /etc/net-probe-panel/config.toml 'listen_addr = ":24443"'
contains /etc/net-probe-panel/config.toml 'token = "test-agent-token"'
contains /etc/net-probe/config.toml 'url = "https://127.0.0.1:24443"'
contains /etc/net-probe/config.toml 'tls_skip_verify = true'
contains /etc/net-probe/config.toml 'token_file = "/etc/net-probe/panel-token"'
[ "$(cat /etc/net-probe/panel-token)" = "test-agent-token" ] || fail "agent token mismatch"
[ "$(stat -c '%a' /etc/net-probe/panel-token)" = "600" ] || fail "agent token mode is not 600"
contains "$systemctl_log" 'enable --now net-probe-panel.service'
contains "$systemctl_log" 'enable --now net-probe.timer'
contains "$test_dir/panel-install.out" 'NET_PROBE_PANEL_URL="https://panel.example.test:24443"'
contains "$test_dir/panel-install.out" 'NET_PROBE_PANEL_TOKEN="test-agent-token" bash'

echo "installer smoke: PASS"
```

- [ ] **Step 4: Exercise Panel install then same-host Agent install**

Review the complete script from Step 3 and confirm it executes the Panel first,
then the Agent without `NET_PROBE_PANEL_URL` or `NET_PROBE_PANEL_TOKEN`. This is
the exact condition that exercises same-host discovery rather than the explicit
environment-variable path.

- [ ] **Step 5: Run the installer smoke test and verify GREEN**

Run: `bash tests/installers-smoke-container.sh`

Expected: `installer smoke: PASS` and exit 0.

- [ ] **Step 6: Commit Task 2**

```bash
git add tests/installers-smoke.sh tests/installers-smoke-container.sh
git commit -m "test: smoke test installer token handoff"
```

---

### Task 3: Frontend type and bundle-size gates

**Files:**
- Create: `web/scripts/check-bundle.mjs`
- Modify: `web/package.json:6-10`
- Modify: `web/src/App.tsx:1-29`

**Interfaces:**
- Consumes: Vite output under `web/dist/assets` and existing default page exports.
- Produces: npm scripts `typecheck`, `check:bundle`, and `verify`; lazy route chunks with unchanged paths.

- [ ] **Step 1: Write the bundle-size checker**

Create `web/scripts/check-bundle.mjs`:

```javascript
import { readdir, stat } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const limit = 512_000;
const assetsDir = fileURLToPath(new URL("../dist/assets/", import.meta.url));
const files = (await readdir(assetsDir)).filter((name) => name.endsWith(".js"));
if (files.length === 0) throw new Error(`no JavaScript chunks found in ${assetsDir}`);

const chunks = await Promise.all(
  files.map(async (name) => ({ name, bytes: (await stat(`${assetsDir}/${name}`)).size })),
);
const oversized = chunks.filter(({ bytes }) => bytes > limit);
if (oversized.length > 0) {
  for (const { name, bytes } of oversized) {
    console.error(`${name}: ${bytes} bytes exceeds ${limit}-byte limit`);
  }
  process.exit(1);
}
console.log(`bundle check: PASS (${chunks.length} chunks, limit ${limit} bytes)`);
```

- [ ] **Step 2: Build current frontend and verify RED**

Run: `cd web && npm run build && node scripts/check-bundle.mjs`

Expected: exit 1 reporting the current approximately 606 KB `index-*.js` chunk exceeds 512,000 bytes.

- [ ] **Step 3: Lazy-load all route pages**

Replace eager page imports in `web/src/App.tsx` with:

```typescript
import { lazy, Suspense } from "react";

const Alerts = lazy(() => import("./pages/Alerts"));
const Login = lazy(() => import("./pages/Login"));
const NodeDetail = lazy(() => import("./pages/NodeDetail"));
const Nodes = lazy(() => import("./pages/Nodes"));
const Overview = lazy(() => import("./pages/Overview"));
const Settings = lazy(() => import("./pages/Settings"));
const Versions = lazy(() => import("./pages/Versions"));
```

Wrap the existing `<Routes>` element in:

```tsx
<Suspense fallback={<div className="p-6 text-slate-400">加载中…</div>}>
  {/* existing Routes */}
</Suspense>
```

- [ ] **Step 4: Add dependency-free verification scripts**

Set `web/package.json` scripts to:

```json
"scripts": {
  "dev": "vite",
  "typecheck": "tsc --noEmit",
  "build": "vite build",
  "check:bundle": "node scripts/check-bundle.mjs",
  "verify": "npm run typecheck && npm run build && npm run check:bundle",
  "preview": "vite preview"
}
```

- [ ] **Step 5: Run frontend verification and verify GREEN**

Run: `cd web && npm run verify`

Expected: TypeScript exits 0, Vite emits multiple page chunks, and the final line starts with `bundle check: PASS`.

- [ ] **Step 6: Commit Task 3**

```bash
git add web/package.json web/src/App.tsx web/scripts/check-bundle.mjs
git commit -m "perf: enforce frontend bundle budget"
```

---

### Task 4: CI hardening and full verification

**Files:**
- Create: `tests/ci-contract.sh`
- Modify: `.github/workflows/ci.yml:1-15`

**Interfaces:**
- Consumes: `tests/release-contract.sh`, `tests/installers-smoke-container.sh`, and `web`'s `verify` npm script.
- Produces: one CI job that rejects release-contract, installer, frontend, Go test, vet, or build regressions.

- [ ] **Step 1: Write the failing CI contract test**

Create `tests/ci-contract.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ci="$repo_dir/.github/workflows/ci.yml"
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$2" "$1" || fail "$1 missing: $2"; }

contains "$ci" "bash -n install.sh install-panel.sh tests/*.sh"
contains "$ci" "bash tests/release-contract.sh"
contains "$ci" "bash tests/installers-smoke-container.sh"
contains "$ci" "npm run verify"
contains "$ci" "go test ./..."
contains "$ci" "go vet ./..."
contains "$ci" "go build ./..."
echo "CI contract: PASS"
```

- [ ] **Step 2: Run the CI contract and verify RED**

Run: `bash tests/ci-contract.sh`

Expected: exit 1 because the current CI lacks installer syntax checks.

- [ ] **Step 3: Expand the CI job**

Replace the frontend build command with `cd web && npm ci && npm run verify`.
Add steps, in this order, for:

```yaml
      - name: Check shell syntax
        run: bash -n install.sh install-panel.sh tests/*.sh
      - name: Check release contract
        run: bash tests/release-contract.sh
      - name: Smoke test installers
        run: bash tests/installers-smoke-container.sh
```

Keep `go test ./...` and `go vet ./...`, then add `go build ./...`.

- [ ] **Step 4: Run focused CI checks and verify GREEN**

Run:

```bash
bash tests/ci-contract.sh
bash -n install.sh install-panel.sh tests/*.sh
bash tests/release-contract.sh
```

Expected: every command exits 0 and prints both contract PASS lines.

- [ ] **Step 5: Run the complete verification sequence**

Run:

```bash
bash tests/installers-smoke-container.sh
(cd web && npm run verify)
go test ./...
go vet ./...
go build ./...
git diff --check
git status --short
```

Expected: all commands exit 0; status lists only the Task 4 CI/test changes before commit.

- [ ] **Step 6: Commit Task 4**

```bash
git add .github/workflows/ci.yml tests/ci-contract.sh
git commit -m "ci: add release hardening gates"
```

- [ ] **Step 7: Verify clean branch after all commits**

Run:

```bash
git status --short --branch
git log --oneline --decorate -6
```

Expected: clean feature branch containing the design, plan, and four implementation commits; no tag, push, release, or deployment.
