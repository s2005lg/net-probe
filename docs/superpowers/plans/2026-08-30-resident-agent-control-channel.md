# Resident Agent and Secure Control Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the timer-driven one-shot Agent with a resident daemon that reports over mTLS HTTPS, maintains an outbound mTLS WSS control session, executes four signed allow-listed actions, and performs signed upgrades with rollback.

**Architecture:** The Agent owns a long-lived scheduler, reporter/outbox, control client, serial command executor, and durable state store. The single-instance Panel owns a private CA, enrollment and renewal APIs, an in-memory WebSocket hub, and a SQLite-backed signed command dispatcher. Telemetry stays on HTTPS; control, heartbeat, and bounded command results use WSS.

**Tech Stack:** Go 1.25 standard library, `github.com/coder/websocket` v1.8.15, BurntSushi TOML, modernc SQLite, React 18, TypeScript, Vitest, Vite, systemd.

## Global Constraints

- Agent initiates every connection and never opens a listening socket.
- HTTPS carries reports; WSS carries hello/welcome, heartbeat, commands, and bounded results.
- Preserve the existing report `schema_version = "1"` and telemetry fields.
- Use an ECDSA P-256 private CA and one client certificate per Agent; remove the shared Agent bearer token.
- Fetch `/api/v1/ca`, verify its DER SHA-256 fingerprint before sending the enrollment code, and never use `tls_skip_verify` for Panel traffic.
- Enrollment codes contain 256 random bits, are hash-only in SQLite, expire after 10 minutes, and succeed once.
- Client certificates last 90 days and renew with 30 days remaining. CA rotation is outside v1.
- Control version is `"1"`; WebSocket compression is off and messages are limited to 64 KiB.
- Heartbeat is 30 seconds; offline timeout is 90 seconds.
- Report interval defaults to 60 seconds, collection timeout to 45 seconds, shutdown timeout to 20 seconds, and stable jitter stays within plus or minus 5 seconds.
- Only `collect_now`, `reload_config`, `self_check`, and `upgrade` are valid. No shell, service restart, proxy reconfiguration, or generic fallback exists.
- TTLs are respectively 5 minutes, 30 minutes, 30 minutes, and 24 hours.
- Execute at most one command and one collection at a time; coalesce collection requests.
- Bound report outbox to 120 reports or 16 MiB and evict the oldest at the limit.
- WSS reconnect backoff is 1–60 seconds with 20% jitter; permanent security/protocol errors retry no faster than every 15 minutes.
- One Panel supports 100–1,000 Agents without Redis, MQTT, NATS, or another broker.
- Viewer reads; Operator runs non-upgrade actions; Admin enrolls, revokes, and upgrades. Upgrade also requires reauthentication within 10 minutes and explicit confirmation.
- Transport CA, command-signing key, and release-signing key are independent.
- Release private key never resides on Panel. The root helper independently verifies signature, size, hash, version, architecture, and fixed paths.
- No old-Panel fallback. Deploy Panel first and abort Agent migration before altering the old service if v1 preflight fails.
- Raw Agent binary growth is at most 500,000 bytes; idle RSS at most 30 MiB; non-collection CPU below 0.5%.

---

## File map

### New files

- `internal/controlproto/{types,sign}.go` and tests — strict wire protocol and signatures.
- `internal/agent/identity.go` and test — CA bootstrap, enrollment, renewal, credential storage.
- `internal/agent/{scheduler,outbox,reporter,runtime,control,commands,selfcheck,upgrade,sdnotify}.go` and tests — resident Agent components.
- `internal/update/{manifest,state,helper}.go` and tests — signed update contract, root helper, rollback.
- `internal/panel/pki/manager.go` and test — Panel CA, server certificate, command keys, Agent certificates.
- `internal/panel/control/hub.go` and test — current WebSocket sessions and bounded queues.
- `internal/panel/command/{store,dispatcher}.go` and tests — durable commands, signing, transitions, replay.
- `internal/panel/api/{agentauth,enrollment,control,commands}.go` and tests — mTLS and Admin APIs.
- `cmd/net-probe-release/main.go` and test — CI manifest/signature generator.
- `systemd/net-probe-update.{path,service}` — fixed root update trigger.
- `web/src/components/AgentControl.tsx` and test — actions, history, upgrade confirmation.
- `web/src/pages/Agents.tsx` and test — enrollment, certificates, fleet targets.
- `tests/{control-e2e,capacity-control}.sh` — real protocol and capacity gates.

### Modified files

- `go.mod`, `go.sum`; Agent config/sink/run/CLI files; Panel config/db/auth/API/main files.
- `web/src/lib/api.ts`, `App.tsx`, `Layout.tsx`, and `pages/NodeDetail.tsx`.
- systemd units, installers, installer smoke tests, CI/release workflows and contracts, README.

---

### Task 1: Define protocol and resident configuration contracts

