package geo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	paneldb "github.com/s2005lg/net-probe/internal/panel/db"
	"github.com/s2005lg/net-probe/internal/report"
)

type fakeProvider struct {
	mu        sync.Mutex
	locations map[string]Location
	errors    map[string]error
	calls     []string
	block     map[string]chan struct{}
	started   map[string]chan struct{}
}

type retryProvider struct {
	mu       sync.Mutex
	calls    int
	location Location
}

func (p *retryProvider) Lookup(context.Context, string) (Location, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		return Location{}, errors.New("first lookup failed")
	}
	return p.location, nil
}

func (p *retryProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeProvider) Lookup(_ context.Context, ip string) (Location, error) {
	p.mu.Lock()
	p.calls = append(p.calls, ip)
	block := p.block[ip]
	if started := p.started[ip]; started != nil {
		close(started)
		delete(p.started, ip)
	}
	loc, ok := p.locations[ip]
	err := p.errors[ip]
	p.mu.Unlock()

	if block != nil {
		<-block
	}
	if err != nil {
		return Location{}, err
	}
	if !ok {
		return Location{}, errors.New("not found")
	}
	return loc, nil
}

func (p *fakeProvider) callSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func openGeoTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := paneldb.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := paneldb.Migrate(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func insertNode(t *testing.T, d *sql.DB, nodeID string, host report.Host) {
	t.Helper()
	b, err := json.Marshal(host)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := d.Exec(`INSERT INTO nodes(node_id,last_host_json,last_services_json,created_at,updated_at) VALUES(?,?,'[]',?,?)`, nodeID, string(b), now, now); err != nil {
		t.Fatal(err)
	}
}

func persistNodeHost(t *testing.T, d *sql.DB, nodeID string, host report.Host) string {
	t.Helper()
	b, err := json.Marshal(host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE nodes SET last_host_json=? WHERE node_id=?`, string(b), nodeID); err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestObserveNodeQueuesNewPublicIP(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	p := &fakeProvider{locations: map[string]Location{"8.8.8.8": {Country: "美国", City: "山景城"}}}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "8.8.8.8"}); err != nil {
		t.Fatal(err)
	}
	select {
	case ip := <-r.queue:
		if ip != "8.8.8.8" {
			t.Fatalf("queued IP = %q", ip)
		}
	default:
		t.Fatal("new public IP was not queued")
	}
	if err := r.refreshOne(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	var geoIP, location string
	if err := d.QueryRow(`SELECT COALESCE(ip_geo_ip,''), COALESCE(ip_location,'') FROM nodes WHERE node_id='n1'`).Scan(&geoIP, &location); err != nil {
		t.Fatal(err)
	}
	if geoIP != "8.8.8.8" || location != "美国-山景城" {
		t.Fatalf("geoIP=%q location=%q", geoIP, location)
	}
	var cachedLocation string
	if err := d.QueryRow(`SELECT location FROM ip_geo_cache WHERE ip='8.8.8.8'`).Scan(&cachedLocation); err != nil {
		t.Fatal(err)
	}
	if cachedLocation != "美国-山景城" {
		t.Fatalf("cached location = %q", cachedLocation)
	}
}

func TestRefreshOneReusesPersistentCache(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='8.8.8.8' WHERE node_id='n1'`); err != nil {
		t.Fatal(err)
	}
	_, err := d.Exec(`INSERT INTO ip_geo_cache(ip,location,country,region,city,updated_at) VALUES(?,?,?,?,?,?)`,
		"8.8.8.8", "缓存显示值", "美国", "加利福尼亚州", "山景城", time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{locations: map[string]Location{}}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.refreshOne(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	if calls := p.callSnapshot(); len(calls) != 0 {
		t.Fatalf("provider calls = %v", calls)
	}
	var location, country, region, city string
	if err := d.QueryRow(`SELECT COALESCE(ip_location,''), COALESCE(ip_country,''), COALESCE(ip_region,''), COALESCE(ip_city,'') FROM nodes WHERE node_id='n1'`).Scan(&location, &country, &region, &city); err != nil {
		t.Fatal(err)
	}
	if location != "缓存显示值" || country != "美国" || region != "加利福尼亚州" || city != "山景城" {
		t.Fatalf("location fields = %q %q %q %q", location, country, region, city)
	}
}

func TestObserveNodeMarksPrivateAndLoopbackAddressesInternal(t *testing.T) {
	tests := []struct {
		name string
		host report.Host
		want string
	}{
		{name: "legacy private IPv4", host: report.Host{IPv4: "10.0.0.7"}, want: "10.0.0.7"},
		{name: "loopback IPv4", host: report.Host{EgressIPv4: "127.0.0.1"}, want: "127.0.0.1"},
		{name: "loopback IPv6", host: report.Host{EgressIPv6: "::1"}, want: "::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := openGeoTestDB(t)
			insertNode(t, d, "n1", tt.host)
			p := &fakeProvider{locations: map[string]Location{}}
			r := NewRefresher(d, p, 12*time.Hour, nil)
			now := time.Unix(1_800_000_000, 0)
			r.now = func() time.Time { return now }
			if err := r.ObserveNode(context.Background(), "n1", tt.host); err != nil {
				t.Fatal(err)
			}
			var geoIP, location, country, region, city string
			var updatedAt int64
			if err := d.QueryRow(`SELECT COALESCE(ip_geo_ip,''), COALESCE(ip_location,''), COALESCE(ip_country,''), COALESCE(ip_region,''), COALESCE(ip_city,''), COALESCE(ip_geo_updated_at,0) FROM nodes WHERE node_id='n1'`).Scan(&geoIP, &location, &country, &region, &city, &updatedAt); err != nil {
				t.Fatal(err)
			}
			if geoIP != tt.want || location != "内网" || country != "" || region != "" || city != "" || updatedAt != now.Unix() {
				t.Fatalf("geography = %q %q %q %q %q %d", geoIP, location, country, region, city, updatedAt)
			}
			if calls := p.callSnapshot(); len(calls) != 0 {
				t.Fatalf("provider calls = %v", calls)
			}
			if len(r.queue) != 0 {
				t.Fatalf("queue length = %d", len(r.queue))
			}
		})
	}
}

func TestObserveNodeWithNoAddressClearsGeography(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{})
	if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='8.8.8.8', ip_location='美国-山景城', ip_country='美国', ip_region='加利福尼亚州', ip_city='山景城', ip_geo_updated_at=123 WHERE node_id='n1'`); err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{locations: map[string]Location{}}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.ObserveNode(context.Background(), "n1", report.Host{}); err != nil {
		t.Fatal(err)
	}
	var geoIP, location, country, region, city string
	var updatedAt int64
	if err := d.QueryRow(`SELECT COALESCE(ip_geo_ip,''), COALESCE(ip_location,''), COALESCE(ip_country,''), COALESCE(ip_region,''), COALESCE(ip_city,''), COALESCE(ip_geo_updated_at,0) FROM nodes WHERE node_id='n1'`).Scan(&geoIP, &location, &country, &region, &city, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if geoIP != "" || location != "" || country != "" || region != "" || city != "" || updatedAt != 0 {
		t.Fatalf("geography = %q %q %q %q %q %d", geoIP, location, country, region, city, updatedAt)
	}
	if calls := p.callSnapshot(); len(calls) != 0 {
		t.Fatalf("provider calls = %v", calls)
	}
}

func TestObserveNodeIPChangeClearsOldLocationImmediately(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "1.1.1.1"})
	if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='8.8.8.8', ip_location='美国-山景城', ip_country='美国', ip_region='加利福尼亚州', ip_city='山景城', ip_geo_updated_at=123 WHERE node_id='n1'`); err != nil {
		t.Fatal(err)
	}
	r := NewRefresher(d, &fakeProvider{locations: map[string]Location{}}, 12*time.Hour, nil)
	if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "1.1.1.1"}); err != nil {
		t.Fatal(err)
	}
	var geoIP, location, country, region, city string
	var updatedAt int64
	if err := d.QueryRow(`SELECT COALESCE(ip_geo_ip,''), COALESCE(ip_location,''), COALESCE(ip_country,''), COALESCE(ip_region,''), COALESCE(ip_city,''), COALESCE(ip_geo_updated_at,0) FROM nodes WHERE node_id='n1'`).Scan(&geoIP, &location, &country, &region, &city, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if geoIP != "1.1.1.1" || location != "" || country != "" || region != "" || city != "" || updatedAt != 0 {
		t.Fatalf("geography = %q %q %q %q %q %d", geoIP, location, country, region, city, updatedAt)
	}
}

