package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/config"
	"github.com/s2005lg/net-probe/internal/panel/db"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func okResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}
}

func TestNodeOffline(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now()
	lastAt := now.Add(-2 * time.Minute).Unix()
	if _, err := d.Exec(`INSERT INTO nodes(node_id,last_report_at,created_at,updated_at) VALUES('n1',?,?,?)`, lastAt, lastAt, lastAt); err != nil {
		t.Fatalf("insert node: %v", err)
	}

	cfg := config.Default()
	cfg.NodeTimeout = "1m"
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	var status string
	if err := d.QueryRow(`SELECT status FROM alerts WHERE node_id='n1' AND rule='node_offline'`).Scan(&status); err != nil {
		t.Fatalf("query alert: %v", err)
	}
	if status != "firing" {
		t.Fatalf("status = %q", status)
	}
}

func TestNotifyWebhook(t *testing.T) {
	var got map[string]any
	old := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/hook" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		return okResponse(), nil
	})}
	defer func() { httpClient = old }()

	if err := NotifyWebhook("https://example.com/hook", "hello"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got["text"] != "hello" {
		t.Fatalf("body = %+v", got)
	}
}

func TestNotifyTelegram(t *testing.T) {
	var got map[string]any
	oldClient := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		return okResponse(), nil
	})}
	defer func() { httpClient = oldClient }()

	if err := NotifyTelegram("tok", "42", "hello"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got["chat_id"] != "42" || got["text"] != "hello" {
		t.Fatalf("body = %+v", got)
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v2.12.1", "2.12.1", false},
		{"v2.11.0", "2.12.1", true},
		{"2.13.0", "2.12.1", false},
		{"v2.9.0", "v2.10.0", true},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Fatalf("versionLess(%q, %q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func openAlertTestDB(t *testing.T) (*sql.DB, *config.Config) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := config.Default()
	cfg.NodeTimeout = "1m"
	return d, cfg
}

func insertAlertNode(t *testing.T, d *sql.DB, nodeID, hostJSON, servicesJSON string, lastAt int64) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO nodes(node_id,last_report_at,last_host_json,last_services_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?)`, nodeID, lastAt, hostJSON, servicesJSON, lastAt, lastAt); err != nil {
		t.Fatalf("insert node: %v", err)
	}
}

func TestServiceDownRule(t *testing.T) {
	d, cfg := openAlertTestDB(t)
	defer d.Close()
	now := time.Now()
	insertAlertNode(t, d, "n1", `{}`, `[{"type":"xray","active":false,"status":"error","listen":[]}]`, now.Unix())
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM alerts WHERE node_id='n1' AND rule='service_down'`).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "firing" {
		t.Fatalf("status=%q", status)
	}
}

func TestCertExpiryRule(t *testing.T) {
	d, cfg := openAlertTestDB(t)
	defer d.Close()
	now := time.Now()
	insertAlertNode(t, d, "n1", `{}`, `[{"type":"hysteria2","active":true,"status":"ok","cert":{"days_left":3},"listen":[]}]`, now.Unix())
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM alerts WHERE node_id='n1' AND rule='cert_expiry'`).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "firing" {
		t.Fatalf("status=%q", status)
	}
}

func TestResourceUsageRules(t *testing.T) {
	d, cfg := openAlertTestDB(t)
	defer d.Close()
	now := time.Now()
	insertAlertNode(t, d, "n1", `{"disk_used_pct":95,"mem_used_pct":95}`, `[]`, now.Unix())
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	for _, rule := range []string{"disk_usage", "mem_usage"} {
		var status string
		if err := d.QueryRow(`SELECT status FROM alerts WHERE node_id='n1' AND rule=?`, rule).Scan(&status); err != nil {
			t.Fatalf("query %s: %v", rule, err)
		}
		if status != "firing" {
			t.Fatalf("%s status=%q", rule, status)
		}
	}
}

func TestVersionLagRule(t *testing.T) {
	d, cfg := openAlertTestDB(t)
	defer d.Close()
	now := time.Now()
	if _, err := d.Exec(`INSERT INTO versions(service_type,latest_version,source,updated_at) VALUES('hysteria2','2.12.1','github',?)`, now.Unix()); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	insertAlertNode(t, d, "n1", `{}`, `[{"type":"hysteria2","active":true,"status":"ok","version":"v2.11.0","listen":[]}]`, now.Unix())
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM alerts WHERE node_id='n1' AND rule='version_lag'`).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "firing" {
		t.Fatalf("status=%q", status)
	}
}

func TestAlertRecovery(t *testing.T) {
	d, cfg := openAlertTestDB(t)
	defer d.Close()
	now := time.Now()
	if _, err := d.Exec(`INSERT INTO alerts(node_id,rule,status,message,first_seen_at,last_seen_at)
		VALUES('n1','node_offline','firing','节点离线',?,?)`, now.Add(-time.Minute).Unix(), now.Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	insertAlertNode(t, d, "n1", `{}`, `[]`, now.Unix())
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM alerts WHERE node_id='n1' AND rule='node_offline'`).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "recovered" {
		t.Fatalf("status=%q", status)
	}
}

func TestAcknowledgedAlertStaysAcknowledged(t *testing.T) {
	d, cfg := openAlertTestDB(t)
	defer d.Close()
	now := time.Now()
	if _, err := d.Exec(`INSERT INTO alerts(node_id,rule,status,message,first_seen_at,last_seen_at,acknowledged_at)
		VALUES('n1','node_offline','acknowledged','节点离线',?,?,?)`, now.Add(-time.Minute).Unix(), now.Add(-time.Minute).Unix(), now.Unix()); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	insertAlertNode(t, d, "n1", `{}`, `[]`, now.Add(-2*time.Minute).Unix())
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM alerts WHERE node_id='n1' AND rule='node_offline'`).Scan(&status); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "acknowledged" {
		t.Fatalf("status=%q", status)
	}
}

func TestNotificationTransitions(t *testing.T) {
	d, cfg := openAlertTestDB(t)
	defer d.Close()
	now := time.Now()
	cfg.Alert.WebhookURL = "https://example.com/hook"

	var bodies []string
	oldClient := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(b, &payload)
		if text, ok := payload["text"].(string); ok {
			bodies = append(bodies, text)
		}
		return okResponse(), nil
	})}
	defer func() { httpClient = oldClient }()

	insertAlertNode(t, d, "n1", `{}`, `[]`, now.Add(-2*time.Minute).Unix())
	if err := Evaluate(context.Background(), d, cfg, now); err != nil {
		t.Fatalf("evaluate fire: %v", err)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], "告警") {
		t.Fatalf("fire bodies=%v", bodies)
	}

	if _, err := d.Exec(`UPDATE nodes SET last_report_at=? WHERE node_id='n1'`, now.Unix()); err != nil {
		t.Fatalf("update node: %v", err)
	}
	if err := Evaluate(context.Background(), d, cfg, now.Add(time.Minute)); err != nil {
		t.Fatalf("evaluate recover: %v", err)
	}
	if len(bodies) != 2 || !strings.Contains(bodies[1], "恢复") {
		t.Fatalf("recovery bodies=%v", bodies)
	}
}
