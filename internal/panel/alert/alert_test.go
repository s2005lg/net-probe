package alert

import (
	"context"
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
