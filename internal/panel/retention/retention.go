package retention

import (
	"context"
	"database/sql"
	"time"
)

func Aggregate(ctx context.Context, d *sql.DB, rawDays, hourlyDays, dailyDays int) error {
	now := time.Now()

	rawCutoff := now.AddDate(0, 0, -rawDays).Unix()
	if err := insertAggregate(ctx, d, "hourly", rawCutoff, 3600); err != nil {
		return err
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM metrics WHERE granularity='raw' AND ts < ?`, rawCutoff); err != nil {
		return err
	}

	hourlyCutoff := now.AddDate(0, 0, -hourlyDays).Unix()
	if err := insertAggregate(ctx, d, "daily", hourlyCutoff, 86400); err != nil {
		return err
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM metrics WHERE granularity='hourly' AND ts < ?`, hourlyCutoff); err != nil {
		return err
	}

	dailyCutoff := now.AddDate(0, 0, -dailyDays).Unix()
	_, err := d.ExecContext(ctx, `DELETE FROM metrics WHERE granularity='daily' AND ts < ?`, dailyCutoff)
	return err
}

func insertAggregate(ctx context.Context, d *sql.DB, target string, cutoff, bucket int64) error {
	source := "raw"
	if target == "daily" {
		source = "hourly"
	}
	_, err := d.ExecContext(ctx, `INSERT INTO metrics(node_id,ts,granularity,load1,load5,load15,mem_used_pct,disk_used_pct,services_json)
		SELECT node_id, (ts/?)*?, ?, avg(load1), avg(load5), avg(load15), avg(mem_used_pct), avg(disk_used_pct), ''
		FROM metrics WHERE granularity=? AND ts < ? GROUP BY node_id, (ts/?)*?`,
		bucket, bucket, target, source, cutoff, bucket, bucket)
	return err
}