**Files:**
- Create: `internal/controlproto/types.go`
- Create: `internal/controlproto/types_test.go`
- Create: `internal/controlproto/sign.go`
- Create: `internal/controlproto/sign_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `controlproto.Action`, `Command`, `Hello`, `Welcome`, `Heartbeat`, `CommandResult`, `StrictDecode`, `SignCommand`, and `VerifyCommand`.
- Produces: resident timing and Panel identity configuration consumed by later Agent tasks.

- [ ] **Step 1: Write failing strict protocol tests**

```go
func TestStrictDecodeCommand(t *testing.T) {
	good := `{"control_version":"1","type":"command","command_id":"c1","sequence":1,"agent_id":"a1","action":"collect_now","issued_at":100,"expires_at":200,"payload":{}}`
	var cmd Command
	if err := StrictDecode([]byte(good), &cmd); err != nil { t.Fatal(err) }
	bad := strings.Replace(good, `"payload":{}`, `"payload":{},"extra":true`, 1)
	if err := StrictDecode([]byte(bad), &cmd); err == nil { t.Fatal("accepted unknown field") }
}
```

Also reject unknown actions/types, trailing JSON, and `MaxMessageBytes+1` input.

- [ ] **Step 2: Verify the test fails**

Run: `go test ./internal/controlproto -run TestStrictDecodeCommand`

Expected: compile failure because protocol definitions do not exist.

- [ ] **Step 3: Implement exact wire constants and types**

```go
const (
	Version = "1"
	MaxMessageBytes = 64 << 10
	HeartbeatInterval = 30 * time.Second
	OfflineTimeout = 90 * time.Second
)
type Action string
const (
	CollectNow Action = "collect_now"
	ReloadConfig Action = "reload_config"
	SelfCheck Action = "self_check"
	Upgrade Action = "upgrade"
)
type Command struct {
	ControlVersion string `json:"control_version"`
	Type string `json:"type"`
	CommandID string `json:"command_id"`
	Sequence uint64 `json:"sequence"`
	AgentID string `json:"agent_id"`
	Action Action `json:"action"`
	IssuedAt int64 `json:"issued_at"`
	ExpiresAt int64 `json:"expires_at"`
	Payload json.RawMessage `json:"payload"`
	Signature string `json:"signature,omitempty"`
}
type Hello struct {
	ControlVersion string `json:"control_version"`
	Type string `json:"type"`
	AgentID string `json:"agent_id"`
	NodeID string `json:"node_id"`
	AgentVersion string `json:"agent_version"`
	OS string `json:"os"`
	Arch string `json:"arch"`
	Capabilities []Action `json:"capabilities"`
	BootID string `json:"boot_id"`
	HighestCompleted uint64 `json:"highest_completed"`
}
type Welcome struct {
	ControlVersion string `json:"control_version"`
	Type string `json:"type"`
	SessionID string `json:"session_id"`
	ServerTime int64 `json:"server_time"`
	HeartbeatSeconds int `json:"heartbeat_seconds"`
	OfflineSeconds int `json:"offline_seconds"`
	MaxMessageBytes int `json:"max_message_bytes"`
	NextSequence uint64 `json:"next_sequence,omitempty"`
}
type Heartbeat struct {
	ControlVersion string `json:"control_version"`
	Type string `json:"type"`
	AgentVersion string `json:"agent_version"`
	UptimeSeconds int64 `json:"uptime_seconds"`
	LastReportCode string `json:"last_report_code"`
	CurrentCommandID string `json:"current_command_id,omitempty"`
	OutboxDepth int `json:"outbox_depth"`
}
type CommandResult struct {
	ControlVersion string `json:"control_version"`
	Type string `json:"type"`
	CommandID string `json:"command_id"`
	Sequence uint64 `json:"sequence"`
	State string `json:"state"`
	Code string `json:"code"`
	Data json.RawMessage `json:"data,omitempty"`
}
func StrictDecode(data []byte, dst any) error
func TTL(action Action) time.Duration
```

Use `json.Decoder.DisallowUnknownFields`, require EOF after one object, and validate version/type/action and size.

- [ ] **Step 4: Write and pass deterministic signature tests**

```go
func TestCommandSignatureCoversEnvelope(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cmd := Command{ControlVersion:Version, Type:"command", CommandID:"c1", Sequence:7, AgentID:"a1", Action:CollectNow, IssuedAt:10, ExpiresAt:20, Payload:json.RawMessage(`{}`)}
	if err := SignCommand(priv, &cmd); err != nil { t.Fatal(err) }
	if err := VerifyCommand(pub, cmd); err != nil { t.Fatal(err) }
	cmd.AgentID = "a2"
	if err := VerifyCommand(pub, cmd); err == nil { t.Fatal("accepted mutation") }
}
```

`SigningBytes` length-prefixes every scalar field and the SHA-256 of exact payload bytes. Mutating any field or payload must fail Ed25519 verification.

- [ ] **Step 5: Write failing resident config tests**

```go
func TestResidentDefaults(t *testing.T) {
	c := Default()
	if c.Agent.ReportInterval != "60s" || c.Agent.CollectTimeout != "45s" || c.Agent.ShutdownTimeout != "20s" { t.Fatalf("agent=%+v", c.Agent) }
}
```

Also test mandatory secure Panel URL, fixed PKI defaults, positive durations, `collect_timeout < report_interval`, and rejection of Panel token/skip-verify configuration.

- [ ] **Step 6: Implement `PanelConfig` and timing fields**

```go
type PanelConfig struct {
	URL string `toml:"url"`
	CAFile string `toml:"ca_file"`
	CertFile string `toml:"cert_file"`
	KeyFile string `toml:"key_file"`
	CommandKeyFile string `toml:"command_key_file"`
	ReleaseKeyFile string `toml:"release_key_file"`
}
```

Use `/etc/net-probe/pki/{ca.crt,agent.crt,agent.key,command-signing.pub,release-signing.pub}` defaults. Keep optional webhook sinks, but Panel reporting comes only from `PanelConfig` with mTLS.

- [ ] **Step 7: Pin dependency and verify foundation**

Run: `go get github.com/coder/websocket@v1.8.15`

Run: `go test ./internal/controlproto ./internal/config && go mod tidy && git diff --check`

Expected: PASS and exact v1.8.15 in `go.mod`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/controlproto internal/config
git commit -m "feat: define resident control protocol"
```

---

### Task 2: Add control persistence, roles, and reauthentication

**Files:**
- Modify: `internal/panel/db/db.go`
- Modify: `internal/panel/db/db_test.go`
- Modify: `internal/panel/auth/auth.go`
- Modify: `internal/panel/auth/auth_test.go`
- Modify: `internal/panel/api/api.go`
- Modify: `internal/panel/api/api_test.go`

**Interfaces:**
- Produces: five control tables plus `auth.Actor`, `Role`, `Require`, and `RequireRecentReauth`.

- [ ] **Step 1: Write failing idempotent schema test**

```go
func TestMigrateControlPlaneSchema(t *testing.T) {
	d := openDB(t)
	if err := Migrate(d); err != nil { t.Fatal(err) }
	if err := Migrate(d); err != nil { t.Fatal(err) }
	for _, table := range []string{"agent_identities","agent_enrollment_tokens","agent_commands","agent_command_events","agent_releases"} {
		var n int
		_ = d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		if n != 1 { t.Fatalf("%s missing", table) }
	}
}
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/panel/db -run TestMigrateControlPlaneSchema`

Expected: FAIL because tables are missing.

- [ ] **Step 3: Add additive transactional migrations**

Create the design Section 11 columns, checks for action/state, unique `(agent_id,sequence)`, append-only events, and these indexes:

```sql
CREATE INDEX idx_agent_commands_delivery ON agent_commands(agent_id,state,expires_at);
CREATE INDEX idx_agent_commands_expiry ON agent_commands(state,expires_at);
CREATE INDEX idx_agent_identity_serial ON agent_identities(cert_serial);
```

`agent_identities` stores Agent/Node IDs, serial/fingerprint, issue/expiry/revoke,
connection/heartbeat/disconnect, version, OS/arch, and capabilities.
`agent_enrollment_tokens` stores hash, creator, create/expiry/consume, use limit,
and label. `agent_commands` stores the full signed envelope, state, all lifecycle
timestamps, attempts, and bounded result. `agent_command_events` stores actor,
session, transition, timestamp, and reason. `agent_releases` stores the verified
manifest/signature, version, OS/arch, size, SHA-256, and import actor/time.

Add `users.role TEXT NOT NULL DEFAULT 'viewer'` and `sessions.reauthenticated_at INTEGER NOT NULL DEFAULT 0`. Preserve every existing row and make reruns idempotent.

- [ ] **Step 4: Write role and reauth tests**

```go
func TestRequireRole(t *testing.T) {
	if Require(Actor{Role:Viewer}, Operator) == nil { t.Fatal("viewer elevated") }
	admin := Actor{Role:Admin, ReauthenticatedAt:time.Now()}
	if RequireRecentReauth(admin, 10*time.Minute) != nil { t.Fatal("fresh admin rejected") }
}
```

- [ ] **Step 5: Implement actor middleware and endpoints**

Define Viewer/Operator/Admin ordering, load actor from session into request context, replace `requireAdmin` with `requireRole`, and add `GET /api/v1/admin/me` plus `POST /api/v1/admin/reauth`. Reauth verifies the current user's password and updates only the current session.

Run: `go test ./internal/panel/db ./internal/panel/auth ./internal/panel/api`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/panel/db internal/panel/auth internal/panel/api/api.go internal/panel/api/api_test.go
git commit -m "feat: add control plane roles and schema"
```

---

### Task 3: Build Panel private PKI and mTLS listener

**Files:**
- Create: `internal/panel/pki/manager.go`
- Create: `internal/panel/pki/manager_test.go`
- Modify: `internal/panel/config/config.go`
- Modify: `internal/panel/config/config_test.go`
- Modify: `internal/panel/api/tls.go`
- Modify: `internal/panel/api/tls_test.go`
- Modify: `cmd/net-probe-panel/main.go`
- Modify: `cmd/net-probe-panel/main_test.go`

**Interfaces:**
- Produces: `pki.Ensure`, `Manager.TLSConfig`, `CAFingerprint`, `IssueAgent`, and `AgentIDFromCertificate`.

- [ ] **Step 1: Write failing SAN, mode, and key-separation tests**

```go
func TestEnsureCreatesServerIPSanAndSeparateKeys(t *testing.T) {
	m, err := Ensure(t.TempDir(), "https://198.51.100.8:24443")
	if err != nil { t.Fatal(err) }
	cert := parseLeaf(t, m.ServerCertFile)
	if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "198.51.100.8" { t.Fatalf("SAN=%v", cert.IPAddresses) }
	if bytes.Equal(read(t,m.CAKeyFile), read(t,m.CommandKeyFile)) { t.Fatal("keys reused") }
}
```

The test file defines `parseLeaf` and `read` locally using `os.ReadFile`,
`pem.Decode`, and `x509.ParseCertificate`; they must fail the test on every
read/decode/parse error.

Also test DNS SAN, `0600` private keys, stable reload, public-URL mismatch rejection, Agent URI identity, 90-day validity, and renewed serial replacement.

- [ ] **Step 2: Implement PKI manager**

```go
func Ensure(dataDir, publicURL string) (*Manager, error)
func (m *Manager) TLSConfig() *tls.Config
func (m *Manager) CAFingerprint() string
func (m *Manager) IssueAgent(agentID, nodeID string, csrDER []byte, now time.Time) ([]byte, string, error)
func AgentIDFromCertificate(cert *x509.Certificate) (string, error)
```

Store under `${dataDir}/pki`, use a P-256 CA, derive server IP/DNS SAN from mandatory `public_url`, generate a separate Ed25519 command key, and encode Agent identity only as `spiffe://net-probe/agent/<uuid>`. Ignore untrusted CSR subject fields when issuing.

- [ ] **Step 3: Add Panel config limits**

Add mandatory `PublicURL`, maximum 1,000 connections, send queue 32, message rate 120/minute. Require HTTPS with valid host/port.

- [ ] **Step 4: Wire the listener and test real TLS**

```go
manager, err := pki.Ensure(cfg.DataDir, cfg.PublicURL)
srv := &http.Server{Addr:cfg.ListenAddr, Handler:apiServer.Routes(), TLSConfig:manager.TLSConfig()}
err = srv.ListenAndServeTLS(manager.ServerCertFile, manager.ServerKeyFile)
```

`TLSConfig.ClientAuth` is `tls.VerifyClientCertIfGiven`. Verify browser/enrollment works without a client cert, a valid Agent produces `VerifiedChains`, and wrong CA fails handshake.

Run: `go test ./internal/panel/config ./internal/panel/pki ./internal/panel/api ./cmd/net-probe-panel`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/panel/pki internal/panel/config internal/panel/api/tls* cmd/net-probe-panel
git commit -m "feat: add panel private PKI"
```

---

### Task 4: Implement enrollment, renewal, and certificate-bound reporting

**Files:**
- Create: `internal/panel/api/agentauth.go`
- Create: `internal/panel/api/agentauth_test.go`
- Create: `internal/panel/api/enrollment.go`
- Create: `internal/panel/api/enrollment_test.go`
- Create: `internal/agent/identity.go`
- Create: `internal/agent/identity_test.go`
- Modify: `internal/panel/api/api.go`
- Modify: `internal/panel/api/report.go`
- Modify: `internal/panel/api/report_test.go`
- Modify: `internal/sink/sink.go`
- Modify: `internal/sink/sink_test.go`
- Modify: `cmd/net-probe/main.go`
- Create: `cmd/net-probe/main_test.go`

**Interfaces:**
- Produces: `agent.Enroll`, `LoadIdentity`, `RenewIfNeeded`, and Agent mTLS config.
- Produces: `requireAgent` middleware used by reports and WSS.

- [ ] **Step 1: Write failing concurrent enrollment tests**

```go
func TestEnrollmentCodeConsumedOnce(t *testing.T) {
	code := createEnrollment(t, server, 10*time.Minute)
	statuses := postConcurrently(t, 20, code)
	if count(statuses, http.StatusCreated) != 1 { t.Fatalf("statuses=%v", statuses) }
}
```

The test file defines `createEnrollment`, `postConcurrently`, and `count` as
local HTTP helpers; `postConcurrently` uses a start barrier and waits for all
20 responses before returning statuses.

Add expiry, hash-only storage, generic replay response, CSR tampering, and a client test proving no enrollment secret is sent when the fetched CA fingerprint differs.

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/panel/api ./internal/agent -run 'TestEnrollment|TestBootstrap'`

