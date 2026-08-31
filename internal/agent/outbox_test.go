package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutboxEvictsOldestAtBothLimits(t *testing.T) {
	q, err := NewOutbox(t.TempDir(), Limits{MaxItems: 2, MaxBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Put([]byte("first"), []string{"panel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Put([]byte("second"), []string{"panel"}); err != nil {
		t.Fatal(err)
	}
	body := []byte(strings.Repeat("x", 24))
	if _, err := q.Put(body, []string{"panel"}); err != nil {
		t.Fatal(err)
	}
	items, err := q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || string(items[0].Body) != string(body) {
		t.Fatalf("items=%v", items)
	}
	info, err := os.Stat(items[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestOutboxQuarantinesCorruptRecords(t *testing.T) {
	root := t.TempDir()
	q, err := NewOutbox(root, Limits{MaxItems: 120, MaxBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "outbox", "00000000000000000001-bad.json")
	if err := os.WriteFile(bad, []byte(`{"version":"1","id":"bad","body":"eA==","pending":["panel"]} trailing`), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("items=%+v", items)
	}
	entries, err := os.ReadDir(filepath.Join(root, "outbox-corrupt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("quarantine entries=%d", len(entries))
	}
}

func TestOutboxAcknowledgesDestinationsIndependently(t *testing.T) {
	q, err := NewOutbox(t.TempDir(), Limits{MaxItems: 120, MaxBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	id, err := q.Put([]byte(`{"node_id":"n1"}`), []string{"panel", "webhook:0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Ack(id, "panel"); err != nil {
		t.Fatal(err)
	}
	items, err := q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Pending) != 1 || items[0].Pending[0] != "webhook:0" {
		t.Fatalf("items=%+v", items)
	}
	if err := q.Ack(id, "webhook:0"); err != nil {
		t.Fatal(err)
	}
	items, err = q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("items=%+v", items)
	}
}
