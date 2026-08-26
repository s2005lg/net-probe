# Release Hardening Design

- Date: 2026-08-26
- Status: Approved for implementation planning
- Target: the next patch release after `v0.1.0`

## Goal

Make the next release reproducible and safer to install by adding automated
installer coverage, release checksums, an explicit MIT license, frontend type
and bundle-size gates, and accurate version examples in the documentation.

## Scope

This hardening pass includes:

- add the MIT license at the repository root;
- replace the nonexistent `v0.2.0` panel example with the current `v0.1.0`;
- generate and publish `SHA256SUMS` beside the four Linux release binaries;
- exercise the panel-to-agent token handoff in a disposable Ubuntu container;
- validate both installers with `bash -n` in CI;
- add a TypeScript `typecheck` command and run it in CI;
- lazy-load frontend routes so the initial JavaScript bundle stays small;
- fail CI when any generated JavaScript chunk exceeds 500 KiB;
- run `go build ./...` in CI in addition to tests and vetting.

## Non-goals

- Do not create or push a Git tag or GitHub release.
- Do not change the agent/panel authentication model.
- Do not add per-node tokens, command dispatch, Docker discovery, or other v2
  features.
- Do not introduce ESLint, Vitest, browser E2E infrastructure, or new npm
  dependencies.
- Do not require a live VPS, a live systemd daemon, or external Panel service
  in the test suite.

## Approach

Use a dependency-light, balanced hardening approach. Shell contract tests cover
repository release requirements, while installer smoke tests run the real
scripts as root inside a disposable Ubuntu container with only external effects
(downloads and systemd commands) replaced by deterministic fakes. The scripts
continue to run unchanged for normal users.

Frontend quality gates use the existing TypeScript compiler and Vite. Route
components are loaded with `React.lazy`, Vite emits separate page chunks, and a
small Node script inspects `web/dist/assets/*.js`. The check fails if any single
JavaScript chunk is larger than 512,000 bytes.

## Components

### Release contract test

`tests/release-contract.sh` verifies:

- `LICENSE` contains the MIT license grant;
- README version-pinning examples reference `v0.1.0` and contain no `v0.2.0`;
- the release workflow generates `SHA256SUMS`;
- the release workflow uploads `SHA256SUMS` with the binaries;
- CI invokes installer smoke tests, frontend verification, Go tests, vet, and
  build.

The contract test runs before implementation to prove the missing safeguards
produce a failing test, then becomes a permanent CI regression check.

### Installer smoke test

`tests/installers-smoke.sh` runs inside Ubuntu as root. It creates deterministic
fake `curl` and `systemctl` commands, executes `install-panel.sh` with a fixed
port, token, password, and public URL, then executes `install.sh` without Panel
environment variables. Assertions verify:

- both downloaded binaries are installed and executable;
- the Panel config contains the fixed listener and shared agent token;
- the Agent discovers the same-host Panel URL and token;
- the Agent config selects the panel sink with self-signed TLS trust enabled;
- `/etc/net-probe/panel-token` has the expected token and mode `0600`;
- both installers request the expected systemd units;
- Panel output includes a copyable cross-host Agent install command.

The host-side `tests/installers-smoke-container.sh` launches the test with an
Ubuntu 24.04 image and mounts the repository read-only. CI is responsible for
providing Docker; no Docker dependency is added to the released binaries.

### Release checksums

After building all four binaries, the release job runs `sha256sum` in a fixed
filename order and writes `SHA256SUMS`. It immediately runs
`sha256sum --check SHA256SUMS` before publishing. GitHub Release uploads the
checksum file with the binaries.

### Frontend verification

`web/package.json` adds:

- `typecheck`: `tsc --noEmit`;
- `check:bundle`: inspect built JavaScript chunks and enforce 512,000 bytes;
- `verify`: run typecheck, production build, then bundle check.

`web/src/App.tsx` keeps `Layout` and routing infrastructure eager but loads all
page components lazily behind one `Suspense` fallback. Navigation paths and API
behavior remain unchanged.

## Error handling

- Shell tests use `set -euo pipefail` and print the failed assertion.
- Installer smoke tests never run directly against the developer host; the
  wrapper requires Docker and always uses `--rm`.
- The checksum step fails the release before upload if any artifact is missing
  or a generated digest cannot be verified.
- The bundle checker reports the offending filename, actual byte count, and
  512,000-byte limit.

## Verification

The complete verification sequence is:

1. `bash tests/release-contract.sh`
2. `bash tests/installers-smoke-container.sh`
3. `cd web && npm run verify`
4. `go test ./...`
5. `go vet ./...`
6. `go build ./...`
7. `bash -n install.sh install-panel.sh tests/*.sh`
8. confirm `git status --short` contains only intended changes.

## Success criteria

- All commands in the verification sequence exit with status 0.
- Installer smoke tests prove the current same-host token handoff end to end.
- The release workflow publishes verified checksums for all four binaries.
- The frontend production build has no JavaScript chunk larger than 512,000
  bytes.
- No existing URL, API, installer default, or runtime behavior changes.
- No tag, release, push, or deployment occurs during this work item.