Expected: FAIL because enrollment APIs are absent.

- [ ] **Step 3: Implement enrollment and renewal routes**

Add:

```text
GET  /api/v1/ca
POST /api/v1/agents/enroll
POST /api/v1/agents/renew
POST /api/v1/admin/enrollments
POST /api/v1/admin/agents/{id}/revoke
```

Generate 32 random bytes, return plaintext once, store SHA-256 only, and consume through one conditional transaction:

```sql
UPDATE agent_enrollment_tokens SET consumed_at=?
WHERE token_hash=? AND consumed_at=0 AND expires_at>?;
```

The successful response contains assigned Agent ID, signed client certificate,
Panel CA bundle, Panel command-verification public key, and the release public
key embedded into this Panel build. Renewal requires current verified identity,
a new CSR, and 30 days or less remaining. Revocation is Admin-only and
immediately blocks report/renew/control handlers.

- [ ] **Step 4: Implement Agent bootstrap and atomic identity storage**

```go
type EnrollmentOptions struct { PanelURL, CAFingerprint, Code, PKIDir, NodeID, Version string }
type Identity struct { AgentID string; TLSConfig *tls.Config; CommandKey, ReleaseKey ed25519.PublicKey }
func Enroll(ctx context.Context, client *http.Client, opts EnrollmentOptions) (*Identity, error)
func LoadIdentity(panelURL, pkiDir string) (*Identity, error)
func RenewIfNeeded(ctx context.Context, id *Identity, now time.Time) error
```

Fetch CA without sending identity, reject cross-origin redirects, compare colon-free DER SHA-256 in constant time, then trust only that CA. Generate the P-256 key locally. Write private/state files `0600` with temp-file, fsync, rename, and directory fsync. Never log code, key, certificate body, or signatures.

- [ ] **Step 5: Replace bearer report auth with certificate binding**

```go
type AgentIdentity struct { AgentID, NodeID, Serial string }
func (s *Server) requireAgent(next func(http.ResponseWriter, *http.Request, AgentIdentity)) http.Handler
```

Require exactly one verified chain, extract URI Agent ID, query an active matching serial, and reject report JSON whose `node_id` differs from the stored Node ID. Remove bearer and `X-Agent-Id` trust. Reuse the loaded mTLS transport in the Panel sink.

- [ ] **Step 6: Add secure CLI enrollment mode**

Add `net-probe enroll --panel-url --ca-fingerprint --code-stdin`. Read code once from stdin, never accept it as an argument, and print only assigned Agent ID and success.

Run: `go test ./internal/agent ./internal/sink ./internal/panel/api ./cmd/net-probe`

Expected: PASS, including wrong-Agent, expired, revoked, and wrong-CA tests.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/identity* internal/panel/api/agentauth* internal/panel/api/enrollment* internal/panel/api/api.go internal/panel/api/report* internal/sink cmd/net-probe/main.go
git commit -m "feat: enroll agents with mutual TLS"
```

---

### Task 5: Build resident scheduler, reporter, outbox, and systemd health

**Files:**
- Create: `internal/agent/scheduler.go`
- Create: `internal/agent/scheduler_test.go`
- Create: `internal/agent/outbox.go`
- Create: `internal/agent/outbox_test.go`
- Create: `internal/agent/reporter.go`
- Create: `internal/agent/reporter_test.go`
- Create: `internal/agent/runtime.go`
- Create: `internal/agent/runtime_test.go`
- Create: `internal/agent/sdnotify.go`
- Create: `internal/agent/sdnotify_test.go`
- Modify: `internal/agent/run.go`
- Modify: `internal/agent/run_test.go`
- Modify: `internal/agent/integration_test.go`
- Modify: `cmd/net-probe/main.go`

**Interfaces:**
- Produces: `Runtime.Run`, `Reload`, `CollectNow`, `Reporter.Send`, bounded Outbox, readiness and watchdog signals.

- [ ] **Step 1: Write failing fake-clock scheduler tests**

```go
func TestSchedulerCoalescesWhileRunning(t *testing.T) {
	c := newBlockingCollector()
	s := NewScheduler("agent-1", time.Minute, c.Run)
	s.Trigger(); c.WaitStarted(t)
	s.Trigger(); s.Trigger(); c.Release()
	c.WaitRuns(t, 2)
	if c.Runs() != 2 { t.Fatalf("runs=%d", c.Runs()) }
}
```

`newBlockingCollector` is a local test helper with start/release channels and
an atomic run counter; `WaitStarted` and `WaitRuns` use one-second test
deadlines.

Also assert startup collection, deterministic jitter in `[-5s,+5s]`, no overlap, missed tick collapse, and context shutdown.

- [ ] **Step 2: Implement Scheduler and split collection from delivery**

Keep `Build`, add a Collector that returns serialized report bytes, and serialize scheduling through one event loop. Derive jitter from the first 64 bits of SHA-256 Agent ID. Never launch a second collection while one runs.

- [ ] **Step 3: Write failing Outbox boundary tests**

```go
func TestOutboxEvictsOldestAtBothLimits(t *testing.T) {
	q := NewOutbox(t.TempDir(), Limits{MaxItems:2, MaxBytes:32})
	_ = q.Put([]byte("first")); _ = q.Put([]byte("second")); _ = q.Put([]byte(strings.Repeat("x",24)))
	items, _ := q.List()
	if len(items) != 1 || string(items[0].Body) != strings.Repeat("x",24) { t.Fatalf("items=%v", items) }
}
```

Test `0600`, fsync+rename, oldest-first, ACK removal, corruption quarantine, and bounded quarantine.

- [ ] **Step 4: Implement reusable Reporter and Outbox**

Build transports once. Persist fixed JSON with report body and pending destination IDs so Panel and webhooks retry independently. Delete a destination only after 2xx; delete item after all ACK. WSS state must not gate reporting.

- [ ] **Step 5: Implement atomic Runtime reload and shutdown**

`Reload` loads, validates, builds a complete replacement Collector/Reporter/identity, and swaps one immutable snapshot only on success. Tests prove invalid TOML/certificates leave old runtime active. On shutdown stop new commands, allow current work up to `shutdown_timeout`, persist state, and return.

- [ ] **Step 6: Implement standard-library sd_notify**

Define `NotifyReady`, `NotifyWatchdog`, and `NotifyStopping` over AF_UNIX datagrams, including abstract `@` sockets. Test exact datagrams on a temporary socket. Watchdog reports main-loop health, never Panel reachability.

- [ ] **Step 7: Make resident mode the CLI default**

Default calls `Runtime.Run`; `--once` runs one collect/deliver cycle; `--check` remains preview. Use `signal.NotifyContext` for SIGINT/SIGTERM and SIGHUP for atomic reload.

Run: `go test -race ./internal/agent ./internal/sink ./cmd/net-probe`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/agent internal/sink cmd/net-probe/main.go
git commit -m "feat: run agent as resident daemon"
```

