# Resident Agent and Secure Control Channel Design

**Date:** 2026-08-30
**Status:** Approved design, pending implementation plan

## 1. Goal

Replace the current systemd `oneshot` Agent plus one-minute timer with a
long-running Agent daemon. The daemon keeps the existing HTTPS telemetry data
path, adds an outbound mutually authenticated WebSocket control path, and can
execute only four explicitly supported actions:

- collect and report immediately;
- reload the local configuration;
- run a bounded self-check;
- upgrade the Agent from a signed release with automatic rollback.

The target deployment is one Panel serving 100–1,000 simultaneously connected
Agents. The Panel is upgraded before every Agent. Old Panels are not supported
by the new Agent, and the implementation must not contain a legacy-Panel
fallback path.

## 2. Approved Decisions

- Use a hybrid transport: WSS for control, HTTPS for full reports.
- The Agent always initiates outbound connections; it does not listen on a
  network port.
- Authenticate each Agent with its own mTLS certificate issued by a private
  Panel CA.
- Bootstrap with a single-use, ten-minute enrollment code and an out-of-band
  CA SHA-256 fingerprint.
- Heartbeat every 30 seconds and mark an Agent offline after 90 seconds without
  a valid heartbeat.
- Keep the report schedule in the local Agent configuration, defaulting to 60
  seconds. The Panel may request one immediate collection but cannot change the
  schedule.
- Commands for offline Agents remain queued until their action-specific expiry;
  an upgrade may remain queued for at most 24 hours.
- Upgrades require an authenticated Admin to select the version and nodes and
  confirm the operation. Automatic fleet-wide upgrades are out of scope.
- Keep the Panel single-instance. Do not introduce NATS, MQTT, Redis, or another
  message broker.
- Do not promise a sub-1 MB Agent as part of this work. Limit the raw Agent
  binary growth to at most 500 KB relative to the pre-feature release build.

## 3. Non-Goals

- Arbitrary command or shell execution.
- Restarting or reconfiguring Hysteria2, Xray, sing-box, or other managed proxy
  services.
- Editing the Agent configuration from the Panel. `reload_config` only rereads
  the local file.
- Multi-Panel active-active routing or connection ownership.
- Supporting old Panels or retaining the systemd timer as a production
  fallback.
- Replacing the existing report schema or redesigning service telemetry.
- Automatic version discovery, unattended fleet rollout, or remote downgrade.

## 4. Architecture

### 4.1 Agent components

The resident process consists of five bounded components coordinated by a
supervisor:

1. **Scheduler** — triggers startup and periodic collection, applies stable
   jitter, coalesces immediate requests, and prevents overlapping runs.
2. **Reporter** — reuses initialized HTTPS clients, maintains a bounded local
   outbox, and sends reports to the existing report endpoint using mTLS.
3. **ControlClient** — maintains the outbound WSS connection, performs the
   hello/welcome handshake, sends heartbeats, and reconnects with backoff.
4. **CommandExecutor** — validates and serially executes allow-listed commands.
5. **StateStore** — atomically persists command deduplication state, certificate
   material references, upgrade state, and the report outbox.

Configuration, service templates, detector registries, sink clients, and TLS
clients are initialized once. A successful configuration reload constructs and
validates a complete replacement runtime before atomically swapping it into the
supervisor.

### 4.2 Panel components

The Panel adds four bounded components:

1. **Enrollment service** — consumes single-use codes and signs Agent CSRs.
2. **Connection Hub** — owns the current `agent_id -> session` map and bounded
   per-session send queues.
3. **Command store and dispatcher** — persists commands and state transitions in
   SQLite, immediately dispatches to connected Agents, and resumes delivery on
   reconnect.
4. **Admin APIs and UI** — expose identity, connection, certificate, command,
   and upgrade state under role-based authorization.

The Connection Hub is deliberately in memory. After a Panel restart all Agents
reconnect and the dispatcher reloads queued commands from SQLite.

