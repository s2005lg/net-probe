package report

import (
	"encoding/json"
	"testing"
)

func TestReportMarshal(t *testing.T) {
	r := Report{
		SchemaVersion: "1",
		AgentVersion:  "0.1.0",
		NodeID:        "node-1",
		CollectedAt:   "2026-08-18T12:00:00+08:00",
		CollectMS:     180,
		Host: Host{
			Hostname:        "node-01",
			OS:              "ubuntu",
			Load1:           0.2,
			MemTotalBytes:   1073741824,
			MemUsedPct:      50,
			DiskUsedPct:     42,
			UpgradableCount: 3,
		},
		Services: []Service{{
			Type:     "hysteria2",
			Runtime:  "systemd",
			Unit:     "hysteria-server",
			Version:  "v2.9.0",
			Active:   true,
			Enabled:  true,
			Listen:   []Listen{{Proto: "udp", Addr: "0.0.0.0", Port: 8443}},
			ListenOK: true,
			Status:   "ok",
		}},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["schema_version"] != "1" {
		t.Fatalf("schema_version = %v", m["schema_version"])
	}
	if _, ok := m["services"]; !ok {
		t.Fatal("missing services")
	}
}