---

### Task 6: Implement WSS hub, heartbeat, and presence

**Files:**
- Create: `internal/panel/control/hub.go`
- Create: `internal/panel/control/hub_test.go`
- Create: `internal/panel/api/control.go`
- Create: `internal/panel/api/control_test.go`
- Create: `internal/agent/control.go`
- Create: `internal/agent/control_test.go`
- Modify: `internal/panel/api/api.go`
- Modify: `internal/panel/api/admin.go`
- Modify: `internal/panel/api/admin_test.go`
- Modify: `cmd/net-probe-panel/main.go`

**Interfaces:**
- Produces: `Hub.Register`, `Unregister`, `Send`, `IsOnline`, and `Snapshot`; Agent `ControlClient.Run`.

- [ ] **Step 1: Write failing Hub concurrency tests**

```go
func TestRegisterReplacesOlderSession(t *testing.T) {
	h := NewHub(1000, 32)
	old, newer := newFakeSession("old"), newFakeSession("new")
	h.Register("a1", old); h.Register("a1", newer)
	if !old.ClosedWith("replaced") || h.SessionID("a1") != "new" { t.Fatal("replacement failed") }
}
```

`newFakeSession` is defined in the test file with a buffered outbound channel,
recorded close reason, and no network activity.

Also test 1,001st Agent rejection, queue overflow returns `ErrBackpressure`, and register/send/unregister under `-race`.

- [ ] **Step 2: Implement Hub without network I/O under locks**

Mutex protects only the session map. Each session owns a buffered channel of 32. `Send` is nonblocking. Close replaced sessions after releasing the map lock.

- [ ] **Step 3: Write real TLS/WSS handler tests**

With `httptest.NewUnstartedServer` and Task 3 certificates assert: first
non-hello closes; cert/hello mismatch rejects; valid hello receives welcome;
second session replaces first; heartbeat updates DB; dead transport is detected
through WebSocket ping/pong; 65 KiB and the 121st application message/minute
reject while protocol ping/pong is excluded from the rate count.

- [ ] **Step 4: Implement Panel control handler**

Add mTLS `GET /api/v1/agents/control`, accept with compression disabled, set 64 KiB limit, validate strict hello, register one session, and persist connect/heartbeat/disconnect reason. Welcome contains random session ID and constants from `controlproto`.

- [ ] **Step 5: Implement Agent ControlClient**

Dial using mTLS. Send hello with boot ID, version, OS/arch, capabilities, and
durable sequence. Send bounded heartbeat every 30 seconds and use ping/pong for
dead transport detection. Certificate/revocation/protocol failures retry every
15 minutes; transients use 1–60 second exponential backoff plus 20% jitter.
On Runtime shutdown, stop receiving new commands, flush durable state, and send
a normal WebSocket close within the remaining 20-second shutdown budget.

- [ ] **Step 6: Expose presence in node APIs**

Add `control_status`, `last_heartbeat_at`, Agent version/OS/arch, certificate expiry, and capabilities. Derive online from 90-second heartbeat, separately from last report.

