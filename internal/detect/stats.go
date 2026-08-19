package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/s2005lg/net-probe/internal/report"
	"gopkg.in/yaml.v3"
)

type StatsEndpoint struct {
	Endpoint string
	Secret   string
}

func CollectStats(ctx context.Context, kind, endpoint, secret string) (*report.Stats, error) {
	switch kind {
	case "hysteria2":
		return hysteria2Stats(ctx, endpoint, secret)
	case "sing-box":
		return singBoxStats(ctx, endpoint, secret)
	case "xray":
		return nil, fmt.Errorf("xray stats requires CollectStatsWithRunner")
	default:
		return nil, fmt.Errorf("unsupported stats kind %q", kind)
	}
}

func CollectStatsWithRunner(ctx context.Context, kind string, endpoint, secret string, r Runner) (*report.Stats, error) {
	switch kind {
	case "hysteria2":
		return hysteria2Stats(ctx, endpoint, secret)
	case "sing-box":
		return singBoxStats(ctx, endpoint, secret)
	case "xray":
		return xrayStats(ctx, r, endpoint)
	default:
		return nil, fmt.Errorf("unsupported stats kind %q", kind)
	}
}

func hysteria2Stats(ctx context.Context, endpoint, secret string) (*report.Stats, error) {
	endpoint = ensureHTTP(endpoint)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	txRx := map[string]struct {
		Tx uint64 `json:"tx"`
		Rx uint64 `json:"rx"`
	}{}
	if err := getJSON(ctx, client, endpoint+"/traffic", secret, &txRx); err != nil {
		return nil, err
	}
	online := map[string]uint64{}
	if err := getJSON(ctx, client, endpoint+"/online", secret, &online); err != nil {
		return nil, err
	}
	s := &report.Stats{}
	for _, v := range txRx {
		s.Tx += v.Tx
		s.Rx += v.Rx
	}
	for _, n := range online {
		s.OnlineClients += n
	}
	return s, nil
}

func singBoxStats(ctx context.Context, endpoint, secret string) (*report.Stats, error) {
	endpoint = ensureHTTP(endpoint)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	authHeader := ""
	if secret != "" {
		authHeader = "Bearer " + secret
	}
	v := struct {
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	}{}
	if err := getJSON(ctx, client, endpoint+"/traffic", authHeader, &v); err != nil {
		return nil, err
	}
	return &report.Stats{Tx: v.Up, Rx: v.Down}, nil
}

func xrayStats(ctx context.Context, r Runner, server string) (*report.Stats, error) {
	out, err := r.Run(ctx, "xray", "api", "stats", "query", "-s", server)
	if err != nil {
		return nil, err
	}
	s := &report.Stats{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[len(f)-1], 10, 64)
		if strings.Contains(line, "uplink") {
			s.Tx += v
		}
		if strings.Contains(line, "downlink") {
			s.Rx += v
		}
	}
	if online, err := xrayOnlineClients(ctx, r, server); err == nil {
		s.OnlineClients = online
	}
	return s, nil
}

func xrayOnlineClients(ctx context.Context, r Runner, server string) (uint64, error) {
	out, err := r.Run(ctx, "xray", "api", "statsonlineiplist", "-s", server, "-all")
	if err != nil {
		return 0, err
	}
	var resp struct {
		Users []struct {
			IPs []struct {
				IP string `json:"ip"`
			} `json:"ips"`
		} `json:"users"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return 0, err
	}
	var total uint64
	for _, u := range resp.Users {
		total += uint64(len(u.IPs))
	}
	return total, nil
}

func ensureHTTP(endpoint string) string {
	if strings.Contains(endpoint, "://") {
		return endpoint
	}
	return "http://" + endpoint
}

func discoverStats(tmpl Template) (StatsEndpoint, bool) {
	for _, p := range tmpl.StatsConfigPaths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal(b, &m); err != nil {
			continue
		}
		switch tmpl.StatsKind {
		case "hysteria2":
			ts, _ := m["trafficStats"].(map[string]any)
			if ts == nil {
				continue
			}
			listen, _ := ts["listen"].(string)
			secret, _ := ts["secret"].(string)
			if listen != "" {
				return StatsEndpoint{Endpoint: listen, Secret: secret}, true
			}
		case "sing-box":
			exp, _ := m["experimental"].(map[string]any)
			if exp == nil {
				continue
			}
			clash, _ := exp["clash_api"].(map[string]any)
			if clash == nil {
				continue
			}
			ctrl, _ := clash["external_controller"].(string)
			secret, _ := clash["secret"].(string)
			if ctrl != "" {
				return StatsEndpoint{Endpoint: ctrl, Secret: secret}, true
			}
		}
	}
	return StatsEndpoint{}, false
}

func getJSON(ctx context.Context, client *http.Client, url, authHeader string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("stats %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