### 4.3 WebSocket implementation

Use `github.com/coder/websocket` for both client and server. It has no transitive
runtime dependencies, integrates with `net/http` and `context.Context`, and
avoids implementing RFC 6455 framing in this repository. Disable per-message
compression and set a 64 KiB maximum reassembled message size.

## 5. Panel TLS and Private PKI

### 5.1 PKI layout

The Panel private CA and TLS files live below the Panel data directory:

```text
/var/lib/net-probe-panel/pki/ca.key
/var/lib/net-probe-panel/pki/ca.crt
/var/lib/net-probe-panel/pki/server.key
/var/lib/net-probe-panel/pki/server.crt
/var/lib/net-probe-panel/pki/command-signing.key
/var/lib/net-probe-panel/pki/command-signing.pub
```

Private keys use mode `0600` and are readable only by `net-probe-panel`. The CA
is ECDSA P-256. The Panel server certificate contains the IP address or DNS name
from the configured public URL in the appropriate SAN extension. The
`public_url` setting is mandatory; startup rejects a certificate whose SAN does
not cover it.

The HTTPS server uses `tls.VerifyClientCertIfGiven`. The browser login and
enrollment endpoints may connect without a client certificate. The report and
WSS control handlers require a verified certificate chain and map the leaf
certificate identity to one active Agent record. A missing, invalid, expired,
or revoked certificate is rejected before application processing.

The current shared Agent bearer token is removed from the new installation and
is not accepted as a substitute for mTLS.

### 5.2 Enrollment

An Admin creates an enrollment record with these properties:

- 256 bits of cryptographic randomness;
- only a SHA-256 hash stored in SQLite;
- ten-minute expiry;
- exactly one successful use;
- optional intended node label for operator clarity.

The UI displays an install command containing the Panel public URL, the
single-use code, and the SHA-256 fingerprint of `ca.crt`. The code is shown only
once.

The Agent installation performs this sequence:

1. Fetch the Panel CA certificate from the read-only bootstrap endpoint without
   sending the enrollment code or any Agent identity.
2. Compute the SHA-256 fingerprint over its DER certificate and compare it in
   constant time with the out-of-band fingerprint. Abort before sending any
   secret if they differ.
3. Build an isolated trust store containing only the verified Panel CA and use
   it to establish the enrollment TLS connection; never use
   `tls_skip_verify`.
4. Generate an ECDSA P-256 private key locally.
5. Save it as `/etc/net-probe/pki/agent.key` with mode `0600`.
6. Build a CSR containing a generated Agent UUID and the local Node ID.
7. Submit the code, CSR, Agent version, Node ID, OS, and architecture.
8. Receive the signed client certificate, Panel CA, command verification public
   key, and assigned Agent ID.
9. Persist the certificate and establish the mTLS WSS session.

The private key never leaves the Agent. Replaying a consumed code returns a
generic enrollment failure and does not disclose whether the code once existed.

### 5.3 Certificate lifecycle

Client certificates are valid for 90 days. At 30 days remaining, the Agent uses
its current mTLS identity to submit a new CSR. A successful rotation creates a
new serial, atomically replaces the certificate, reconnects, and retires the old
serial. A revoked or expired identity must be re-enrolled by an Admin.

CA rotation is not part of the first implementation. The files and Agent trust
store must nevertheless be represented as a CA bundle so a later release can
support overlapping old and new roots.

## 6. Transport and Session Protocol

### 6.1 Endpoints

```text
GET  /api/v1/ca
POST /api/v1/agents/enroll
POST /api/v1/agents/renew
POST /api/v1/agents/report
GET  /api/v1/agents/control   (WebSocket upgrade)
```

All endpoints use the Panel public HTTPS listener. `/api/v1/ca` returns only
the public CA certificate and is the sole request allowed before the Agent has
verified the supplied CA fingerprint. Enrollment uses the single-use code and
the resulting isolated CA trust store. Renewal, reporting, and control require
a valid Agent client certificate.