Run: `go test -race ./internal/panel/control ./internal/panel/api ./internal/agent`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/panel/control internal/panel/api internal/agent/control* cmd/net-probe-panel/main.go
git commit -m "feat: add agent control heartbeat channel"
```

---

### Task 7: Persist, sign, dispatch, and deduplicate commands

**Files:**
- Create: `internal/panel/command/store.go`
- Create: `internal/panel/command/store_test.go`
- Create: `internal/panel/command/dispatcher.go`
- Create: `internal/panel/command/dispatcher_test.go`
- Create: `internal/panel/api/commands.go`
- Create: `internal/panel/api/commands_test.go`
- Create: `internal/agent/commands.go`
- Create: `internal/agent/commands_test.go`
- Modify: `internal/agent/control.go`
- Modify: `internal/panel/api/api.go`
- Modify: `cmd/net-probe-panel/main.go`

**Interfaces:**
- Produces: `Store.Create`, `Transition`, `QueuedFor`, `History`, `Expire`; `Dispatcher`; serial Agent `CommandExecutor`.

- [ ] **Step 1: Write failing Store tests**

```go
func TestCreateAssignsMonotonicSignedSequence(t *testing.T) {
	c1 := create(t, store, "a1", controlproto.CollectNow, `{}`)
	c2 := create(t, store, "a1", controlproto.SelfCheck, `{"checks":["config"]}`)
	if c1.Sequence != 1 || c2.Sequence != 2 { t.Fatalf("seq=%d,%d", c1.Sequence,c2.Sequence) }
	if err := controlproto.VerifyCommand(pub, c2); err != nil { t.Fatal(err) }
}
```

The test-local `create` calls `Store.Create` with a fixed clock and fails the
test on error.

Test 20 concurrent creates, TTL defaults, forbidden transitions, append-only events, expiry, and 16 KiB result bound.

- [ ] **Step 2: Implement transactional signed Store**

Create sequence, signature, `queued` row, and first event inside one SQLite transaction. Allow only design state edges. Expiry worker marks queued/dispatched records expired. Never acknowledge creation before commit.

- [ ] **Step 3: Implement Dispatcher delivery and replay**

Read unexpired commands by sequence. Before every dispatch, reload the creating
actor and verify that actor still has the action's required role; cancel with a
stable authorization code if access was removed. Mark `dispatched` only after
Hub enqueue. Leave offline/backpressured commands queued. On hello replay
unexpired queued/dispatched commands in order. Reconstruct after Panel restart
only from SQLite.

- [ ] **Step 4: Write failing Agent validation/dedup tests**

```go
func TestExecutorReturnsDurablePriorResultAfterRestart(t *testing.T) {
	e1 := openExecutor(t, stateDir)
	want := e1.Execute(ctx, signedCollectCommand(t,1))
	e2 := openExecutor(t, stateDir)
	got := e2.Execute(ctx, signedCollectCommand(t,1))
	if got.Code != want.Code || handlerCalls != 1 { t.Fatalf("got=%+v calls=%d", got,handlerCalls) }
}
```

`openExecutor` constructs a real file-backed executor in the supplied state
directory; `signedCollectCommand` signs a fixed empty collect payload with the
test key.

Also reject wrong signature/target, 61-second future issue time, expiry, stale sequence, duplicate ID with changed content, unknown action/field, and invalid payload.

- [ ] **Step 5: Implement serial durable executor**

Validate before ACK, persist accepted/running/terminal state atomically, and
keep highest sequence plus newest 256 results. A duplicate terminal command
returns its durable result. After a crash with accepted/running but no terminal
record, collect/reload/self-check resume through their idempotent handler using
the same command ID; upgrade consults Task 9's durable update state and only
resumes or finalizes its current phase. Use one worker queue, fixed handler map,
and no default/generic executor.

- [ ] **Step 6: Add Operator command APIs**

Add `GET` and `POST /api/v1/admin/agents/{id}/commands`. Operator may create collect/reload/self-check only. Validate advertised capability before Store creation. Task 9 owns Admin upgrade creation.

Run: `go test -race ./internal/controlproto ./internal/panel/command ./internal/panel/api ./internal/agent`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/panel/command internal/panel/api/commands* internal/panel/api/api.go internal/agent/commands* internal/agent/control.go cmd/net-probe-panel/main.go
git commit -m "feat: dispatch signed agent commands"
```

---

### Task 8: Implement non-privileged actions

**Files:**
- Create: `internal/agent/selfcheck.go`
- Create: `internal/agent/selfcheck_test.go`
- Modify: `internal/agent/commands.go`
- Modify: `internal/agent/commands_test.go`
- Modify: `internal/agent/runtime.go`
- Modify: `internal/agent/runtime_test.go`
- Modify: `internal/agent/reporter.go`
- Modify: `internal/agent/reporter_test.go`

**Interfaces:**
- Produces: exact handlers for collect, reload, and self-check; upgrade remains unregistered until Task 9.

- [ ] **Step 1: Write failing action semantics tests**

```go
func TestCollectNowRequiresPanelACK(t *testing.T) {
	r := runtimeWithPanelStatus(http.StatusServiceUnavailable)
	result := r.HandleCollectNow(context.Background(), json.RawMessage(`{}`))
	if result.Code != "report_not_acknowledged" { t.Fatalf("result=%+v", result) }
}
```

`runtimeWithPanelStatus` is a local helper backed by an `httptest.Server` that
returns the supplied status for the mTLS report endpoint.

Assert collection coalesces behind running work, reload failure preserves old runtime, self-check rejects unknown identifiers, and no result contains fixture secrets/raw errors.

- [ ] **Step 2: Define strict payload and result schemas**

Collect and reload accept only `{}`. Self-check accepts only:

```go
type SelfCheckPayload struct { Checks []CheckID `json:"checks"` }
const (
	CheckConfig CheckID = "config"
	CheckCertificate CheckID = "certificate"
	CheckPanel CheckID = "panel"
	CheckDetectors CheckID = "detectors"
	CheckFilesystem CheckID = "filesystem"
	CheckUpdate CheckID = "update"
)
```

Return at most one fixed `{check,status,code}` per requested check, maximum 16 KiB. Never return file contents, environment values, credentialed URLs, raw errors, or logs.

- [ ] **Step 3: Connect handlers to Runtime**

Collect succeeds only after Panel HTTPS ACK; reload invokes atomic snapshot builder; self-check uses bounded per-check contexts. Register handlers in an explicit map with no fallback.

