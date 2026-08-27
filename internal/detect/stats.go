package detect

import (
	"context"
	"encoding/json"
	"errors"
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

type TelemetryDiagnostic struct {
	Metric string
	Err    error
}

type TelemetryResult struct {
	Telemetry   *report.ServiceTelemetry
	Diagnostics []TelemetryDiagnostic
}

type telemetryError struct {
	code string
	err  error
}

func (e *telemetryError) Error() string { return e.err.Error() }

func (e *telemetryError) Unwrap() error { return e.err }

type httpStatusError struct {
	status int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %d", e.status)
}

func CollectStats(ctx context.Context, kind, endpoint, secret string) (*report.Stats, error) {
	if kind == "xray" {
		return nil, fmt.Errorf("xray stats requires CollectStatsWithRunner")
	}
	return CollectStatsWithRunner(ctx, kind, endpoint, secret, ExecRunner{})
}

func CollectStatsWithRunner(ctx context.Context, kind string, endpoint, secret string, r Runner) (*report.Stats, error) {
	result := CollectTelemetryWithRunner(ctx, kind, endpoint, secret, r)
	if err := legacyStatsError(kind, result); err != nil {
		return nil, err
	}
	return legacyStats(result.Telemetry), nil
}

func CollectTelemetryWithRunner(ctx context.Context, kind, endpoint, secret string, r Runner) TelemetryResult {
	switch kind {
	case "hysteria2":
		return hysteria2Telemetry(ctx, endpoint, secret)
	case "sing-box":
		return singBoxTelemetry(ctx, endpoint, secret)
	case "xray":
		return xrayTelemetry(ctx, r, endpoint)
	default:
		err := &telemetryError{code: "unknown", err: fmt.Errorf("unsupported stats kind %q", kind)}
		return TelemetryResult{
			Telemetry:   &report.ServiceTelemetry{Traffic: observationError(err.code)},
			Diagnostics: []TelemetryDiagnostic{{Metric: "traffic", Err: err}},
		}
	}
}

func hysteria2Telemetry(ctx context.Context, endpoint, secret string) TelemetryResult {
	endpoint = ensureHTTP(endpoint)
	result := TelemetryResult{Telemetry: &report.ServiceTelemetry{}}
	traffic, err := hysteria2Traffic(ctx, endpoint, secret)
	if err != nil {
		result.Telemetry.Traffic = observationError(errorCode(err))
		result.Diagnostics = append(result.Diagnostics, TelemetryDiagnostic{Metric: "traffic", Err: err})
	} else {
		result.Telemetry.Traffic = traffic
	}
	online, err := hysteria2OnlineClients(ctx, endpoint, secret)
	if err != nil {
		result.Telemetry.OnlineClients = countObservationError(errorCode(err))
		result.Diagnostics = append(result.Diagnostics, TelemetryDiagnostic{Metric: "online_clients", Err: err})
	} else {
		result.Telemetry.OnlineClients = online
	}
	return result
}

func hysteria2Traffic(ctx context.Context, endpoint, secret string) (*report.TrafficTelemetry, error) {
	txRx := map[string]struct {
		Tx uint64 `json:"tx"`
		Rx uint64 `json:"rx"`
	}{}
	if err := getStatsJSON(ctx, endpoint+"/traffic", secret, &txRx); err != nil {
		return nil, err
	}
	var tx, rx uint64
	for _, v := range txRx {
		tx += v.Tx
		rx += v.Rx
	}
	return &report.TrafficTelemetry{State: report.ObservationOK, TxBytes: uint64Ptr(tx), RxBytes: uint64Ptr(rx)}, nil
}

func hysteria2OnlineClients(ctx context.Context, endpoint, secret string) (*report.CountTelemetry, error) {
	online := map[string]uint64{}
	if err := getStatsJSON(ctx, endpoint+"/online", secret, &online); err != nil {
		return nil, err
	}
	var total uint64
	for _, n := range online {
		total += n
	}
	return &report.CountTelemetry{State: report.ObservationOK, Value: uint64Ptr(total)}, nil
}

func singBoxTelemetry(ctx context.Context, endpoint, secret string) TelemetryResult {
	endpoint = ensureHTTP(endpoint)
	authHeader := ""
	if secret != "" {
		authHeader = "Bearer " + secret
	}
	v := struct {
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	}{}
	if err := getStatsJSON(ctx, endpoint+"/traffic", authHeader, &v); err != nil {
		return TelemetryResult{
			Telemetry:   &report.ServiceTelemetry{Traffic: observationError(errorCode(err))},
			Diagnostics: []TelemetryDiagnostic{{Metric: "traffic", Err: err}},
		}
	}
	return TelemetryResult{Telemetry: &report.ServiceTelemetry{
		Traffic: &report.TrafficTelemetry{State: report.ObservationOK, TxBytes: uint64Ptr(v.Up), RxBytes: uint64Ptr(v.Down)},
	}}
}

func xrayTelemetry(ctx context.Context, r Runner, server string) TelemetryResult {
	result := TelemetryResult{Telemetry: &report.ServiceTelemetry{}}
	traffic, err := xrayTraffic(ctx, r, server)
	if err != nil {
		result.Telemetry.Traffic = observationError(errorCode(err))
		result.Diagnostics = append(result.Diagnostics, TelemetryDiagnostic{Metric: "traffic", Err: err})
	} else {
		result.Telemetry.Traffic = traffic
	}
	online, err := xrayOnlineClients(ctx, r, server)
	if err != nil {
		result.Telemetry.OnlineClients = countObservationError(errorCode(err))
		result.Diagnostics = append(result.Diagnostics, TelemetryDiagnostic{Metric: "online_clients", Err: err})
	} else {
		result.Telemetry.OnlineClients = online
	}
	return result
}

func xrayTraffic(ctx context.Context, r Runner, server string) (*report.TrafficTelemetry, error) {
	out, err := r.Run(ctx, "xray", "api", "stats", "query", "-s", server)
	if err != nil {
		return nil, &telemetryError{code: "command_failed", err: err}
	}
	var tx, rx uint64
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[len(f)-1], 10, 64)
		if strings.Contains(line, "uplink") {
			tx += v
		}
		if strings.Contains(line, "downlink") {
			rx += v
		}
	}
	return &report.TrafficTelemetry{State: report.ObservationOK, TxBytes: uint64Ptr(tx), RxBytes: uint64Ptr(rx)}, nil
}

