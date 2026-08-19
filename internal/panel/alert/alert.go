package alert

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/config"
	"github.com/s2005lg/net-probe/internal/report"
)

var telegramAPIBase = "https://api.telegram.org"

var httpClient = &http.Client{Timeout: 10 * time.Second}

type alertKey struct {
	NodeID string
	Rule   string
}

type existingAlert struct {
	ID      int64
	Status  string
	Message string
}

func Evaluate(ctx context.Context, d *sql.DB, cfg *config.Config, now time.Time) error {
	timeout, err := time.ParseDuration(cfg.NodeTimeout)
	if err != nil {
		return err
	}
	cutoff := now.Add(-timeout).Unix()

	latest, err := loadLatestVersions(ctx, d)
	if err != nil {
		return err
	}
	existing, err := loadExistingAlerts(ctx, d)
	if err != nil {
		return err
	}

	nodes, err := loadNodes(ctx, d)
	if err != nil {
		return err
	}

	triggered := map[alertKey]string{}
	for _, node := range nodes {
		if node.MutedUntil > 0 && node.MutedUntil > now.Unix() {
			continue
		}

		hostname := hostnameFromJSON(node.HostJSON)

		if node.LastReportAt == 0 || node.LastReportAt < cutoff {
			triggered[alertKey{NodeID: node.NodeID, Rule: "node_offline"}] = nodeMessage(hostname, "节点离线")
			continue
		}

		var host report.Host
		_ = json.Unmarshal([]byte(node.HostJSON), &host)
		var services []report.Service
		_ = json.Unmarshal([]byte(node.ServicesJSON), &services)

		for _, service := range services {
			if !service.Active || service.Status == "error" {
				triggered[alertKey{NodeID: node.NodeID, Rule: "service_down"}] = nodeMessage(hostname, "服务异常")
				break
			}
		}

		for _, service := range services {
			if service.Cert != nil && service.Cert.DaysLeft < cfg.Alert.CertExpiryDays {
				msg := "证书即将到期"
				if service.Cert.DaysLeft >= 0 {
					msg = fmt.Sprintf("证书剩余 %d 天", service.Cert.DaysLeft)
				}
				triggered[alertKey{NodeID: node.NodeID, Rule: "cert_expiry"}] = nodeMessage(hostname, msg)
				break
			}
		}

		if host.DiskUsedPct > float64(cfg.Alert.DiskUsagePct) {
			triggered[alertKey{NodeID: node.NodeID, Rule: "disk_usage"}] = nodeMessage(hostname, fmt.Sprintf("磁盘使用率 %.1f%%", host.DiskUsedPct))
		}
		if host.MemUsedPct > float64(cfg.Alert.MemUsagePct) {
			triggered[alertKey{NodeID: node.NodeID, Rule: "mem_usage"}] = nodeMessage(hostname, fmt.Sprintf("内存使用率 %.1f%%", host.MemUsedPct))
		}

		for _, service := range services {
			want, ok := latest[service.Type]
			if !ok || want == "" || service.Version == "" {
				continue
			}
			if versionLess(service.Version, want) {
				triggered[alertKey{NodeID: node.NodeID, Rule: "version_lag"}] = nodeMessage(hostname, fmt.Sprintf("服务 %s 版本落后：%s < %s", service.Type, service.Version, want))
				break
			}
		}
	}

	var fireNotifications, recoveryNotifications []string
	for key, message := range triggered {
		prev, ok := existing[key]
		if !ok || prev.Status == "recovered" {
			if _, err := d.ExecContext(ctx, `INSERT INTO alerts(node_id,rule,status,message,first_seen_at,last_seen_at)
				VALUES(?,?,'firing',?,?,?)`,
				key.NodeID, key.Rule, message, now.Unix(), now.Unix()); err != nil {
				return err
			}
			fireNotifications = append(fireNotifications, message)
			continue
		}
		if prev.Status == "acknowledged" {
			if _, err := d.ExecContext(ctx, `UPDATE alerts SET last_seen_at=? WHERE id=?`, now.Unix(), prev.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := d.ExecContext(ctx, `UPDATE alerts SET last_seen_at=?, message=? WHERE id=?`, now.Unix(), message, prev.ID); err != nil {
			return err
		}
	}

	for key, prev := range existing {
		if prev.Status != "firing" {
			continue
		}
		if _, stillFiring := triggered[key]; stillFiring {
			continue
		}
		if _, err := d.ExecContext(ctx, `UPDATE alerts SET status='recovered', recovered_at=?, last_seen_at=? WHERE id=?`,
			now.Unix(), now.Unix(), prev.ID); err != nil {
			return err
		}
		recoveryNotifications = append(recoveryNotifications, prev.Message)
	}

	var firstErr error
	for _, text := range fireNotifications {
		if err := notify(cfg, "告警："+text); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, text := range recoveryNotifications {
		if err := notify(cfg, "恢复："+text); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type nodeState struct {
	NodeID       string
	LastReportAt int64
	MutedUntil   int64
	HostJSON     string
	ServicesJSON string
}

func loadNodes(ctx context.Context, d *sql.DB) ([]nodeState, error) {
	rows, err := d.QueryContext(ctx, `SELECT node_id, COALESCE(last_report_at,0), COALESCE(muted_until,0), COALESCE(last_host_json,'{}'), COALESCE(last_services_json,'[]') FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]nodeState, 0)
	for rows.Next() {
		var n nodeState
		if err := rows.Scan(&n.NodeID, &n.LastReportAt, &n.MutedUntil, &n.HostJSON, &n.ServicesJSON); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func loadLatestVersions(ctx context.Context, d *sql.DB) (map[string]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT service_type, latest_version FROM versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var serviceType, latest string
		if err := rows.Scan(&serviceType, &latest); err != nil {
			return nil, err
		}
		out[serviceType] = latest
	}
	return out, rows.Err()
}

func loadExistingAlerts(ctx context.Context, d *sql.DB) (map[alertKey]existingAlert, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, node_id, rule, status, message FROM alerts ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[alertKey]existingAlert{}
	for rows.Next() {
		var a existingAlert
		var key alertKey
		if err := rows.Scan(&a.ID, &key.NodeID, &key.Rule, &a.Status, &a.Message); err != nil {
			return nil, err
		}
		out[key] = a
	}
	return out, rows.Err()
}

func hostnameFromJSON(raw string) string {
	if raw == "" || raw == "{}" {
		return ""
	}
	var host report.Host
	if err := json.Unmarshal([]byte(raw), &host); err != nil {
		return ""
	}
	return host.Hostname
}

func nodeMessage(hostname, message string) string {
	if hostname == "" {
		return message
	}
	return hostname + "：" + message
}

func versionLess(a, b string) bool {
	as := versionSegments(a)
	bs := versionSegments(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av < bv
		}
	}
	return false
}

func versionSegments(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "vV")
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, _ := strconv.Atoi(part)
		out = append(out, n)
	}
	return out
}

func notify(cfg *config.Config, text string) error {
	var firstErr error
	if cfg.Alert.TelegramToken != "" && cfg.Alert.TelegramChatID != "" {
		if err := NotifyTelegram(cfg.Alert.TelegramToken, cfg.Alert.TelegramChatID, text); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if cfg.Alert.WebhookURL != "" {
		if err := NotifyWebhook(cfg.Alert.WebhookURL, text); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func NotifyTelegram(token, chatID, text string) error {
	body := map[string]any{"chat_id": chatID, "text": text}
	return postJSON(telegramAPIBase+"/bot"+token+"/sendMessage", body)
}

func NotifyWebhook(url, text string) error {
	return postJSON(url, map[string]any{"text": text})
}

func postJSON(url string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification target returned %s", resp.Status)
	}
	return nil
}