Run: `go test -race ./internal/agent`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/agent
git commit -m "feat: execute bounded agent actions"
```

---

### Task 9: Implement signed upgrades and rollback

**Files:**
- Create: `internal/update/manifest.go`
- Create: `internal/update/manifest_test.go`
- Create: `internal/update/state.go`
- Create: `internal/update/state_test.go`
- Create: `internal/update/helper.go`
- Create: `internal/update/helper_test.go`
- Create: `internal/agent/upgrade.go`
- Create: `internal/agent/upgrade_test.go`
- Create: `cmd/net-probe-release/main.go`
- Create: `cmd/net-probe-release/main_test.go`
- Create: `systemd/net-probe-update.path`
- Create: `systemd/net-probe-update.service`
- Modify: `cmd/net-probe/main.go`
- Modify: `internal/panel/api/commands.go`
- Modify: `internal/panel/api/commands_test.go`
- Modify: `.github/workflows/release.yml`
- Modify: `tests/release-contract.sh`

**Interfaces:**
- Produces: strict `update.Manifest`, `VerifyManifest`, fixed `UpdateRequest`, root `RunHelper`, and Admin upgrade APIs.

- [ ] **Step 1: Write failing manifest verification tests**

```go
func TestVerifyManifestRejectsDowngrade(t *testing.T) {
	m := signedManifest(t,"v1.2.2","linux","amd64")
	err := VerifyManifest(pub,m,VerifyOptions{CurrentVersion:"v1.2.3",OS:"linux",Arch:"amd64",ControlVersion:"1"})
	if !errors.Is(err,ErrDowngrade) { t.Fatalf("err=%v",err) }
}
```

Cover signature, expiry, future time, OS/arch, byte size, SHA-256, HTTPS URL and redirect, 32 MiB ceiling, minimum Panel, and control version.

- [ ] **Step 2: Implement strict manifest and update state**

Manifest contains exactly semantic version, OS, architecture, byte size, SHA-256, HTTPS artifact URL, minimum Panel version, control version, issue time, and expiry time plus detached signature. `UpdateRequest` contains only verified local artifact basename, manifest, previous version, Agent ID, and command ID. Reject absolute paths, separators, symlinks, wrong modes/owners, and unknown fields.

- [ ] **Step 3: Write root helper attack/rollback tests**

Use fake filesystem/service interfaces. Test traversal, symlink swap, changed artifact, bad signature/hash/arch/mode/owner, disk full, interrupted switch, failed 120-second proof, successful install, rollback, and retention of current plus previous only.

- [ ] **Step 4: Implement fixed root helper**

`net-probe internal-update-helper` accepts no flags/stdin. It opens only `/var/lib/net-probe/updates/pending.json` with no-follow, independently re-verifies, installs `/opt/net-probe/versions/<version>/net-probe` root-owned `0755`, atomically changes `/usr/local/bin/net-probe`, and restarts only `net-probe.service`.

The new Agent writes `/run/net-probe/upgrade-ready.json` only after systemd ready, WSS welcome, and Panel report all show expected version/boot ID. Helper waits 120 seconds, then restores previous symlink on failure. Final command result survives restart and emits once.

- [ ] **Step 5: Implement unprivileged upgrade handler**

Download to `/var/lib/net-probe/updates` with a 32 MiB+1 reader, HTTPS-only redirects, fsync, verify, rename, and fixed request. Do not invoke shell/systemctl. `.path` watches only `pending.json`; root `.service` has no network and fixed `ReadWritePaths`.

- [ ] **Step 6: Add release import and confirmed fleet upgrade APIs**

Add `POST/GET /api/v1/admin/releases` and `POST /api/v1/admin/upgrades`. Import verifies signature before storage. Upgrade requires Admin, reauth within 10 minutes, `confirmed=true`, explicit Agent IDs, and version/architecture/capability matches. Create one durable command per target; never auto-select fleet.

- [ ] **Step 7: Sign release assets in CI**

`cmd/net-probe-release` reads `NET_PROBE_RELEASE_SIGNING_KEY_B64`, derives the public key injected into both binaries, creates and signs one manifest per Agent artifact, then re-verifies it. Release fails before upload if secret is absent or size/hash/signature disagrees. Publish `*.manifest.json` and `*.manifest.sig` with binaries.

Run: `go test ./internal/update ./internal/agent ./internal/panel/api ./cmd/net-probe-release && bash tests/release-contract.sh`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/update internal/agent/upgrade* cmd/net-probe cmd/net-probe-release internal/panel/api/commands* systemd/net-probe-update.* .github/workflows/release.yml tests/release-contract.sh
git commit -m "feat: add signed agent upgrades with rollback"
```

---

### Task 10: Add control, enrollment, certificate, and upgrade UI

**Files:**
- Create: `web/src/components/AgentControl.tsx`
- Create: `web/src/components/AgentControl.test.tsx`
- Create: `web/src/pages/Agents.tsx`
- Create: `web/src/pages/Agents.test.tsx`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/pages/NodeDetail.tsx`
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/Layout.tsx`

**Interfaces:**
- Consumes: role, identity, enrollment, command, release, reauth, upgrade, and revoke APIs.
- Produces: capability-aware controls and Admin-only security workflows.

- [ ] **Step 1: Write failing role/capability component tests**

```tsx
it("hides unsupported actions", async () => {
  render(<AgentControl agent={agent({ role: "admin", capabilities: ["collect_now"] })} />);
  expect(screen.getByRole("button", { name: "立即采集" })).toBeEnabled();
  expect(screen.queryByRole("button", { name: "升级" })).not.toBeInTheDocument();
});
```

The test-local `agent` fixture returns a complete `AgentIdentity` and applies
only the supplied role/capability overrides.

Also prove Viewer sees history only, Operator cannot revoke/upgrade, offline commands show expiry, and Admin upgrade is disabled until recent reauth plus typed confirmation.

Run: `cd web && npm test -- AgentControl Agents`

Expected: FAIL because components do not exist.

- [ ] **Step 2: Add exact frontend API types**

Define `Role`, `AgentIdentity`, `AgentCommand`, `Enrollment`, `ReleaseManifest`, and bounded `CommandResult`. Add methods for every Task 2/4/7/9 endpoint, preserve cookies, encode IDs, and map stable result codes to fixed Chinese labels instead of rendering raw text/HTML.

- [ ] **Step 3: Implement Agent inventory/enrollment page**

Show control status, heartbeat, version, OS/arch, certificate expiry, and capabilities. Admin enrollment displays code/install command only from create response. Revocation requires confirmation and retains node telemetry.

- [ ] **Step 4: Implement node control/history**

Mount `AgentControl` in NodeDetail. Show last report separately from heartbeat; capability-aware buttons; queued expiry; transitions, duration, result, upgrade version, and rollback. Poll every 10 seconds for nonterminal commands and 30 seconds otherwise.

- [ ] **Step 5: Implement manual release import and fleet confirmation**

Admin uploads/pastes signed manifest pair, selects matching-architecture Agents, reauthenticates, reviews version/size/SHA-256/targets, types the version, and submits `confirmed:true`. Never render Agent/manifest strings as HTML.

Run: `cd web && npm run verify`

