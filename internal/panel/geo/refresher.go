package geo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/s2005lg/net-probe/internal/report"
)

const refreshQueueCapacity = 128

type Refresher struct {
	db         *sql.DB
	provider   Provider
	ttl        time.Duration
	queue      chan string
	mu         sync.Mutex
	pending    map[string]struct{}
	processing map[string]struct{}
	dirty      map[string]struct{}
	logf       func(string, ...any)
	now        func() time.Time
}

func NewRefresher(d *sql.DB, provider Provider, ttl time.Duration, logf func(string, ...any)) *Refresher {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Refresher{
		db:         d,
		provider:   provider,
		ttl:        ttl,
		queue:      make(chan string, refreshQueueCapacity),
		pending:    make(map[string]struct{}),
		processing: make(map[string]struct{}),
		dirty:      make(map[string]struct{}),
		logf:       logf,
		now:        time.Now,
	}
}

func (r *Refresher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ip := <-r.queue:
			r.mu.Lock()
			r.processing[ip] = struct{}{}
			r.mu.Unlock()
			if err := r.refreshOne(ctx, ip); err != nil {
				r.logf("geolocation refresh failed for %s", ip)
			}
			r.finishProcessing(ip)
		}
	}
}

func (r *Refresher) ObserveNode(ctx context.Context, nodeID string, host report.Host) error {
	return r.observeNode(ctx, nodeID, host, nil)
}

func (r *Refresher) observeNode(ctx context.Context, nodeID string, host report.Host, expectedHostJSON *string) error {
	where := ` WHERE node_id=?`
	whereArgs := []any{nodeID}
	if expectedHostJSON != nil {
		where += ` AND COALESCE(last_host_json,'{}')=?`
		whereArgs = append(whereArgs, *expectedHostJSON)
	}

	ip := EffectiveIP(host)
	if ip == "" {
		_, err := r.db.ExecContext(ctx, `UPDATE nodes
SET ip_geo_ip='', ip_location='', ip_country='', ip_region='', ip_city='', ip_geo_updated_at=0`+where, whereArgs...)
		return err
	}

	now := r.now()
	if !IsPublicIP(ip) {
		args := append([]any{ip, now.Unix()}, whereArgs...)
		_, err := r.db.ExecContext(ctx, `UPDATE nodes
SET ip_geo_ip=?, ip_location='内网', ip_country='', ip_region='', ip_city='', ip_geo_updated_at=?`+where, args...)
		return err
	}

	var location string
	var updatedAt int64
	args := append([]any{ip, ip, ip, ip, ip, ip}, whereArgs...)
	err := r.db.QueryRowContext(ctx, `UPDATE nodes SET
ip_location=CASE WHEN COALESCE(ip_geo_ip,'')<>? THEN '' ELSE COALESCE(ip_location,'') END,
ip_country=CASE WHEN COALESCE(ip_geo_ip,'')<>? THEN '' ELSE COALESCE(ip_country,'') END,
ip_region=CASE WHEN COALESCE(ip_geo_ip,'')<>? THEN '' ELSE COALESCE(ip_region,'') END,
ip_city=CASE WHEN COALESCE(ip_geo_ip,'')<>? THEN '' ELSE COALESCE(ip_city,'') END,
ip_geo_updated_at=CASE WHEN COALESCE(ip_geo_ip,'')<>? THEN 0 ELSE COALESCE(ip_geo_updated_at,0) END,
ip_geo_ip=?`+where+`
RETURNING COALESCE(ip_location,''), COALESCE(ip_geo_updated_at,0)`, args...).Scan(&location, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) && expectedHostJSON != nil {
		return nil
	}
	if err != nil {
		return err
	}
	if location == "" || updatedAt == 0 || time.Unix(updatedAt, 0).Add(r.ttl).Before(now) {
		r.enqueue(ip)
	}
	return nil
}

func (r *Refresher) Reconcile(ctx context.Context) error {
	nodes, err := r.reconcileNodes(ctx)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		var host report.Host
		if err := json.Unmarshal([]byte(node.hostJSON), &host); err != nil {
			r.logf("skipping invalid host data for node %s", node.nodeID)
			continue
		}
		if err := r.observeNode(ctx, node.nodeID, host, &node.hostJSON); err != nil {
			return fmt.Errorf("observe node %s: %w", node.nodeID, err)
		}
	}
	return nil
}

type reconcileNode struct {
	nodeID   string
	hostJSON string
}

func (r *Refresher) reconcileNodes(ctx context.Context) ([]reconcileNode, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT node_id, COALESCE(last_host_json,'{}') FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []reconcileNode
	for rows.Next() {
		var node reconcileNode
		if err := rows.Scan(&node.nodeID, &node.hostJSON); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (r *Refresher) enqueue(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pending[ip]; ok {
		if _, processing := r.processing[ip]; processing {
			r.dirty[ip] = struct{}{}
		}
		return
	}
	r.pending[ip] = struct{}{}
	select {
	case r.queue <- ip:
	default:
		delete(r.pending, ip)
	}
}

func (r *Refresher) finishProcessing(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.processing, ip)
	if _, rerun := r.dirty[ip]; !rerun {
		delete(r.pending, ip)
		return
	}
	delete(r.dirty, ip)
	select {
	case r.queue <- ip:
	default:
		delete(r.pending, ip)
	}
}

func (r *Refresher) refreshOne(ctx context.Context, ip string) error {
	now := r.now()
	location, loc, updatedAt, found, err := r.cachedLocation(ctx, ip)
	if err != nil {
		return err
	}
	if !found || time.Unix(updatedAt, 0).Add(r.ttl).Before(now) {
		loc, err = r.provider.Lookup(ctx, ip)
		if err != nil {
			return errors.New("geolocation provider lookup failed")
		}
		location = Format(loc)
		updatedAt = now.Unix()
		if _, err := r.db.ExecContext(ctx, `INSERT INTO ip_geo_cache(ip,location,country,region,city,updated_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(ip) DO UPDATE SET
location=excluded.location,
country=excluded.country,
region=excluded.region,
city=excluded.city,
updated_at=excluded.updated_at`, ip, location, loc.Country, loc.RegionName, loc.City, updatedAt); err != nil {
			return err
		}
	}

	_, err = r.db.ExecContext(ctx, `UPDATE nodes
SET ip_location=?, ip_country=?, ip_region=?, ip_city=?, ip_geo_updated_at=?
WHERE ip_geo_ip=?`, location, loc.Country, loc.RegionName, loc.City, updatedAt, ip)
	return err
}

func (r *Refresher) cachedLocation(ctx context.Context, ip string) (string, Location, int64, bool, error) {
	var location string
	var loc Location
	var updatedAt int64
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(location,''), COALESCE(country,''), COALESCE(region,''), COALESCE(city,''), updated_at
FROM ip_geo_cache WHERE ip=?`, ip).Scan(&location, &loc.Country, &loc.RegionName, &loc.City, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Location{}, 0, false, nil
	}
	if err != nil {
		return "", Location{}, 0, false, err
	}
	return location, loc, updatedAt, true, nil
}
