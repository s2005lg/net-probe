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
	"github.com/s2005lg/net-probe/internal/retrybackoff"
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

	beforeObserve      func(string, report.Host)
	afterReconcileLoad func()
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
	hostJSON, err := json.Marshal(host)
	if err != nil {
		return err
	}
	if r.beforeObserve != nil {
		r.beforeObserve(nodeID, host)
	}
	expectedHostJSON := string(hostJSON)
	return r.observeNode(ctx, nodeID, host, &expectedHostJSON)
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
	if r.afterReconcileLoad != nil {
		r.afterReconcileLoad()
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
	if _, ok := r.pending[ip]; ok {
		if _, processing := r.processing[ip]; processing {
			r.dirty[ip] = struct{}{}
		}
		r.mu.Unlock()
		return
	}
	r.pending[ip] = struct{}{}
	select {
	case r.queue <- ip:
		r.mu.Unlock()
	default:
		delete(r.pending, ip)
		r.mu.Unlock()
		r.logf("geolocation refresh queue full; refresh deferred")
	}
}

func (r *Refresher) finishProcessing(ip string) {
	r.mu.Lock()
	delete(r.processing, ip)
	if _, rerun := r.dirty[ip]; !rerun {
		delete(r.pending, ip)
		r.mu.Unlock()
		return
	}
	delete(r.dirty, ip)
	select {
	case r.queue <- ip:
		r.mu.Unlock()
	default:
		delete(r.pending, ip)
		r.mu.Unlock()
		r.logf("geolocation refresh queue full; refresh deferred")
	}
}

func (r *Refresher) refreshOne(ctx context.Context, ip string) error {
	now := r.now()
	cached, found, err := r.cachedLocation(ctx, ip)
	if err != nil {
		return err
	}
	if found && cached.UpdatedAt > 0 {
		if err := r.applyLocation(ctx, ip, cached); err != nil {
			return err
		}
		if !time.Unix(cached.UpdatedAt, 0).Add(r.ttl).Before(now) {
			return nil
		}
	}
	if found && cached.RetryAt > now.Unix() {
		return nil
	}

	loc, err := r.provider.Lookup(ctx, ip)
	if err != nil {
		if err := r.persistFailure(ctx, ip, cached, now); err != nil {
			return err
		}
		return errors.New("geolocation provider lookup failed")
	}
	cached = geoCacheEntry{
		Location:      Format(loc),
		LocationParts: loc,
		UpdatedAt:     now.Unix(),
		LastAttemptAt: now.Unix(),
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO ip_geo_cache(ip,location,country,region,city,updated_at,last_attempt_at,failure_count,retry_at)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(ip) DO UPDATE SET
location=excluded.location,
country=excluded.country,
region=excluded.region,
city=excluded.city,
updated_at=excluded.updated_at,
last_attempt_at=excluded.last_attempt_at,
failure_count=0,
retry_at=0`, ip, cached.Location, loc.Country, loc.RegionName, loc.City, cached.UpdatedAt, cached.LastAttemptAt, 0, 0); err != nil {
		return err
	}
	return r.applyLocation(ctx, ip, cached)
}

type geoCacheEntry struct {
	Location      string
	LocationParts Location
	UpdatedAt     int64
	LastAttemptAt int64
	FailureCount  int
	RetryAt       int64
}

func (r *Refresher) applyLocation(ctx context.Context, ip string, cached geoCacheEntry) error {
	_, err := r.db.ExecContext(ctx, `UPDATE nodes
SET ip_location=?, ip_country=?, ip_region=?, ip_city=?, ip_geo_updated_at=?
WHERE ip_geo_ip=?`, cached.Location, cached.LocationParts.Country, cached.LocationParts.RegionName, cached.LocationParts.City, cached.UpdatedAt, ip)
	return err
}

func (r *Refresher) persistFailure(ctx context.Context, ip string, cached geoCacheEntry, now time.Time) error {
	if cached.FailureCount < 32 {
		cached.FailureCount++
	}
	cached.LastAttemptAt = now.Unix()
	cached.RetryAt = now.Add(retrybackoff.Delay(cached.FailureCount)).Unix()
	_, err := r.db.ExecContext(ctx, `INSERT INTO ip_geo_cache(ip,location,country,region,city,updated_at,last_attempt_at,failure_count,retry_at)
VALUES(?,'','','','',0,?,?,?)
ON CONFLICT(ip) DO UPDATE SET
last_attempt_at=excluded.last_attempt_at,
failure_count=excluded.failure_count,
retry_at=excluded.retry_at`, ip, cached.LastAttemptAt, cached.FailureCount, cached.RetryAt)
	return err
}

func (r *Refresher) cachedLocation(ctx context.Context, ip string) (geoCacheEntry, bool, error) {
	var cached geoCacheEntry
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(location,''), COALESCE(country,''), COALESCE(region,''), COALESCE(city,''), updated_at, last_attempt_at, failure_count, retry_at
FROM ip_geo_cache WHERE ip=?`, ip).Scan(&cached.Location, &cached.LocationParts.Country, &cached.LocationParts.RegionName, &cached.LocationParts.City, &cached.UpdatedAt, &cached.LastAttemptAt, &cached.FailureCount, &cached.RetryAt)
	if errors.Is(err, sql.ErrNoRows) {
		return geoCacheEntry{}, false, nil
	}
	if err != nil {
		return geoCacheEntry{}, false, err
	}
	return cached, true, nil
}