Expected: Vitest, TypeScript, Vite, and 512 KB chunk checks PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "feat: add resident agent control UI"
```

---

### Task 11: Migrate installers and systemd without legacy fallback

**Files:**
- Modify: `systemd/net-probe.service`
- Delete: `systemd/net-probe.timer`
- Modify: `systemd/net-probe-panel.service`
- Modify: `install.sh`
- Modify: `install-panel.sh`
- Modify: `tests/installers-smoke.sh`
- Modify: `tests/ci-contract.sh`

**Interfaces:**
- Consumes: enrollment CLI, ready/watchdog, public URL/fingerprint, and update units.
- Produces: safe Panel-first migration preserving old service on preflight failure.

- [ ] **Step 1: Rewrite installer smoke expectations first**

The fake Panel serves CA, enrollment, control-v1 preflight, and report responses. Assert:

```bash
contains /etc/systemd/system/net-probe.service 'Type=notify'
contains /etc/systemd/system/net-probe.service 'User=net-probe'
contains "$systemctl_log" 'disable --now net-probe.timer'
contains "$systemctl_log" 'enable --now net-probe.service net-probe-update.path'
not_contains /etc/net-probe/config.toml 'tls_skip_verify'
not_contains /etc/net-probe/config.toml 'token_file'
```

Add a missing-enrollment failure case and assert old timer remains enabled and old binary/config hashes are unchanged.

- [ ] **Step 2: Harden resident Agent unit**

```ini
[Service]
Type=notify
User=net-probe
ExecStart=/usr/local/bin/net-probe --config /etc/net-probe/config.toml
Restart=on-failure
RestartSec=5s
WatchdogSec=120s
TimeoutStopSec=30s
StateDirectory=net-probe
ConfigurationDirectory=net-probe
RuntimeDirectory=net-probe
NoNewPrivileges=true
ProtectSystem=strict
PrivateTmp=true
ReadWritePaths=/etc/net-probe/pki /var/lib/net-probe /run/net-probe
```

Add `/etc/net-probe/pki` to `ReadWritePaths` so the unprivileged Agent can
atomically renew its client certificate, while keeping configuration and the
rest of `/etc` read-only. The root helper trusts the release key embedded in
the currently trusted binary, not the Agent-writable public-key copy. Remove
timer installation and install root update units with fixed writable paths
only.

- [ ] **Step 3: Update Panel installer/unit**

Require/discover `NET_PROBE_PANEL_PUBLIC_URL`, write `public_url`, start Panel, and wait for `/api/v1/ca`. Print CA fingerprint and Admin URL; never create or print a shared Agent token. Keep Panel PKI/data private.

- [ ] **Step 4: Implement Agent preflight-before-switch**

Obtain Panel URL, fingerprint, and one-use code; pass code via stdin to
enrollment; stage binary/config; validate control v1, initial WSS, initial
report, and readiness. Install the initial binary into
`/opt/net-probe/versions/<version>/net-probe` and atomically point
`/usr/local/bin/net-probe` at it, establishing the same versioned layout used
by rollback. Only then disable/remove the timer and enable resident/update
units. Failure removes staging only and preserves old deployment.

Run: `bash -n install.sh install-panel.sh tests/*.sh`

Run in CI: `sudo env GITHUB_ACTIONS=true RUNNER_OS=Linux NET_PROBE_INSTALLER_SMOKE=1 bash tests/installers-smoke.sh`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add install.sh install-panel.sh systemd tests/installers-smoke.sh tests/ci-contract.sh
git commit -m "feat: migrate installs to resident agent"
```

---

### Task 12: Add E2E, security, capacity, size, and documentation gates

**Files:**
- Create: `tests/control-e2e.sh`
- Create: `tests/capacity-control.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `tests/ci-contract.sh`
- Modify: `tests/release-contract.sh`
- Modify: `README.md`

**Interfaces:**
- Consumes: all completed subsystems.
- Produces: release-blocking evidence for protocol, security, resources, size, and rollout.

- [ ] **Step 1: Add deterministic local E2E harness**

Build both binaries, use temporary data/config and CA, bind Panel to loopback ephemeral port, enroll one Agent, then assert report ACK, WSS online, three safe commands, offline queued delivery, Panel restart, Agent restart dedup, and certificate renewal. Trap cleanup; never touch production paths or sudo.

Run: `bash tests/control-e2e.sh`

Expected: PASS.

- [ ] **Step 2: Add security integration cases**

Cover replayed code/command, stale sequence, wrong CA/Agent/revoked/expired certs, forged signatures, 65 KiB/121st messages, queue exhaustion, traversal/symlink, interrupted artifact, and rollback. Assert stable codes and scan logs/state for fixture secrets.

- [ ] **Step 3: Add 1,000-session capacity driver**

Start 1,000 lightweight clients, dispatch one command per Agent, drop all sessions, and reconnect with jitter. Fail unless connections equal 1,000, accepted p95 is under 1 second, offline is 90 plus/minus 5 seconds, recovery under 120 seconds, and Hub incremental RSS under 128 MiB. Run on tags/manual workflow, not every PR.

- [ ] **Step 4: Add Agent size/resource gates**

Build baseline at `git merge-base HEAD origin/main` and candidate with identical release flags. Fail above 500,000 raw-byte growth. Idle an Agent for 10 minutes and fail above 30 MiB RSS or 0.5% average CPU. Print raw and gzip sizes for amd64/arm64.

- [ ] **Step 5: Wire CI/release contracts**

CI runs shell syntax, focused race tests, frontend verify, E2E, vet, and builds. Release runs manifest verification, upgrade/rollback, size, capacity, and secret scan before upload. Contract scripts assert every job and signed asset.

- [ ] **Step 6: Document coordinated operations**

README documents exact Panel-first install, code/fingerprint enrollment, local schedule, statuses, roles/TTLs, renewal/re-enrollment, release import, upgrade confirmation, rollback inspection, timer removal, no-old-Panel policy, outbound-only firewall, and secret-safe troubleshooting commands.

- [ ] **Step 7: Run complete verification**

```bash
bash -n install.sh install-panel.sh tests/*.sh
bash tests/release-contract.sh
bash tests/ci-contract.sh
bash tests/control-e2e.sh
go test -race ./internal/controlproto ./internal/agent ./internal/panel/control ./internal/panel/command ./internal/panel/api ./internal/update
go test ./...
go vet ./...
go build ./...
(cd web && npm run verify)
git diff --check
```

Expected: every command exits 0; no fixture secret is found; all size/resource/capacity thresholds pass.

- [ ] **Step 8: Commit**

```bash
git add .github/workflows tests README.md
git commit -m "test: gate resident agent releases"
```

---

## Rollout checkpoints

1. Run one test server for 24 hours through enrollment, report, WSS, safe actions, upgrade, and forced rollback.
2. Run ten real Agents for 24 hours with network partitions, Panel restart, renewal, and staged upgrade.
3. Run the 1,000-session suite and review Agent/Panel memory, CPU, latency, and binary sizes.
4. Publish the Panel-first guide and signed assets before tagging the coordinated release.
5. Block release on replay, privilege-boundary bypass, unbounded growth, rollback failure, secret leakage, or any missed capacity target.
