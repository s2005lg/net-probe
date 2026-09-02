# Release signing

GitHub Release uses an Ed25519 private key stored only in GitHub Actions. The
public key is embedded into Panel, Agent, and update-helper binaries during the
release workflow.

## Create the signing key

Run this once on a trusted machine:

```bash
go run ./cmd/net-probe-release -generate-key
```

The output has two lines:

```text
NET_PROBE_RELEASE_SIGNING_KEY_B64=<base64-private-key>
RELEASE_PUBLIC_KEY_HEX=<hex-public-key>
```

Add `NET_PROBE_RELEASE_SIGNING_KEY_B64` in GitHub:

```text
Settings -> Secrets and variables -> Actions -> New repository secret
```

Use the first output line's value as the secret value. Keep
`RELEASE_PUBLIC_KEY_HEX` only for audit/debugging; the workflow derives it from
the private key and injects it into binaries.

## Publish a release

Run the `release` workflow manually and enter a semver tag such as `v0.1.1`.
The workflow validates the signing key before creating the tag, then builds
Linux amd64/arm64 binaries, signs Agent manifests, writes `SHA256SUMS`, and
uploads everything to the GitHub Release.

If a run fails after creating a tag, rerun the same version only when the tag
still points to the same commit. If the tag points to the wrong commit, delete
that failed tag from GitHub before releasing the corrected commit.

## Rotate the key

Generate a new key, replace the GitHub secret, then publish a new Panel release
before publishing Agent releases that depend on the new key. Agents enrolled
after that Panel release receive the new public key in their trust material.
