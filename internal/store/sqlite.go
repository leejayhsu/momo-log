package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"momo-poo/internal/trips"

	_ "modernc.org/sqlite"
)

const defaultBusyTimeout = 5 * time.Second

// Config controls the SQLite database connection.
type Config struct {
	Path        string
	BusyTimeout time.Duration
}

// SQLite persists bathroom trips in SQLite.
type SQLite struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens a SQLite database and applies its connection settings and schema.
func Open(ctx context.Context, cfg Config) (*SQLite, error) {
	if cfg.Path == "" {
		return nil, errors.New("store: database path is required")
	}
	if cfg.BusyTimeout < 0 {
		return nil, errors.New("store: busy timeout cannot be negative")
	}
	if cfg.BusyTimeout == 0 {
		cfg.BusyTimeout = defaultBusyTimeout
	}

	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	// PRAGMAs are connection-local. One connection is sufficient for this small
	// application and ensures every operation uses the configured connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &SQLite{db: db, now: time.Now}
	if err := s.configure(ctx, cfg.BusyTimeout); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) configure(ctx context.Context, busyTimeout time.Duration) error {
	statements := []string{
		"PRAGMA journal_mode = WAL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds()),
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: configure sqlite: %w", err)
		}
	}
	return nil
}

func (s *SQLite) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS trips (
			id INTEGER PRIMARY KEY,
			occurred_at_ms INTEGER NOT NULL,
			has_poo INTEGER NOT NULL CHECK (has_poo IN (0, 1))
		)`,
		`CREATE INDEX IF NOT EXISTS trips_occurred_at_idx
			ON trips (occurred_at_ms DESC, id DESC)`,
		"PRAGMA user_version = 1",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: migrate sqlite: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration: %w", err)
	}
	return nil
}

// Create records a trip using the server's current time.
func (s *SQLite) Create(ctx context.Context, hasPoo bool) (trips.Trip, error) {
	return s.create(ctx, s.now(), hasPoo)
}

func (s *SQLite) create(ctx context.Context, occurredAt time.Time, hasPoo bool) (trips.Trip, error) {
	occurredAt = time.UnixMilli(occurredAt.UnixMilli()).UTC()
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO trips (occurred_at_ms, has_poo) VALUES (?, ?)",
		occurredAt.UnixMilli(), hasPoo,
	)
	if err != nil {
		return trips.Trip{}, fmt.Errorf("store: create trip: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return trips.Trip{}, fmt.Errorf("store: get trip ID: %w", err)
	}
	return trips.Trip{ID: id, OccurredAt: occurredAt, HasPoo: hasPoo}, nil
}

// Delete removes a trip by ID and reports whether it existed.
func (s *SQLite) Delete(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM trips WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("store: delete trip: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: get deleted trip count: %w", err)
	}
	return rows > 0, nil
}

// List returns trips in [start, end), newest first.
func (s *SQLite) List(ctx context.Context, start, end time.Time) ([]trips.Trip, error) {
	if !start.Before(end) {
		return nil, errors.New("store: start must be before end")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, occurred_at_ms, has_poo
		FROM trips
		WHERE occurred_at_ms >= ? AND occurred_at_ms < ?
		ORDER BY occurred_at_ms DESC, id DESC`,
		ceilUnixMilli(start), ceilUnixMilli(end),
	)
	if err != nil {
		return nil, fmt.Errorf("store: list trips: %w", err)
	}
	defer rows.Close()

	result := make([]trips.Trip, 0)
	for rows.Next() {
		var trip trips.Trip
		var occurredAtMS int64
		if err := rows.Scan(&trip.ID, &occurredAtMS, &trip.HasPoo); err != nil {
			return nil, fmt.Errorf("store: scan trip: %w", err)
		}
		trip.OccurredAt = time.UnixMilli(occurredAtMS).UTC()
		result = append(result, trip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list trips: %w", err)
	}
	return result, nil
}

func ceilUnixMilli(t time.Time) int64 {
	milliseconds := t.UnixMilli()
	if t.After(time.UnixMilli(milliseconds)) {
		milliseconds++
	}
	return milliseconds
}

// Ping verifies that the database is reachable.
func (s *SQLite) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close releases the database connection.
func (s *SQLite) Close() error {
	return s.db.Close()
}