func TestObserveNodeSnapshotCannotOverwriteNewerReport(t *testing.T) {
	d := openGeoTestDB(t)
	oldHost := report.Host{EgressIPv4: "8.8.8.8"}
	insertNode(t, d, "n1", oldHost)
	r := NewRefresher(d, &fakeProvider{locations: map[string]Location{}}, 12*time.Hour, nil)
	paused := make(chan struct{})
	resume := make(chan struct{})
	r.beforeObserve = func(_ string, host report.Host) {
		if EffectiveIP(host) == "8.8.8.8" {
			close(paused)
			<-resume
		}
	}

	oldDone := make(chan error, 1)
	go func() { oldDone <- r.ObserveNode(context.Background(), "n1", oldHost) }()
	select {
	case <-paused:
	case <-time.After(time.Second):
		close(resume)
		<-oldDone
		t.Fatal("old observation did not pause before its update")
	}

	newHost := report.Host{EgressIPv4: "1.1.1.1"}
	newJSON := persistNodeHost(t, d, "n1", newHost)
	if err := r.ObserveNode(context.Background(), "n1", newHost); err != nil {
		close(resume)
		<-oldDone
		t.Fatal(err)
	}
	close(resume)
	if err := <-oldDone; err != nil {
		t.Fatal(err)
	}

	var hostJSON, geoIP string
	if err := d.QueryRow(`SELECT COALESCE(last_host_json,''), COALESCE(ip_geo_ip,'') FROM nodes WHERE node_id='n1'`).Scan(&hostJSON, &geoIP); err != nil {
		t.Fatal(err)
	}
	if hostJSON != newJSON || geoIP != "1.1.1.1" {
		t.Fatalf("last_host_json=%q ip_geo_ip=%q", hostJSON, geoIP)
	}
}