func xrayOnlineClients(ctx context.Context, r Runner, server string) (*report.CountTelemetry, error) {
	out, err := r.Run(ctx, "xray", "api", "statsonlineiplist", "-s", server, "-all")
	if err != nil {
		return nil, &telemetryError{code: "command_failed", err: err}
	}
	var resp struct {
		Users []struct {
			IPs []struct {
				IP string `json:"ip"`
			} `json:"ips"`
		} `json:"users"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, &telemetryError{code: "invalid_response", err: err}
	}
	var total uint64
	for _, u := range resp.Users {
		total += uint64(len(u.IPs))
	}
	return &report.CountTelemetry{State: report.ObservationOK, Value: uint64Ptr(total)}, nil
}

func observationError(code string) *report.TrafficTelemetry {
	return &report.TrafficTelemetry{State: report.ObservationError, ErrorCode: code}
}

func countObservationError(code string) *report.CountTelemetry {
	return &report.CountTelemetry{State: report.ObservationError, ErrorCode: code}
}

func uint64Ptr(v uint64) *uint64 { return &v }

func errorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		if statusErr.status == http.StatusUnauthorized || statusErr.status == http.StatusForbidden {
			return "unauthorized"
		}
		return "connection_failed"
	}
	var collectorErr *telemetryError
	if errors.As(err, &collectorErr) {
		return collectorErr.code
	}
	return "unknown"
}

func legacyStatsError(kind string, result TelemetryResult) error {
	if len(result.Diagnostics) == 0 {
		return nil
	}
	if kind == "xray" && result.Telemetry.Traffic != nil && result.Telemetry.Traffic.State == report.ObservationOK {
		return nil
	}
	return result.Diagnostics[0].Err
}

func legacyStats(telemetry *report.ServiceTelemetry) *report.Stats {
	stats := &report.Stats{}
	if telemetry.Traffic != nil && telemetry.Traffic.State == report.ObservationOK {
		stats.Tx = *telemetry.Traffic.TxBytes
		stats.Rx = *telemetry.Traffic.RxBytes
	}
	if telemetry.OnlineClients != nil && telemetry.OnlineClients.State == report.ObservationOK {
		stats.OnlineClients = *telemetry.OnlineClients.Value
	}
	return stats
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

func getStatsJSON(ctx context.Context, url, authHeader string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &telemetryError{code: "invalid_response", err: err}
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		return &telemetryError{code: "connection_failed", err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return &httpStatusError{status: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &telemetryError{code: "invalid_response", err: err}
	}
	return nil
}