### 6.2 Session establishment

The Agent opens:

```text
wss://<panel-host>:<panel-port>/api/v1/agents/control
```

The first Agent message is `hello`, containing:

- `control_version = "1"`;
- Agent and Node IDs;
- Agent version, OS, and architecture;
- a fixed command-capability list;
- process boot ID;
- the highest durably completed command sequence.

The Panel verifies that the message Agent ID matches the certificate identity
and returns `welcome` with:

- a random session ID;
- authoritative server time;
- heartbeat interval of 30 seconds;
- offline timeout of 90 seconds;
- maximum message size of 64 KiB;
- the next queued sequence, if one exists.

Only one current session is allowed per Agent ID. A new valid session replaces
the older connection and records the reason in the audit log.

### 6.3 Heartbeats and presence

The Agent sends an application heartbeat every 30 seconds and uses WebSocket
ping/pong to detect a dead transport. The heartbeat includes Agent version,
uptime, last report result, current command state, and outbox depth. It does not
contain complete reports or arbitrary diagnostic text.

The Panel marks the Agent offline after 90 seconds without a valid heartbeat.
Systemd health does not depend on Panel reachability: a network partition makes
the control session offline but does not cause the Agent process to restart.

## 7. Command Envelope and Lifecycle

### 7.1 Envelope

Every command is a strictly decoded JSON object:

```json
{
  "control_version": "1",
  "type": "command",
  "command_id": "uuid",
  "sequence": 42,
  "agent_id": "agent-uuid",
  "action": "collect_now",
  "issued_at": 1788010000,
  "expires_at": 1788010300,
  "payload": {},
  "signature": "base64"
}
```

The Panel signs the canonical command fields with an Ed25519 command-signing
key that is distinct from the TLS CA and release-signing key. The Agent pins the
verification key during enrollment.

Before acknowledging a command, the Agent validates:

1. protocol version and strict JSON shape;
2. certificate-authenticated session and target Agent ID;
3. Ed25519 signature;
4. issue and expiry time, allowing at most 60 seconds of future clock skew;
5. monotonic sequence and unseen command ID;
6. locally declared action capability;
7. action-specific payload schema.

Unknown fields, unknown actions, invalid signatures, and oversized results are
rejected with stable codes. There is no shell or generic execution fallback.

### 7.2 State machine

```text
queued -> dispatched -> accepted -> running -> succeeded
                                      |       -> failed
queued/dispatched ----------------------------> expired
```

The Panel persists every transition. The Agent stores the highest completed
sequence and a bounded recent-command result cache with atomic `0600` writes.
After reconnect, a duplicate command returns the durable prior result without
executing again.

Each Agent executes at most one command at a time. Panel-side delivery may be
retried until expiry; execution is exactly-once from the Agent's durable point
of view, not from transport delivery.

### 7.3 Actions

#### `collect_now`

Payload is empty. If collection is running, the request is coalesced into one
follow-up run. Success is reported only after the generated HTTPS report
receives a Panel ACK. Default TTL: 5 minutes.

#### `reload_config`

Payload is empty. The Agent loads, validates, and constructs a complete new
runtime from the local configuration path, then atomically swaps it. Failure
keeps the previous runtime active. Default TTL: 30 minutes.

#### `self_check`

Payload may contain only a fixed list of check identifiers. Results are bounded
structured codes for configuration, certificate, Panel reachability, detector
availability, filesystem permissions, and update readiness. The response never
contains arbitrary files, environment variables, secrets, or unbounded logs.
Default TTL: 30 minutes.

#### `upgrade`

Payload contains only the signed release manifest. Default TTL: 24 hours. See
Section 10.

## 8. Scheduler and Reporting

The default local configuration adds:

```toml
[agent]
report_interval = "60s"
collect_timeout = "45s"
shutdown_timeout = "20s"
```

