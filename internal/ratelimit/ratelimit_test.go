package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadExhaustionAndReset(t *testing.T) {
	l := NewRead(2, 10)
	now := time.Date(2026, time.August, 25, 10, 2, 30, 0, time.UTC)

	first := l.Allow("client", now)
	if !first.Allowed || first.Remaining != 1 {
		t.Fatalf("first result = %+v", first)
	}
	if want := time.Date(2026, time.August, 25, 10, 3, 0, 0, time.UTC); !first.Reset.Equal(want) {
		t.Fatalf("reset = %v, want %v", first.Reset, want)
	}
	if got := l.Allow("client", now); !got.Allowed || got.Remaining != 0 {
		t.Fatalf("second result = %+v", got)
	}
	if got := l.Allow("client", now); got.Allowed || got.Remaining != 0 || !got.Reset.Equal(first.Reset) {
		t.Fatalf("exhausted result = %+v", got)
	}

	got := l.Allow("client", first.Reset)
	if !got.Allowed || got.Remaining != 1 || !got.Reset.Equal(first.Reset.Add(time.Minute)) {
		t.Fatalf("new-window result = %+v", got)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l := NewRead(1, 10)
	now := time.Date(2026, time.August, 25, 10, 0, 0, 0, time.UTC)

	if !l.Allow("192.0.2.1", now).Allowed || !l.Allow("192.0.2.2", now).Allowed {
		t.Fatal("independent clients should each be allowed")
	}
	if l.Allow("192.0.2.1", now).Allowed {
		t.Fatal("first client should be exhausted")
	}
}

func TestConcurrentAllow(t *testing.T) {
	const limit = 37
	const attempts = 500
	l := NewRead(limit, 10)
	now := time.Date(2026, time.August, 25, 10, 0, 1, 0, time.UTC)

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("client", now).Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := allowed.Load(); got != limit {
		t.Fatalf("allowed = %d, want %d", got, limit)
	}
}

func TestWriteResetUsesLocalMidnightAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	l := NewWrite(1, loc, 10)

	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "spring forward",
			now:  time.Date(2026, time.March, 8, 0, 30, 0, 0, loc),
			want: time.Date(2026, time.March, 9, 0, 0, 0, 0, loc),
		},
		{
			name: "fall back",
			now:  time.Date(2026, time.November, 1, 0, 30, 0, 0, loc),
			want: time.Date(2026, time.November, 2, 0, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := l.Allow(tt.name, tt.now)
			if !got.Reset.Equal(tt.want) {
				t.Fatalf("reset = %v, want %v", got.Reset, tt.want)
			}
			if got.Reset.Sub(tt.now) == 24*time.Hour {
				t.Fatalf("DST day unexpectedly had a 24-hour duration")
			}
			if !l.Allow(tt.name, got.Reset).Allowed {
				t.Fatal("request at midnight should start a new window")
			}
		})
	}
}

func TestLimiterBoundsKeysAndCleansExpiredEntries(t *testing.T) {
	l := NewRead(1, 2)
	now := time.Date(2026, time.August, 25, 10, 0, 1, 0, time.UTC)
	l.Allow("a", now)
	l.Allow("b", now)
	l.Allow("c", now)

	if got := len(l.entries); got != 2 {
		t.Fatalf("entry count = %d, want 2", got)
	}
	if _, exists := l.entries["c"]; exists {
		t.Fatal("new key should be rejected while all tracked keys are active")
	}

	l.Allow("d", now.Add(time.Minute))
	if got := len(l.entries); got != 1 {
		t.Fatalf("entry count after cleanup = %d, want 1", got)
	}
	if _, exists := l.entries["d"]; !exists {
		t.Fatal("new key missing after cleanup")
	}
}