func TestObserveNodeQueuesMissingAndStaleGeographyButNotFresh(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name      string
		location  string
		updatedAt int64
		wantQueue int
	}{
		{name: "missing", location: "", updatedAt: now.Unix(), wantQueue: 1},
		{name: "stale", location: "美国-山景城", updatedAt: now.Add(-13 * time.Hour).Unix(), wantQueue: 1},
		{name: "fresh", location: "美国-山景城", updatedAt: now.Add(-11 * time.Hour).Unix(), wantQueue: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := openGeoTestDB(t)
			insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
			if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='8.8.8.8', ip_location=?, ip_geo_updated_at=? WHERE node_id='n1'`, tt.location, tt.updatedAt); err != nil {
				t.Fatal(err)
			}
			r := NewRefresher(d, &fakeProvider{locations: map[string]Location{}}, 12*time.Hour, nil)
			r.now = func() time.Time { return now }
			if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "8.8.8.8"}); err != nil {
				t.Fatal(err)
			}
			if len(r.queue) != tt.wantQueue {
				t.Fatalf("queue length = %d, want %d", len(r.queue), tt.wantQueue)
			}
		})
	}
}

func TestRefreshOneFailurePreservesSameIPLocation(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='8.8.8.8', ip_location='美国-山景城', ip_country='美国', ip_region='加利福尼亚州', ip_city='山景城', ip_geo_updated_at=123 WHERE node_id='n1'`); err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{locations: map[string]Location{}, errors: map[string]error{"8.8.8.8": errors.New("token=secret response body")}}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.refreshOne(context.Background(), "8.8.8.8"); err == nil {
		t.Fatal("refreshOne unexpectedly succeeded")
	} else if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "response body") {
		t.Fatalf("provider detail leaked in error: %v", err)
	}
	var location, country, region, city string
	var updatedAt int64
	if err := d.QueryRow(`SELECT COALESCE(ip_location,''), COALESCE(ip_country,''), COALESCE(ip_region,''), COALESCE(ip_city,''), COALESCE(ip_geo_updated_at,0) FROM nodes WHERE node_id='n1'`).Scan(&location, &country, &region, &city, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if location != "美国-山景城" || country != "美国" || region != "加利福尼亚州" || city != "山景城" || updatedAt != 123 {
		t.Fatalf("location fields = %q %q %q %q %d", location, country, region, city, updatedAt)
	}
}

