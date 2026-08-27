package detect

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/report"
)

type Deps struct {
	Runner   Runner
	ProcRoot string
	Logf     func(format string, args ...any)
}

const certWarnDays = 30

func Detect(ctx context.Context, reg *Registry, cfg config.DetectConfig, statsCfg config.StatsConfig, deps Deps) ([]report.Service, error) {
	procRoot := deps.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	names, err := ListUnitNames(ctx, deps.Runner)
	if err != nil {
		return nil, err
	}
	out := make([]report.Service, 0)
	matched := map[string]bool{}
	for _, name := range names {
		unit := strings.TrimSuffix(name, ".service")
		tmpl, ok := reg.FindUnit(unit)
		if !ok {
			continue
		}
		capabilities := CapabilitiesFor(tmpl.ID)
		matched[unit] = true
		info, err := ShowUnit(ctx, deps.Runner, unit)
		if err != nil {
			out = append(out, report.Service{Type: tmpl.ID, Runtime: "systemd", Unit: unit, Capabilities: &capabilities, Telemetry: commandFailedTelemetry(capabilities), Status: "error", Error: err.Error()})
			continue
		}
		svc := report.Service{
			Type:         tmpl.ID,
			Runtime:      "systemd",
			Unit:         unit,
			Binary:       info.ExecStart,
			Active:       info.Active,
			Enabled:      info.Enabled,
			MainPID:      info.MainPID,
			NRestarts:    info.NRestarts,
			Capabilities: &capabilities,
			Status:       "ok",
		}
		if info.ExecStart != "" {
			if v, err := Version(ctx, deps.Runner, info.ExecStart, tmpl.VersionCmd); err == nil {
				svc.Version = v
			}
		}
		socks := readProcSockets(procRoot)
		if info.MainPID > 0 {
			svc.Listen = ListenForPID(procRoot, info.MainPID, socks)
			if len(svc.Listen) == 0 && deps.Runner != nil {
				svc.Listen = ListenForPIDFromSS(ctx, deps.Runner, info.MainPID)
			}
		}
		svc.ListenOK = len(svc.Listen) > 0
		if len(tmpl.ListenPorts) > 0 {
			svc.ListenOK = hasPorts(svc.Listen, tmpl.ListenPorts)
		}
		for _, p := range tmpl.CertPaths {
			if c, err := CertInfo(p); err == nil {
				svc.Cert = c
				break
			}
		}
		if tmpl.StatsKind != "" {
			if statsDisabled(tmpl, statsCfg) {
				svc.Telemetry = disabledTelemetry(capabilities)
			} else {
				endpoint, secret, ok := statsEndpointFor(tmpl, statsCfg)
				if !ok {
					svc.Telemetry = notConfiguredTelemetry(capabilities)
				} else {
					result := CollectTelemetryWithRunner(ctx, tmpl.StatsKind, endpoint, secret, deps.Runner)
					svc.Telemetry = result.Telemetry
					for _, diagnostic := range result.Diagnostics {
						if deps.Logf != nil {
							deps.Logf("service %s telemetry %s: %v", tmpl.ID, diagnostic.Metric, diagnostic.Err)
						}
					}
				}
			}
			if svc.Telemetry != nil {
				svc.PopulateLegacyStats()
			}
		}
		svc.Status, svc.Error = classifyServiceStatus(svc.Active, svc.Cert, len(tmpl.ListenPorts) > 0, svc.ListenOK)
		out = append(out, svc)
	}
	return out, nil
}

func statsDisabled(tmpl Template, statsCfg config.StatsConfig) bool {
	service, ok := statsCfg.Services[tmpl.StatsKind]
	return ok && service.Enabled != nil && !*service.Enabled
}

func disabledTelemetry(capabilities report.ServiceCapabilities) *report.ServiceTelemetry {
	return telemetryWithState(capabilities, report.ObservationDisabled)
}

func notConfiguredTelemetry(capabilities report.ServiceCapabilities) *report.ServiceTelemetry {
	return telemetryWithState(capabilities, report.ObservationNotConfigured)
}

func commandFailedTelemetry(capabilities report.ServiceCapabilities) *report.ServiceTelemetry {
	telemetry := &report.ServiceTelemetry{}
	if capabilities.Traffic.Support == report.CapabilitySupported {
		telemetry.Traffic = &report.TrafficTelemetry{State: report.ObservationError, ErrorCode: "command_failed"}
	}
	if capabilities.OnlineClients.Support == report.CapabilitySupported {
		telemetry.OnlineClients = &report.CountTelemetry{State: report.ObservationError, ErrorCode: "command_failed"}
	}
	if telemetry.Traffic == nil && telemetry.OnlineClients == nil {
		return nil
	}
	return telemetry
}

func telemetryWithState(capabilities report.ServiceCapabilities, state report.ObservationState) *report.ServiceTelemetry {
	telemetry := &report.ServiceTelemetry{}
	if capabilities.Traffic.Support == report.CapabilitySupported {
		telemetry.Traffic = &report.TrafficTelemetry{State: state}
	}
	if capabilities.OnlineClients.Support == report.CapabilitySupported {
		telemetry.OnlineClients = &report.CountTelemetry{State: state}
	}
	return telemetry
}

func classifyServiceStatus(active bool, cert *report.Cert, hasDeclaredPorts, listenOK bool) (status, errMsg string) {
	if !active {
		return "error", "service not active"
	}
	if cert != nil && cert.DaysLeft < 0 {
		return "error", "certificate expired"
	}
	if cert != nil && cert.DaysLeft < certWarnDays {
		return "warn", ""
	}
	if hasDeclaredPorts && !listenOK {
		return "warn", ""
	}
	return "ok", ""
}

func statsEndpointFor(tmpl Template, statsCfg config.StatsConfig) (string, string, bool) {
	if sc, ok := statsCfg.Services[tmpl.StatsKind]; ok && sc.Endpoint != "" {
		return sc.Endpoint, sc.Secret, true
	}
	if ep, ok := discoverStats(tmpl); ok {
		return ep.Endpoint, ep.Secret, true
	}
	return "", "", false
}

func readProcSockets(root string) []Socket {
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(root, "net", name))
		return string(b)
	}
	s, _ := ParseProcSockets(read("tcp"), read("tcp6"), read("udp"), read("udp6"))
	return s
}

func hasPorts(listen []report.Listen, ports []uint16) bool {
	for _, p := range ports {
		found := false
		for _, l := range listen {
			if l.Port == p {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
