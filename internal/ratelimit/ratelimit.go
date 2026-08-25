// Package ratelimit provides bounded, in-memory fixed-window rate limiting.
package ratelimit

import (
	"sync"
	"time"
)

// Result describes an Allow decision. Remaining excludes the current request.
type Result struct {
	Allowed   bool
	Remaining int
	Reset     time.Time
}

type entry struct {
	used  int
	reset time.Time
}

// Limiter is safe for concurrent use.
type Limiter struct {
	mu       sync.Mutex
	entries  map[string]entry
	limit    int
	maxKeys  int
	resetFor func(time.Time) time.Time
}

// NewWrite returns a limiter whose windows end at the next midnight in loc.
func NewWrite(limit int, loc *time.Location, maxKeys int) *Limiter {
	if loc == nil {
		panic("ratelimit: nil location")
	}
	return newLimiter(limit, maxKeys, func(now time.Time) time.Time {
		local := now.In(loc)
		year, month, day := local.Date()
		return time.Date(year, month, day+1, 0, 0, 0, 0, loc)
	})
}

// NewRead returns a limiter whose windows end at the next minute boundary.
func NewRead(limit, maxKeys int) *Limiter {
	return newLimiter(limit, maxKeys, func(now time.Time) time.Time {
		return now.Truncate(time.Minute).Add(time.Minute)
	})
}

func newLimiter(limit, maxKeys int, resetFor func(time.Time) time.Time) *Limiter {
	if limit <= 0 {
		panic("ratelimit: limit must be positive")
	}
	if maxKeys <= 0 {
		panic("ratelimit: maxKeys must be positive")
	}
	return &Limiter{
		entries:  make(map[string]entry),
		limit:    limit,
		maxKeys:  maxKeys,
		resetFor: resetFor,
	}
}

// Allow records an attempt for key at now and returns its window metadata.
// Denied attempts do not consume additional capacity.
func (l *Limiter) Allow(key string, now time.Time) Result {
	l.mu.Lock()
	defer l.mu.Unlock()

	reset := l.resetFor(now)
	e, exists := l.entries[key]
	if !exists {
		if !l.makeRoom(now) {
			return Result{Allowed: false, Remaining: 0, Reset: reset}
		}
		e = entry{reset: reset}
	} else if !e.reset.Equal(reset) {
		e = entry{reset: reset}
	}

	if e.used >= l.limit {
		return Result{Allowed: false, Remaining: 0, Reset: e.reset}
	}

	e.used++
	l.entries[key] = e
	return Result{Allowed: true, Remaining: l.limit - e.used, Reset: e.reset}
}

// makeRoom opportunistically drops expired entries and strictly bounds keys.
func (l *Limiter) makeRoom(now time.Time) bool {
	for key, e := range l.entries {
		if !now.Before(e.reset) {
			delete(l.entries, key)
		}
	}
	if len(l.entries) < l.maxKeys {
		return true
	}
	return false
}
