package agent

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/controlproto"
)

func TestControlClientConnectsWithMTLSAndSendsHeartbeat(t *testing.T) {
	panel := startEnrollmentPanel(t)
	pkiDir := t.TempDir()
	identity, err := Enroll(context.Background(), insecureBootstrapClient(), EnrollmentOptions{
		PanelURL: panel.URL, CAFingerprint: panel.Fingerprint, Code: panel.Code,
		PKIDir: pkiDir, NodeID: "node-control-client", Version: "v1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewControlClient(ControlOptions{
		PanelURL: panel.URL, Identity: identity, NodeID: "node-control-client", Version: "v1.2.3",
		Capabilities: []controlproto.Action{controlproto.CollectNow, controlproto.ReloadConfig, controlproto.SelfCheck},
		OutboxDepth:  func() int { return 2 },
	})
	if err != nil {
		t.Fatal(err)
	}
	client.heartbeatOverride = 20 * time.Millisecond
	client.transientBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	var heartbeats atomic.Int32
	var welcomes atomic.Int32
	client.onHeartbeat = func(controlproto.Heartbeat) { heartbeats.Add(1) }
	client.options.OnWelcome = func() { welcomes.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		var connectedAt, heartbeatAt int64
		var capabilities, version string
		err := panel.DB.QueryRow(`SELECT last_connected_at,last_heartbeat_at,capabilities_json,agent_version FROM agent_identities WHERE agent_id=?`, identity.AgentID).
			Scan(&connectedAt, &heartbeatAt, &capabilities, &version)
		if err == nil && connectedAt > 0 && heartbeatAt >= connectedAt && capabilities != "[]" && version == "v1.2.3" && heartbeats.Load() > 0 && welcomes.Load() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("control did not become healthy: connected=%d heartbeat=%d capabilities=%q version=%q err=%v identity=%s",
				connectedAt, heartbeatAt, capabilities, version, err, filepath.Base(pkiDir))
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("control client did not stop")
	}
}

func TestCommandWorkerQueuesWithoutBlockingAndExecutesSerially(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 2)
	release := make(chan struct{}, 2)
	var active atomic.Int32
	var maximum atomic.Int32
	handler := func(_ context.Context, command controlproto.Command) controlproto.CommandResult {
		current := active.Add(1)
		for {
			prior := maximum.Load()
			if current <= prior || maximum.CompareAndSwap(prior, current) {
				break
			}
		}
		started <- command.CommandID
		<-release
		active.Add(-1)
		return failedCommandResult(command, "done")
	}
	queue, results := startCommandWorker(ctx, 2, handler)
	queue <- controlproto.Command{CommandID: "first", Sequence: 1}
	<-started
	select {
	case queue <- controlproto.Command{CommandID: "second", Sequence: 2}:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("caller blocked behind running command")
	}
	release <- struct{}{}
	if result := <-results; result.CommandID != "first" {
		t.Fatalf("first result=%+v", result)
	}
	if commandID := <-started; commandID != "second" {
		t.Fatalf("second command=%q", commandID)
	}
	release <- struct{}{}
	if result := <-results; result.CommandID != "second" {
		t.Fatalf("second result=%+v", result)
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent commands=%d", maximum.Load())
	}
}
