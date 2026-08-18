package retention

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/s2005lg/net-probe/internal/panel/db"
)

func TestAggregate(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if err := db.Migrate(d); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	old := time.Now().AddDate(0, 0, -8).Unix()
	for _, ts := range []int64{old, old + 1800} {
		if _, err := d.Exec(`INSERT INTO metrics(node_id,ts,granularity,load1,load5,load15,mem_used_pct,disk_used_pct,services_json)
			VALUES('n1',?,'raw',0.1,0.2,0.3,12,20,'[]')`, ts); err != nil {
			t.Fatalf("insert raw: %v", err)
		}
	}

	if err := Aggregate(context.Background(), d, 7, 30, 365); err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	var count int
	if err := d.QueryRow(`SELECT count(*) FROM metrics WHERE node_id='n1' AND granularity='hourly'`).Scan(&count); err != nil {
		t.Fatalf("count hourly: %v", err)
	}
	if count != 1 {
		t.Fatalf("hourly count = %d", count)
	}
	if err := d.QueryRow(`SELECT count(*) FROM metrics WHERE node_id='n1' AND granularity='raw'`).Scan(&count); err != nil {
		t.Fatalf("count raw: %v", err)
	}
	if count != 0 {
		t.Fatalf("raw count = %d", count)
	}
}
