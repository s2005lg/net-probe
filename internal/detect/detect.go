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
}

func Detect(ctx context.Context, reg *Registry, cfg config.DetectConfig, deps Deps) ([]report.Service, error) {
	procRoot := deps.ProcRoot
	if procRoot == "" {
		procRoot = "/proc"
	}
	names, err := ListUnitNames(ctx, deps.Runner)
	if err != nil {
		return nil, err
	}
	var out []report.Service
	for _, name := range names {
		unit := strings.TrimSuffix(name, ".service")
		tmpl, ok := reg.FindUnit(unit)
		if !ok {
			continue
		}
		info, err := ShowUnit(ctx, deps.Runner, unit)
		if err != nil {
			out = append(out, report.Service{Type: tmpl.ID, Runtime: "systemd", Unit: unit, Status: "error", Error: err.Error()})
			continue
		}
		svc := report.Service{
			Type:      tmpl.ID,
			Runtime:   "systemd",
			Unit:      unit,
			Binary:    info.ExecStart,
			Active:    info.Active,
			Enabled:   info.Enabled,
			MainPID:   info.MainPID,
			NRestarts: info.NRestarts,
			Status:    "ok",
		}
		if info.ExecStart != "" {
			if v, err := Version(ctx, deps.Runner, info.ExecStart, tmpl.VersionCmd); err == nil {
				svc.Version = v
			}
		}
		socks := readProcSockets(procRoot)
		if info.MainPID > 0 {
			svc.Listen = ListenForPID(procRoot, info.MainPID, socks)
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
		if !svc.Active {
			svc.Status = "error"
			svc.Error = "service not active"
		}
		out = append(out, svc)
	}
	return out, nil
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
