package detect

import (
	"context"
	"errors"
	"testing"

	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/report"
)

func TestDetectKnownUnit(t *testing.T) {
	reg, _ := NewRegistry([]Template{{
		ID: "hysteria2", Name: "Hysteria2",
		Units: []string{"hysteria-server"}, BinaryPatterns: []string{"hysteria"},
		VersionCmd: []string{"version"},
	}})
	r := fakeRunner{out: map[string]string{
		"systemctl list-unit-files --type=service --no-legend --no-pager":                                          "hysteria-server.service enabled\n",
		"systemctl show hysteria-server --property=ActiveState,SubState,UnitFileState,NRestarts,MainPID,ExecStart": "ActiveState=active\nUnitFileState=enabled\nMainPID=10\nExecStart={ path=/usr/local/bin/hysteria }",
	}}
	svcs, err := Detect(context.Background(), reg, config.DetectConfig{}, config.StatsConfig{}, Deps{Runner: r, ProcRoot: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 || svcs[0].Type != "hysteria2" || !svcs[0].Active {
		t.Fatalf("svcs = %+v", svcs)
	}
}

func TestDetectCapabilitiesForAnyTLSHaveNoTelemetry(t *testing.T) {
	reg, err := NewRegistry([]Template{{ID: "anytls", Units: []string{"anytls"}}})
	if err != nil {
		t.Fatal(err)
	}
	svcs, err := Detect(context.Background(), reg, config.DetectConfig{}, config.StatsConfig{}, Deps{Runner: activeUnitRunner("anytls"), ProcRoot: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 1 {
		t.Fatalf("services = %+v", svcs)
	}
	svc := svcs[0]
	if svc.Capabilities == nil || svc.Capabilities.Traffic.Support != report.CapabilityUnsupported || svc.Capabilities.OnlineClients.Support != report.CapabilityUnsupported {
		t.Fatalf("capabilities = %+v", svc.Capabilities)
	}
	if svc.Telemetry != nil {
		t.Fatalf("telemetry = %+v", svc.Telemetry)
	}
}

func TestDetectCapabilitiesXrayWithoutEndpointAreNotConfigured(t *testing.T) {
	reg, err := NewRegistry([]Template{{ID: "xray", Units: []string{"xray"}, StatsKind: "xray"}})
	if err != nil {
		t.Fatal(err)
	}
	svcs, err := Detect(context.Background(), reg, config.DetectConfig{}, config.StatsConfig{}, Deps{Runner: activeUnitRunner("xray"), ProcRoot: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	svc := svcs[0]
	if svc.Telemetry == nil || svc.Telemetry.Traffic == nil || svc.Telemetry.OnlineClients == nil {
		t.Fatalf("telemetry = %+v", svc.Telemetry)
	}
	if svc.Telemetry.Traffic.State != report.ObservationNotConfigured || svc.Telemetry.OnlineClients.State != report.ObservationNotConfigured {
		t.Fatalf("telemetry = %+v", svc.Telemetry)
	}
}

func TestDetectCapabilitiesXrayDisabled(t *testing.T) {
	reg, err := NewRegistry([]Template{{ID: "xray", Units: []string{"xray"}, StatsKind: "xray"}})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	statsCfg := config.StatsConfig{Services: map[string]config.StatsService{
		"xray": {Enabled: &disabled, Endpoint: "127.0.0.1:10085"},
	}}
	svcs, err := Detect(context.Background(), reg, config.DetectConfig{}, statsCfg, Deps{Runner: activeUnitRunner("xray"), ProcRoot: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	svc := svcs[0]
	if svc.Telemetry == nil || svc.Telemetry.Traffic == nil || svc.Telemetry.OnlineClients == nil {
		t.Fatalf("telemetry = %+v", svc.Telemetry)
	}
	if svc.Telemetry.Traffic.State != report.ObservationDisabled || svc.Telemetry.OnlineClients.State != report.ObservationDisabled {
		t.Fatalf("telemetry = %+v", svc.Telemetry)
	}
}

func TestDetectCapabilitiesTelemetryErrorDoesNotChangeStatus(t *testing.T) {
	reg, err := NewRegistry([]Template{{ID: "xray", Units: []string{"xray"}, StatsKind: "xray"}})
	if err != nil {
		t.Fatal(err)
	}
	runner := activeUnitRunner("xray")
	runner.responses["xray api stats query -s 127.0.0.1:10085"] = runnerResponse{err: errors.New("statistics unavailable")}
	runner.responses["xray api statsonlineiplist -s 127.0.0.1:10085 -all"] = runnerResponse{err: errors.New("statistics unavailable")}
	statsCfg := config.StatsConfig{Services: map[string]config.StatsService{
		"xray": {Endpoint: "127.0.0.1:10085"},
	}}
	svcs, err := Detect(context.Background(), reg, config.DetectConfig{}, statsCfg, Deps{Runner: runner, ProcRoot: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	svc := svcs[0]
	if svc.Status != "ok" || svc.Error != "" {
		t.Fatalf("status=%q error=%q", svc.Status, svc.Error)
	}
	if svc.Telemetry == nil || svc.Telemetry.Traffic.State != report.ObservationError || svc.Telemetry.OnlineClients.State != report.ObservationError {
		t.Fatalf("telemetry = %+v", svc.Telemetry)
	}
}

func TestDetectCapabilitiesSuccessfulTelemetryPopulatesLegacyStats(t *testing.T) {
	reg, err := NewRegistry([]Template{{ID: "xray", Units: []string{"xray"}, StatsKind: "xray"}})
	if err != nil {
		t.Fatal(err)
	}
	runner := activeUnitRunner("xray")
	runner.responses["xray api stats query -s 127.0.0.1:10085"] = runnerResponse{out: "uplink 7\ndownlink 9"}
	runner.responses["xray api statsonlineiplist -s 127.0.0.1:10085 -all"] = runnerResponse{out: `{"users":[{"ips":[{"ip":"1.2.3.4"}]}]}`}
	statsCfg := config.StatsConfig{Services: map[string]config.StatsService{
		"xray": {Endpoint: "127.0.0.1:10085"},
	}}
	svcs, err := Detect(context.Background(), reg, config.DetectConfig{}, statsCfg, Deps{Runner: runner, ProcRoot: "/nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	svc := svcs[0]
	if svc.Telemetry == nil || svc.Telemetry.Traffic.State != report.ObservationOK || svc.Telemetry.OnlineClients.State != report.ObservationOK {
		t.Fatalf("telemetry = %+v", svc.Telemetry)
	}
	if svc.Stats == nil || svc.Stats.Tx != 7 || svc.Stats.Rx != 9 || svc.Stats.OnlineClients != 1 {
		t.Fatalf("stats = %+v", svc.Stats)
	}
}

func activeUnitRunner(unit string) scriptedRunner {
	return scriptedRunner{responses: map[string]runnerResponse{
		"systemctl list-unit-files --type=service --no-legend --no-pager":                                       {out: unit + ".service enabled\n"},
		"systemctl show " + unit + " --property=ActiveState,SubState,UnitFileState,NRestarts,MainPID,ExecStart": {out: "ActiveState=active\nUnitFileState=enabled\nMainPID=10"},
	}}
}
