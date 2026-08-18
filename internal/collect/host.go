package collect

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/s2005lg/net-probe/internal/config"
	"github.com/s2005lg/net-probe/internal/detect"
	"github.com/s2005lg/net-probe/internal/report"
)

func ParseLoadavg(s string) (float64, float64, float64, error) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return 0, 0, 0, fmt.Errorf("bad loadavg")
	}
	l1, _ := strconv.ParseFloat(f[0], 64)
	l5, _ := strconv.ParseFloat(f[1], 64)
	l15, _ := strconv.ParseFloat(f[2], 64)
	return l1, l5, l15, nil
}

func ParseMeminfo(s string) (uint64, uint64, error) {
	var total, avail uint64
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			avail = v * 1024
		}
	}
	return total, avail, nil
}

func ParseUptime(s string) (int64, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, fmt.Errorf("bad uptime")
	}
	u, err := strconv.ParseFloat(f[0], 64)
	return int64(u), err
}

func ParseOSRelease(s string) (name, version string) {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch k {
		case "PRETTY_NAME":
			name = v
		case "VERSION_ID":
			version = v
		}
	}
	return name, version
}

func ParseDf(s string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("bad df output")
	}
	f := strings.Fields(lines[1])
	if len(f) < 3 {
		return 0, fmt.Errorf("bad df line")
	}
	total, err1 := strconv.ParseUint(f[1], 10, 64)
	used, err2 := strconv.ParseUint(f[2], 10, 64)
	if err1 != nil || err2 != nil || total == 0 {
		return 0, fmt.Errorf("bad df numbers")
	}
	return float64(used) / float64(total) * 100, nil
}

func Host(ctx context.Context, cfg config.CollectConfig, runner detect.Runner) (report.Host, error) {
	h := report.Host{Arch: runtime.GOARCH}
	if hn, err := os.Hostname(); err == nil {
		h.Hostname = hn
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		h.OS, h.OSVersion = ParseOSRelease(string(b))
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		h.Kernel = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		h.Load1, h.Load5, h.Load15, _ = ParseLoadavg(string(b))
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		total, avail, _ := ParseMeminfo(string(b))
		h.MemTotalBytes, h.MemAvailableBytes = total, avail
		if total > 0 {
			h.MemUsedPct = float64(total-avail) / float64(total) * 100
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		h.UptimeSeconds, _ = ParseUptime(string(b))
	}
	if len(cfg.DiskMounts) > 0 && runner != nil {
		if out, err := runner.Run(ctx, "df", "-P", cfg.DiskMounts[0]); err == nil {
			h.DiskUsedPct, _ = ParseDf(out)
		}
	}
	if cfg.Upgradable && runner != nil {
		h.UpgradableCount = upgradableCount(ctx, runner)
	}
	return h, nil
}

func upgradableCount(ctx context.Context, runner detect.Runner) int {
	if out, err := runner.Run(ctx, "apt", "list", "--upgradable"); err == nil {
		n := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "/") {
				n++
			}
		}
		return n
	}
	if out, err := runner.Run(ctx, "dnf", "check-update", "-q"); err == nil {
		n := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		return n
	}
	return 0
}

func Now() string { return time.Now().Format(time.RFC3339) }