The Panel cannot alter these values. Startup triggers one immediate collection.
Subsequent runs use stable per-Agent jitter within plus or minus five seconds so
a fleet does not synchronize on minute boundaries. Collection never overlaps;
a missed periodic tick collapses into one pending run.

The resident Reporter creates each configured sink once and reuses its HTTP
transport. Each report retains the existing report schema unless a separate
future design changes it.

### 8.1 Report outbox

Failed reports are atomically stored below `/var/lib/net-probe/outbox/` in
collection-time order. The outbox is bounded to 120 reports or 16 MiB,
whichever is reached first. At the limit, the oldest report is removed and a
counter is incremented. Corrupt files move to a bounded quarantine and do not
block later reports.

When HTTPS recovers, the Reporter sends queued reports oldest first while new
collection continues. A report is removed only after a successful Panel ACK.
WSS availability does not gate this process.

## 9. Reconnection, Backpressure, and Shutdown

- Transient WSS failures retry with exponential backoff from 1 to 60 seconds
  and 20% random jitter.
- Authentication, revocation, and protocol-version failures are classified as
  permanent security/configuration errors and retry no more than once every 15
  minutes until local configuration or certificate state changes.
- HTTPS report retry has an independent bounded backoff and outbox.
- Every connection has a bounded send queue. Overflow fails or defers the
  affected command; it never blocks the whole Hub.
- Disable WebSocket compression. Limit each reassembled message to 64 KiB and
  each Agent to 120 application messages per minute, excluding protocol
  ping/pong.
- SIGTERM stops new commands, gives the current command or collection up to 20
  seconds, flushes durable state, closes WSS cleanly, and exits.
- SIGHUP and `reload_config` use the same reload implementation.

## 10. Signed Upgrade and Rollback

### 10.1 Key separation

Use three independent key domains:

- the private CA for transport identities;
- the Panel command-signing key for command authorization;
- an Ed25519 release-signing key for artifacts.

The release private key is not stored on the Panel. It belongs to the protected
release workflow or an offline signing environment. Agents pin only the release
public key.

### 10.2 Manifest

The signed manifest contains exactly:

- semantic version;
- OS and architecture;
- exact byte size and SHA-256;
- HTTPS artifact URL;
- minimum Panel version;
- control protocol version;
- issue and expiry times.

The Agent rejects an expired manifest, unexpected architecture, unsupported
Panel/control version, non-HTTPS URL, size above the configured maximum,
invalid signature, hash mismatch, and any remote downgrade.

### 10.3 Privilege boundary

The unprivileged Agent downloads into `/var/lib/net-probe/updates/`, validates
the manifest and artifact, and atomically writes a fixed-schema update request.
It cannot write `/usr/local/bin` or invoke arbitrary systemd operations.

A root-owned systemd `.path` unit triggers a root oneshot helper implemented as
a restricted subcommand of the currently trusted root-owned Agent binary. The
helper accepts no URL, shell text, command line, or caller-selected destination.
It opens only the fixed state-directory request, rejects symlinks and paths
outside that directory, and independently re-verifies the release signature,
size, hash, version, and architecture.

The helper installs into a versioned root-owned directory and atomically changes
the active symlink. It retains only the current and previous binaries, restarts
the Agent, and waits up to 120 seconds for all of these conditions:

- systemd readiness;
- a successful mTLS WSS session;
- a successful HTTPS report from the new version.

Failure restores the previous symlink and restarts the old version. The pending
command record survives restart so the new or rolled-back Agent reports exactly
one final result.

## 11. Panel Persistence and Authorization

### 11.1 SQLite tables

Add separate control-plane tables instead of overloading `nodes`:

#### `agent_identities`

Agent/Node IDs, certificate serial and fingerprint, issue/expiry/revocation
times, last connection and heartbeat, disconnect reason, Agent version, OS,
architecture, and advertised capabilities.

