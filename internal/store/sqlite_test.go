package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T, path string) *SQLite {
	t.Helper()
	s, err := Open(context.Background(), Config{Path: path, BusyTimeout: 250 * time.Millisecond})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return s
}

func TestCreateUsesServerTimeAtMillisecondResolution(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "trips.db"))
	wantTime := time.Date(2026, time.August, 25, 12, 34, 56, 789_654_321, time.FixedZone("test", -7*60*60))
	s.now = func() time.Time { return wantTime }

	trip, err := s.Create(context.Background(), true)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	wantTime = time.UnixMilli(wantTime.UnixMilli()).UTC()
	if trip.ID == 0 {
		t.Error("Create() returned a zero ID")
	}
	if !trip.OccurredAt.Equal(wantTime) || trip.OccurredAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Errorf("OccurredAt = %v, want %v at millisecond resolution", trip.OccurredAt, wantTime)
	}
	if !trip.HasPoo {
		t.Error("HasPoo = false, want true")
	}

	got, err := s.List(context.Background(), wantTime, wantTime.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0] != trip {
		t.Fatalf("List() = %+v, want [%+v]", got, trip)
	}
}

func TestListUsesHalfOpenBoundsAndNewestFirstOrder(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "trips.db"))
	ctx := context.Background()
	start := time.Date(2026, time.August, 25, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	create := func(at time.Time, hasPoo bool) int64 {
		t.Helper()
		trip, err := s.create(ctx, at, hasPoo)
		if err != nil {
			t.Fatalf("create(%v, %t) error = %v", at, hasPoo, err)
		}
		return trip.ID
	}
	create(start.Add(-time.Millisecond), false)
	atStartID := create(start, false)
	middleOldID := create(start.Add(30*time.Minute), false)
	middleNewID := create(start.Add(30*time.Minute), true)
	create(end, true)

	got, err := s.List(ctx, start, end)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	wantIDs := []int64{middleNewID, middleOldID, atStartID}
	if len(got) != len(wantIDs) {
		t.Fatalf("List() returned %d trips, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i, wantID := range wantIDs {
		if got[i].ID != wantID {
			t.Errorf("List()[%d].ID = %d, want %d", i, got[i].ID, wantID)
		}
	}
	if got[0].HasPoo != true || got[1].HasPoo != false {
		t.Errorf("List() did not preserve HasPoo values: %+v", got)
	}
}

func TestListHonorsSubMillisecondBounds(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "trips.db"))
	ctx := context.Background()
	at := time.Date(2026, time.August, 25, 8, 0, 0, 123_000_000, time.UTC)
	if _, err := s.create(ctx, at, false); err != nil {
		t.Fatalf("create() error = %v", err)
	}

	got, err := s.List(ctx, at.Add(time.Nanosecond), at.Add(time.Millisecond+time.Nanosecond))
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List() = %+v, want no trips after exclusive sub-millisecond start", got)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trips.db")
	ctx := context.Background()
	s, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	want, err := s.create(ctx, time.Date(2026, time.August, 25, 9, 10, 11, 456_000_000, time.UTC), true)
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	s = openTestStore(t, path)
	got, err := s.List(ctx, want.OccurredAt, want.OccurredAt.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("List() after reopen error = %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("List() after reopen = %+v, want [%+v]", got, want)
	}
}

func TestConfigurationAndLifecycle(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "trips.db"))
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	var journalMode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}
	var busyTimeout int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != 250 {
		t.Errorf("busy_timeout = %d, want 250", busyTimeout)
	}
	var indexCount int
	if err := s.db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'trips_occurred_at_idx'",
	).Scan(&indexCount); err != nil {
		t.Fatalf("query schema index: %v", err)
	}
	if indexCount != 1 {
		t.Errorf("schema index count = %d, want 1", indexCount)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := s.Ping(ctx); err == nil {
		t.Error("Ping() after Close() returned nil error")
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, Config{}); err == nil {
		t.Error("Open() with empty path returned nil error")
	}
	if _, err := Open(ctx, Config{Path: "ignored", BusyTimeout: -time.Second}); err == nil {
		t.Error("Open() with negative busy timeout returned nil error")
	}

	s := openTestStore(t, filepath.Join(t.TempDir(), "trips.db"))
	if _, err := s.List(ctx, time.Now(), time.Now().Add(-time.Second)); err == nil {
		t.Error("List() with reversed range returned nil error")
	}
}
