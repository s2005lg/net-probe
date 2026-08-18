# net-probe

`net-probe` is a small Linux agent that periodically collects host health and
detects installed proxy/VPN server processes, then uploads a JSON report to a
panel or webhook sink.

## Features

- Runs as a periodic `systemd` timer (no long-lived daemon).
- Detects services by matching systemd units and running binaries against
  built-in and custom YAML templates.
- Collects hostname, OS/kernel metadata, load, memory, disk usage, uptime, and
  available package upgrades.
- Reports listening ports and certificate expiry for detected services.
- Sends reports over HTTPS to `panel` or `webhook` sinks with optional bearer
  token authentication.

## One-line install

Clone the repository, change into it, and run the installer with root:

```bash
git clone --depth 1 https://github.com/s2005lg/net-probe.git && cd net-probe && sudo ./install.sh
```

You can select a specific release:

```bash
sudo NET_PROBE_VERSION=v0.1.0 ./install.sh
```

The installer writes the binary to `/usr/local/bin/net-probe`, creates a sample
config at `/etc/net-probe/config.toml`, and enables the
`net-probe.timer` systemd unit.

## Minimal configuration

Edit `/etc/net-probe/config.toml`:

```toml
[agent]
node_id = "edge-01"
log_level = "info"

[[sink]]
type = "panel"
url = "https://panel.example.com"
token_env = "NET_PROBE_PANEL_TOKEN"

[collect]
disk_mounts = ["/"]
upgradable = true

[detect]
include = ["hysteria2", "xray", "v2ray", "sing-box", "shadowsocks", "trojan", "tuic", "anytls", "generic"]
custom_dir = "/etc/net-probe/services.d"
```

Provide the token in the environment when the timer runs, for example in a
systemd drop-in or the shell that runs `net-probe`.

## Validate configuration and preview a report

Run a dry-run that validates the config, collects the current host and service
data, and prints a JSON report preview without sending it:

```bash
sudo net-probe --check --config /etc/net-probe/config.toml
```

To print the version:

```bash
net-probe --version
```

## Supported services

The built-in detection templates are:

- Hysteria2
- Xray
- V2Ray
- sing-box
- Shadowsocks
- Trojan
- TUIC
- AnyTLS
- Generic

See `internal/detect/builtin/*.yaml` for the built-in patterns. Custom
templates are loaded from `/etc/net-probe/services.d` by default.

## Security notes

- The systemd unit runs as the `net-probe` user and sets
  `NoNewPrivileges=true` and `ProtectSystem=strict`.
- Only `/etc/net-probe` is writable by the service; the rest of the filesystem
  is read-only from the service's perspective.
- Panel sinks require HTTPS unless `insecure_allow_http = true` is explicitly
  set for localhost-only or otherwise trusted endpoints.
- Secrets are not embedded in the report or config; use `token_env` or
  `token_file` so the token is read from an environment variable or file.

## Writing a detection template

Create a YAML file in `/etc/net-probe/services.d/`, for example
`my-service.yaml`:

```yaml
id: my-service
name: My Service
units: ["my-service", "my-service@.*"]
binary_patterns: ["my-service"]
version_cmd: ["--version"]
transport: ["tcp"]
cert_paths: []
listen_ports: []
stats_kind: ""
```

Template fields:

- `id`: stable identifier used in the report `type` field.
- `name`: human-readable service name.
- `units`: regular expressions anchored to systemd unit names (without the
  `.service` suffix).
- `binary_patterns`: regular expressions matched against the service's
  `ExecStart` path.
- `version_cmd`: arguments used to query the binary version.
- `transport`: informational transport list, such as `tcp` or `udp`.
- `cert_paths`: optional paths checked for TLS certificate expiry.
- `listen_ports`: optional ports that must be present for `listen_ok` to be
  true.
- `stats_kind`: optional service-specific statistics kind identifier.

After adding or editing templates, restart the timer so the next run picks
them up:

```bash
sudo systemctl restart net-probe.timer
```