#### `agent_enrollment_tokens`

Token hash, creator, created/expiry/consumed times, use limit, and optional node
label. Plaintext codes are never stored.

#### `agent_commands`

Command ID, Agent ID, monotonic sequence, action, strict payload, signature,
state, issue/expiry/dispatch/accept/start/finish times, attempt count, stable
result code, and bounded structured result.

#### `agent_command_events`

Append-only state transitions with actor, timestamp, session ID, and stable
reason code. Do not store credentials, complete certificates, private keys, or
unbounded message bodies.

Required indexes cover `(agent_id, sequence)`,
`(agent_id, state, expires_at)`, `(state, expires_at)`, and certificate serial.
Migrations are additive, transactional, and idempotent.

### 11.2 Roles

- **Viewer:** read Agent connection, certificate, and command history.
- **Operator:** Viewer rights plus `collect_now`, `reload_config`, and
  `self_check`.
- **Admin:** Operator rights plus enrollment, revocation, and upgrade.

Upgrade requires recent reauthentication and a confirmation view showing target
nodes, version, architecture, artifact size, and SHA-256. Authorization is
checked at command creation and dispatch; an existing WSS session does not grant
unlimited authority.

## 12. Systemd and Installer Migration

The production Agent unit becomes resident:

```ini
[Service]
Type=notify
User=net-probe
Restart=on-failure
RestartSec=5s
WatchdogSec=120s
TimeoutStopSec=30s
StateDirectory=net-probe
ConfigurationDirectory=net-probe
NoNewPrivileges=true
ProtectSystem=strict
```

Implement the small systemd notify/watchdog datagram protocol in an internal
package using the Go standard library rather than adding a systemd dependency.
The watchdog reports main-loop health, not remote Panel availability.

The installer requires the new Panel enrollment endpoint and validates the
supplied public URL, CA fingerprint, enrollment response, control protocol, and
initial WSS connection before switching services. It then disables and removes
`net-probe.timer`, installs the resident service plus the restricted update
helper units, starts the Agent, and verifies readiness.

There is no old-Panel fallback. Enrollment endpoint absence, incompatible
control version, or an invalid CA fingerprint aborts installation without
disabling the existing deployment.

Keep `--once` only for local diagnostics, installer verification, and incident
response. It is not installed as a periodic production path.

## 13. Security Controls

- WSS and HTTPS verify the private CA; `tls_skip_verify` is forbidden for Agent
  registration, control, and reporting.
- Require verified Agent client certificates on report, renewal, and control
  routes.
- Sign every command and every release manifest with separate Ed25519 keys.
- Strictly decode JSON and reject unknown fields.
- Enforce action-level authorization, expiration, sequence, nonce/ID
  deduplication, size limits, rate limits, deadlines, and bounded queues.
- Never log enrollment codes, tokens, private keys, certificate bodies, command
  signatures, raw manifests, arbitrary self-check data, or complete messages.
- Record connection, authentication, authorization, replay, rate-limit,
  certificate, command-transition, and abnormal-close events with stable reason
  codes.
- The Agent has no network listener, no shell command, and no privilege to
  replace its binary.
- The root update helper treats the Agent process and update directory as
  untrusted and re-verifies every security property.

## 14. Failure Semantics

- **Panel/WSS unavailable:** scheduled collection and HTTPS reporting continue;
  control presence becomes offline.
- **HTTPS unavailable:** control heartbeats continue and reports enter the
  bounded outbox.
- **SQLite unavailable:** Panel does not acknowledge command creation or state
  transitions it cannot persist; it never treats an in-memory command as
  durable.
- **Invalid local configuration:** reload fails and the previous runtime remains
  active.
- **Certificate near expiry:** automatic renewal retries with backoff and raises
  a Panel warning.
- **Certificate expired/revoked:** control/reporting are rejected and an Admin
  must re-enroll the Agent.