func TestRefreshOneFansOutOnlyToMatchingNodes(t *testing.T) {
	d := openGeoTestDB(t)
	for _, nodeID := range []string{"n1", "n2", "n3"} {
		insertNode(t, d, nodeID, report.Host{})
	}
	if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='8.8.8.8' WHERE node_id IN ('n1','n2')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='1.1.1.1', ip_location='澳大利亚-悉尼' WHERE node_id='n3'`); err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{locations: map[string]Location{"8.8.8.8": {Country: "美国", RegionName: "加利福尼亚州", City: "山景城"}}}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.refreshOne(context.Background(), "8.8.8.8"); err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []string{"n1", "n2"} {
		var location string
		if err := d.QueryRow(`SELECT COALESCE(ip_location,'') FROM nodes WHERE node_id=?`, nodeID).Scan(&location); err != nil {
			t.Fatal(err)
		}
		if location != "美国-山景城" {
			t.Fatalf("%s location = %q", nodeID, location)
		}
	}
	var unmatched string
	if err := d.QueryRow(`SELECT COALESCE(ip_location,'') FROM nodes WHERE node_id='n3'`).Scan(&unmatched); err != nil {
		t.Fatal(err)
	}
	if unmatched != "澳大利亚-悉尼" {
		t.Fatalf("unmatched location = %q", unmatched)
	}
}

func TestQueueDeduplicatesPendingIP(t *testing.T) {
	r := NewRefresher(nil, nil, 12*time.Hour, nil)
	r.enqueue("8.8.8.8")
	r.enqueue("8.8.8.8")
	if len(r.queue) != 1 {
		t.Fatalf("queue length = %d", len(r.queue))
	}
	r.mu.Lock()
	_, pending := r.pending["8.8.8.8"]
	r.mu.Unlock()
	if !pending {
		t.Fatal("queued IP is not marked pending")
	}
}

func TestQueueSaturationIsNonBlockingAndRetryable(t *testing.T) {
	r := NewRefresher(nil, nil, 12*time.Hour, nil)
	for i := 0; i < cap(r.queue); i++ {
		r.enqueue(fmt.Sprintf("ip-%d", i))
	}
	done := make(chan struct{})
	go func() {
		r.enqueue("overflow")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked on a full queue")
	}
	r.mu.Lock()
	_, pending := r.pending["overflow"]
	r.mu.Unlock()
	if pending {
		t.Fatal("overflow IP retained a pending marker")
	}
	<-r.queue
	r.enqueue("overflow")
	if len(r.queue) != cap(r.queue) {
		t.Fatalf("queue length after retry = %d", len(r.queue))
	}
	r.mu.Lock()
	_, pending = r.pending["overflow"]
	r.mu.Unlock()
	if !pending {
		t.Fatal("overflow IP was not queued after capacity became available")
	}
}

func TestOldLookupCannotOverwriteNewIP(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	if _, err := d.Exec(`UPDATE nodes SET ip_geo_ip='8.8.8.8' WHERE node_id='n1'`); err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	started := make(chan struct{})
	p := &fakeProvider{
		locations: map[string]Location{"8.8.8.8": {Country: "美国", City: "山景城"}},
		block:     map[string]chan struct{}{"8.8.8.8": block},
		started:   map[string]chan struct{}{"8.8.8.8": started},
	}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- r.refreshOne(context.Background(), "8.8.8.8") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(block)
		t.Fatal("old lookup did not start")
	}
	persistNodeHost(t, d, "n1", report.Host{EgressIPv4: "1.1.1.1"})
	if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "1.1.1.1"}); err != nil {
		close(block)
		t.Fatal(err)
	}
	close(block)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	var geoIP, location string
	if err := d.QueryRow(`SELECT COALESCE(ip_geo_ip,''), COALESCE(ip_location,'') FROM nodes WHERE node_id='n1'`).Scan(&geoIP, &location); err != nil {
		t.Fatal(err)
	}
	if geoIP != "1.1.1.1" || location != "" {
		t.Fatalf("geoIP=%q location=%q", geoIP, location)
	}
}

func TestRunProcessesOneQueuedLookup(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	started := make(chan struct{})
	p := &fakeProvider{
		locations: map[string]Location{"8.8.8.8": {Country: "美国", City: "山景城"}},
		started:   map[string]chan struct{}{"8.8.8.8": started},
	}
	r := NewRefresher(d, p, 12*time.Hour, nil)
	if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "8.8.8.8"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(runDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("worker did not start provider lookup")
	}
	deadline := time.Now().Add(time.Second)
	for {
		var location string
		if err := d.QueryRow(`SELECT COALESCE(ip_location,'') FROM nodes WHERE node_id='n1'`).Scan(&location); err != nil {
			cancel()
			t.Fatal(err)
		}
		if location == "美国-山景城" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("worker did not update the node")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
	r.mu.Lock()
	_, pending := r.pending["8.8.8.8"]
	r.mu.Unlock()
	if pending {
		t.Fatal("completed IP retained a pending marker")
	}
}

func TestRunDoesNotLogProviderDetails(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	p := &fakeProvider{locations: map[string]Location{}, errors: map[string]error{"8.8.8.8": errors.New("token=secret response body")}}
	var mu sync.Mutex
	var logs []string
	r := NewRefresher(d, p, 12*time.Hour, func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "8.8.8.8"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		_, pending := r.pending["8.8.8.8"]
		r.mu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("failed lookup did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	if strings.Contains(joined, "secret") || strings.Contains(joined, "response body") {
		t.Fatalf("provider detail leaked in log: %q", joined)
	}
}

func TestRunRetriesObservationArrivingBeforePendingCleanup(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	insertNode(t, d, "n2", report.Host{EgressIPv4: "8.8.8.8"})
	p := &retryProvider{location: Location{Country: "美国", City: "山景城"}}
	paused := make(chan struct{})
	resume := make(chan struct{})
	var pauseOnce sync.Once
	r := NewRefresher(d, p, 12*time.Hour, func(string, ...any) {
		pauseOnce.Do(func() {
			close(paused)
			<-resume
		})
	})
	if err := r.ObserveNode(context.Background(), "n1", report.Host{EgressIPv4: "8.8.8.8"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(runDone)
	}()
	select {
	case <-paused:
	case <-time.After(time.Second):
		cancel()
		close(resume)
		t.Fatal("worker did not pause before pending cleanup")
	}
	if err := r.ObserveNode(context.Background(), "n2", report.Host{EgressIPv4: "8.8.8.8"}); err != nil {
		cancel()
		close(resume)
		t.Fatal(err)
	}
	close(resume)

	deadline := time.Now().Add(time.Second)
	for {
		var location string
		if err := d.QueryRow(`SELECT COALESCE(ip_location,'') FROM nodes WHERE node_id='n2'`).Scan(&location); err != nil {
			cancel()
			t.Fatal(err)
		}
		if location == "美国-山景城" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-runDone
			t.Fatalf("second observation was lost; provider calls = %d", p.callCount())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-runDone
	if calls := p.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want one retry", calls)
	}
}

func TestReconcileClosesRowsBeforeObservationAndSkipsInvalidJSON(t *testing.T) {
	d := openGeoTestDB(t)
	insertNode(t, d, "n1", report.Host{EgressIPv4: "8.8.8.8"})
	insertNode(t, d, "broken", report.Host{})
	if _, err := d.Exec(`UPDATE nodes SET last_host_json='not-json' WHERE node_id='broken'`); err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	p := &fakeProvider{locations: map[string]Location{"8.8.8.8": {Country: "美国", City: "山景城"}}}
	var mu sync.Mutex
	var logs []string
	r := NewRefresher(d, p, 12*time.Hour, func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := p.callSnapshot(); len(calls) != 0 {
		t.Fatalf("provider calls during reconciliation = %v", calls)
	}
	if len(r.queue) != 1 {
		t.Fatalf("queue length = %d", len(r.queue))
	}
	mu.Lock()
	joined := strings.Join(logs, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "broken") || strings.Contains(joined, "not-json") {
		t.Fatalf("invalid JSON log = %q", joined)
	}
}

func TestReconcileSnapshotCannotOverwriteNewerReport(t *testing.T) {
	d := openGeoTestDB(t)
	oldHost := report.Host{EgressIPv4: "8.8.8.8"}
	insertNode(t, d, "target", oldHost)

	paused := make(chan struct{})
	resume := make(chan struct{})
	r := NewRefresher(d, &fakeProvider{locations: map[string]Location{}}, 12*time.Hour, nil)
	r.afterReconcileLoad = func() {
		close(paused)
		<-resume
	}
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- r.Reconcile(context.Background()) }()
	select {
	case <-paused:
	case <-time.After(time.Second):
		close(resume)
		<-reconcileDone
		t.Fatal("reconciliation did not pause after materializing rows")
	}

	newHost := report.Host{EgressIPv4: "1.1.1.1"}
	newJSON := persistNodeHost(t, d, "target", newHost)
	if err := r.ObserveNode(context.Background(), "target", newHost); err != nil {
		close(resume)
		t.Fatal(err)
	}
	close(resume)
	if err := <-reconcileDone; err != nil {
		t.Fatal(err)
	}

	var hostJSON, geoIP string
	if err := d.QueryRow(`SELECT COALESCE(last_host_json,''), COALESCE(ip_geo_ip,'') FROM nodes WHERE node_id='target'`).Scan(&hostJSON, &geoIP); err != nil {
		t.Fatal(err)
	}
	if hostJSON != newJSON || geoIP != "1.1.1.1" {
		t.Fatalf("last_host_json=%q ip_geo_ip=%q", hostJSON, geoIP)
	}
}
