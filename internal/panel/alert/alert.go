package alert

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/config"
)

var telegramAPIBase = "https://api.telegram.org"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func Evaluate(ctx context.Context, d *sql.DB, cfg *config.Config, now time.Time) error {
	timeout, err := time.ParseDuration(cfg.NodeTimeout)
	if err != nil {
		return err
	}
	cutoff := now.Add(-timeout).Unix()
	rows, err := d.QueryContext(ctx, `SELECT node_id FROM nodes WHERE COALESCE(last_report_at,0) < ? AND (COALESCE(muted_until,0)=0 OR COALESCE(muted_until,0)<?)`, cutoff, now.Unix())
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return err
		}
		if _, err := d.ExecContext(ctx, `INSERT INTO alerts(node_id,rule,status,message,first_seen_at,last_seen_at)
			VALUES(?,'node_offline','firing','节点离线',?,?)
			ON CONFLICT DO NOTHING`, nodeID, now.Unix(), now.Unix()); err != nil {
			return err
		}
	}
	return rows.Err()
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