- **Duplicate command:** return the prior durable result without re-execution.
- **Panel restart:** sessions reconnect; queued commands resume from SQLite.
- **Agent restart:** persisted sequence, command result, outbox, and upgrade
  state prevent duplicate side effects and report loss.

## 15. UI

The node page adds:

- control status: online, offline, certificate error, protocol error;
- last heartbeat separately from last report;
- Agent version, OS/architecture, and certificate expiry;
- capability-aware action buttons;
- structured command history, duration, result, and rollback state;
- certificate revoke and re-enrollment actions for Admins;
- an upgrade confirmation showing version, architecture, size, SHA-256, and
  selected nodes.

The UI never renders arbitrary Agent HTML or log content. Command results are
fixed structured fields with bounded lengths.

## 16. Verification

### 16.1 Unit tests

- Scheduler intervals, stable jitter, coalescing, and non-overlap.
- Strict command decoding, signatures, target, expiry, clock skew, sequence,
  duplicate IDs, and action payloads.
- Atomic reload success/failure and old-runtime retention.
- Outbox ordering, ACK removal, capacity eviction, and corrupt-file isolation.
- Enrollment code hashing, expiry, one-time use, and concurrent consumption.
- Certificate issue, renewal, expiry, and revocation.
- Manifest signature, hash, version, architecture, size, path, and downgrade
  rules.

### 16.2 Integration tests

- Temporary CA with a real HTTPS/WSS Panel and mTLS Agent.
- Enrollment, hello/welcome, heartbeat, commands, reconnect, and offline command
  delivery.
- Panel and Agent restarts without duplicate command execution.
- HTTPS failure while WSS works and WSS failure while HTTPS works.
- Installer rejection of an old Panel, mismatched fingerprint, invalid
  enrollment, and unsupported protocol.
- systemd migration removes the timer and verifies notify/watchdog readiness.

### 16.3 Security and upgrade tests

- Replayed enrollment code, command, and stale sequence.
- Expired, revoked, wrong-CA, and wrong-Agent certificates.
- Forged command/release signatures, oversized messages, flooding, slow
  connections, and bounded-queue exhaustion.
- Update path traversal, symlink replacement, wrong architecture, bad hash,
  truncated artifact, disk-full, and malicious manifest.
- Successful upgrade, failed readiness rollback, interrupted download, and
  exactly-once final result across restart.
- Automated secret scan of logs and persisted results.

### 16.4 Capacity acceptance

- 1,000 simultaneous WSS connections.
- Online command dispatch-to-accepted p95 below one second.
- Offline detection within 90 plus or minus five seconds.
- A 1,000-Agent reconnect event recovers within 120 seconds without a sharp
  synchronized request spike.
- Idle Agent RSS at or below 30 MiB and average non-collection CPU below 0.5%.
- Connection Hub incremental memory below 128 MiB for 1,000 Agents.
- Agent raw release binary growth at or below 500 KB compared with the current
  release flags; exact raw and compressed sizes are reported.

## 17. Rollout

1. Deploy the new Panel and initialize its PKI and signing configuration.
2. Validate enrollment, mTLS reporting, WSS, all safe commands, upgrade, and
   rollback on one test server for 24 hours.
3. Run ten real Agents for 24 hours, including network partitions, Panel
   restart, certificate rotation, and a staged upgrade.
4. Run the 100–1,000 connection synthetic capacity suite.
5. Publish the coordinated Panel-first release and new install command.

Release is blocked by any command replay, privilege-boundary bypass, unbounded
resource growth, failed rollback, secret leakage, or capacity target failure.

## 18. References

- RFC 6455, The WebSocket Protocol: https://www.rfc-editor.org/rfc/rfc6455
- OWASP WebSocket Security Cheat Sheet:
  https://cheatsheetseries.owasp.org/cheatsheets/WebSocket_Security_Cheat_Sheet.html
- Coder WebSocket: https://github.com/coder/websocket
