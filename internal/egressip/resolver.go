package egressip

import (
	"context"
	"errors"
	"time"
)

type Options struct {
	Enabled         bool
	RefreshInterval time.Duration
	Timeout         time.Duration
	CachePath       string
	IPv4Endpoints   []string
	IPv6Endpoints   []string
	Logf            func(string, ...any)
}

type Result struct {
	IPv4 string
	IPv6 string
}

type DiscoverFunc func(context.Context, Family, []string) (string, error)

type Resolver struct {
	Discover DiscoverFunc
	Now      func() time.Time
}

type familyRefresh struct {
	family    Family
	endpoints []string
}

type familyResult struct {
	family  Family
	address string
	err     error
}

func (r Resolver) Resolve(ctx context.Context, opts Options) Result {
	if !opts.Enabled {
		return Result{}
	}

	cache, err := readCache(opts.CachePath)
	if err != nil {
		logSafe(opts.Logf, "egress IP cache category=invalid_or_unreadable")
		cache = cacheFile{}
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	result := Result{IPv4: cache.IPv4.Address, IPv6: cache.IPv6.Address}

	var pending []familyRefresh
	if needsRefresh(cache.IPv4, now, opts.RefreshInterval) && (cache.IPv4.Address != "" || len(opts.IPv4Endpoints) > 0) {
		pending = append(pending, familyRefresh{family: IPv4, endpoints: opts.IPv4Endpoints})
	}
	if needsRefresh(cache.IPv6, now, opts.RefreshInterval) && (cache.IPv6.Address != "" || len(opts.IPv6Endpoints) > 0) {
		pending = append(pending, familyRefresh{family: IPv6, endpoints: opts.IPv6Endpoints})
	}
	if len(pending) == 0 {
		return result
	}

	results := make(chan familyResult, len(pending))
	for _, refresh := range pending {
		go func(refresh familyRefresh) {
			familyCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
			if r.Discover == nil {
				results <- familyResult{family: refresh.family, err: errors.New("discoverer unavailable")}
				return
			}
			address, err := r.Discover(familyCtx, refresh.family, refresh.endpoints)
			results <- familyResult{family: refresh.family, address: address, err: err}
		}(refresh)
	}

	dirty := false
	for range pending {
		refresh := <-results
		if refresh.err != nil {
			logSafe(opts.Logf, "egress IP refresh family=%s category=%s", familyName(refresh.family), errorCategory(refresh.err))
			continue
		}
		entry := cacheEntry{Address: refresh.address, ObservedAt: now}
		switch refresh.family {
		case IPv4:
			cache.IPv4 = entry
			result.IPv4 = refresh.address
		case IPv6:
			cache.IPv6 = entry
			result.IPv6 = refresh.address
		}
		dirty = true
	}
	if dirty {
		if err := writeCache(opts.CachePath, cache); err != nil {
			logSafe(opts.Logf, "egress IP cache category=write_failed")
		}
	}
	return result
}

func needsRefresh(entry cacheEntry, now time.Time, interval time.Duration) bool {
	if entry.Address == "" || entry.ObservedAt.IsZero() {
		return true
	}
	return !now.Before(entry.ObservedAt.Add(interval))
}

func errorCategory(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "unavailable"
	}
}

func logSafe(logf func(string, ...any), format string, args ...any) {
	if logf != nil {
		logf(format, args...)
	}
}
