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
	client.onHeartbeat = func(controlproto.Heartbeat) { heartbeats.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		var connectedAt, heartbeatAt int64
		var capabilities, version string
		err := panel.DB.QueryRow(`SELECT last_connected_at,last_heartbeat_at,capabilities_json,agent_version FROM agent_identities WHERE agent_id=?`, identity.AgentID).
			Scan(&connectedAt, &heartbeatAt, &capabilities, &version)
		if err == nil && connectedAt > 0 && heartbeatAt >= connectedAt && capabilities != "[]" && version == "v1.2.3" && heartbeats.Load() > 0 {
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
