package report

import (
	"encoding/json"
	"strings"
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

func TestStatsOnlineClients(t *testing.T) {
	b, err := json.Marshal(Stats{Tx: 10, Rx: 20, OnlineClients: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"online_clients":3`) {
		t.Fatalf("json = %s", b)
	}
}

func uint64ptr(v uint64) *uint64 { return &v }

func TestTelemetryMarshalPreservesSuccessfulZero(t *testing.T) {
	svc := Service{
		Type: "xray",
		Capabilities: &ServiceCapabilities{
			Traffic:       MetricCapability{Support: CapabilitySupported, Source: "native_api"},
			OnlineClients: MetricCapability{Support: CapabilitySupported, Source: "native_api"},
		},
		Telemetry: &ServiceTelemetry{
			Traffic:       &TrafficTelemetry{State: ObservationOK, TxBytes: uint64ptr(0), RxBytes: uint64ptr(0)},
			OnlineClients: &CountTelemetry{State: ObservationOK, Value: uint64ptr(0)},
		},
	}
	b, err := json.Marshal(svc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"tx_bytes":0`, `"rx_bytes":0`, `"value":0`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("json %s missing %s", b, want)
		}
	}
}

func TestPopulateLegacyStats(t *testing.T) {
	svc := Service{Telemetry: &ServiceTelemetry{
		Traffic:       &TrafficTelemetry{State: ObservationOK, TxBytes: uint64ptr(7), RxBytes: uint64ptr(9)},
		OnlineClients: &CountTelemetry{State: ObservationOK, Value: uint64ptr(0)},
	}}
	svc.PopulateLegacyStats()
	if svc.Stats == nil || svc.Stats.Tx != 7 || svc.Stats.Rx != 9 || svc.Stats.OnlineClients != 0 {
		t.Fatalf("legacy stats = %+v", svc.Stats)
	}
}
