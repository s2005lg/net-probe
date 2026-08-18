package detect

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

type UnitInfo struct {
	Name      string
	Active    bool
	Enabled   bool
	MainPID   int
	NRestarts int
	ExecStart string
}

func ListUnitNames(ctx context.Context, r Runner) ([]string, error) {
	out, err := r.Run(ctx, "systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		names = append(names, f[0])
	}
	return names, nil
}

func ShowUnit(ctx context.Context, r Runner, name string) (UnitInfo, error) {
	out, err := r.Run(ctx, "systemctl", "show", name,
		"--property=ActiveState,SubState,UnitFileState,NRestarts,MainPID,ExecStart")
	if err != nil {
		return UnitInfo{}, err
	}
	u := UnitInfo{Name: name}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "ActiveState":
			u.Active = v == "active"
		case "UnitFileState":
			u.Enabled = v == "enabled"
		case "MainPID":
			u.MainPID, _ = strconv.Atoi(v)
		case "NRestarts":
			u.NRestarts, _ = strconv.Atoi(v)
		case "ExecStart":
			u.ExecStart = parseExecStart(v)
		}
	}
	return u, nil
}

func parseExecStart(v string) string {
	// ExecStart 形如：{ path=/usr/bin/x ; argv[]=/usr/bin/x ... }
	if i := strings.Index(v, "path="); i >= 0 {
		rest := v[i+len("path="):]
		if j := strings.IndexAny(rest, " ;"); j >= 0 {
			return rest[:j]
		}
	}
	return v
}
