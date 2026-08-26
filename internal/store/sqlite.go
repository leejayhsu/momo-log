package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"momo-poo/internal/auth"
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

// PushSubscription contains the browser-provided Web Push delivery details.
type PushSubscription struct {
	Endpoint string
	P256DH   string
	Auth     string
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
		"PRAGMA foreign_keys = ON",
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
			has_poo INTEGER NOT NULL CHECK (has_poo IN (0, 1)),
			user_id INTEGER REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS trips_occurred_at_idx
			ON trips (occurred_at_ms DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			endpoint TEXT PRIMARY KEY,
			p256dh TEXT NOT NULL,
			auth TEXT NOT NULL,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL COLLATE NOCASE UNIQUE,
			webauthn_handle BLOB NOT NULL UNIQUE,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS webauthn_credentials (
			credential_id BLOB PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			credential_json BLOB NOT NULL,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS webauthn_credentials_user_idx ON webauthn_credentials (user_id)`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (
			token_hash BLOB PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at_ms INTEGER NOT NULL,
			created_at_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS auth_sessions_expiry_idx ON auth_sessions (expires_at_ms)`,
		"PRAGMA user_version = 3",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("store: migrate sqlite: %w", err)
		}
	}
	var hasTripUserID bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info('trips') WHERE name = 'user_id')").Scan(&hasTripUserID); err != nil {
		return fmt.Errorf("store: inspect trips schema: %w", err)
	}
	if !hasTripUserID {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE trips ADD COLUMN user_id INTEGER REFERENCES users(id)"); err != nil {
			return fmt.Errorf("store: add trip user: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration: %w", err)
	}
	return nil
}

// AuthUsernameExists reports whether a case-insensitive username is already registered.
func (s *SQLite) AuthUsernameExists(ctx context.Context, username string) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)", username).Scan(&exists); err != nil {
		return false, fmt.Errorf("store: check auth username: %w", err)
	}
	return exists, nil
}

// AuthCreateUser atomically creates a passkey-only user and their first credential.
func (s *SQLite) AuthCreateUser(ctx context.Context, user *auth.User, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("store: encode WebAuthn credential: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin auth user transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT INTO users (username, webauthn_handle, created_at_ms) VALUES (?, ?, ?)", user.Username, user.Handle, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("store: create auth user: %w", err)
	}
	user.ID, err = result.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: get auth user ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO webauthn_credentials (credential_id, user_id, credential_json, created_at_ms) VALUES (?, ?, ?, ?)", credential.ID, user.ID, encoded, s.now().UnixMilli()); err != nil {
		return fmt.Errorf("store: create WebAuthn credential: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit auth user: %w", err)
	}
	user.Credentials = []webauthn.Credential{credential}
	return nil
}

// AuthUserByHandle loads the owner and credentials for a discoverable passkey.
func (s *SQLite) AuthUserByHandle(ctx context.Context, handle []byte) (*auth.User, error) {
	user := &auth.User{Handle: append([]byte(nil), handle...)}
	if err := s.db.QueryRowContext(ctx, "SELECT id, username FROM users WHERE webauthn_handle = ?", handle).Scan(&user.ID, &user.Username); err != nil {
		return nil, fmt.Errorf("store: load auth user: %w", err)
	}
	credentials, err := s.authCredentials(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	user.Credentials = credentials
	return user, nil
}

func (s *SQLite) authCredentials(ctx context.Context, userID int64) ([]webauthn.Credential, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT credential_json FROM webauthn_credentials WHERE user_id = ?", userID)
	if err != nil {
		return nil, fmt.Errorf("store: list WebAuthn credentials: %w", err)
	}
	defer rows.Close()
	var credentials []webauthn.Credential
	for rows.Next() {
		var encoded []byte
		var credential webauthn.Credential
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("store: scan WebAuthn credential: %w", err)
		}
		if err := json.Unmarshal(encoded, &credential); err != nil {
			return nil, fmt.Errorf("store: decode WebAuthn credential: %w", err)
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list WebAuthn credentials: %w", err)
	}
	return credentials, nil
}

// AuthUpdateCredential persists counters and backup state changed during login.
func (s *SQLite) AuthUpdateCredential(ctx context.Context, userID int64, credential webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("store: encode WebAuthn credential: %w", err)
	}
	result, err := s.db.ExecContext(ctx, "UPDATE webauthn_credentials SET credential_json = ? WHERE credential_id = ? AND user_id = ?", encoded, credential.ID, userID)
	if err != nil {
		return fmt.Errorf("store: update WebAuthn credential: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("store: WebAuthn credential was not found")
	}
	return nil
}

// AuthCreateSession stores a hashed bearer token.
func (s *SQLite) AuthCreateSession(ctx context.Context, tokenHash []byte, userID int64, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO auth_sessions (token_hash, user_id, expires_at_ms, created_at_ms) VALUES (?, ?, ?, ?)", tokenHash, userID, expires.UnixMilli(), s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("store: create auth session: %w", err)
	}
	return nil
}

// AuthUserBySession resolves a live application session.
func (s *SQLite) AuthUserBySession(ctx context.Context, tokenHash []byte, now time.Time) (*auth.User, error) {
	user := &auth.User{}
	if err := s.db.QueryRowContext(ctx, `SELECT users.id, users.username, users.webauthn_handle
		FROM auth_sessions JOIN users ON users.id = auth_sessions.user_id
		WHERE auth_sessions.token_hash = ? AND auth_sessions.expires_at_ms > ?`, tokenHash, now.UnixMilli()).Scan(&user.ID, &user.Username, &user.Handle); err != nil {
		return nil, fmt.Errorf("store: load auth session: %w", err)
	}
	return user, nil
}

// AuthDeleteSession revokes an application session.
func (s *SQLite) AuthDeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM auth_sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("store: delete auth session: %w", err)
	}
	return nil
}

// SavePushSubscription creates or refreshes a browser push subscription.
func (s *SQLite) SavePushSubscription(ctx context.Context, subscription PushSubscription) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (endpoint, p256dh, auth, created_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET
			p256dh = excluded.p256dh,
			auth = excluded.auth,
			created_at_ms = excluded.created_at_ms`,
		subscription.Endpoint, subscription.P256DH, subscription.Auth, s.now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("store: save push subscription: %w", err)
	}
	return nil
}

// DeletePushSubscription removes a browser push subscription by endpoint.
func (s *SQLite) DeletePushSubscription(ctx context.Context, endpoint string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM push_subscriptions WHERE endpoint = ?", endpoint); err != nil {
		return fmt.Errorf("store: delete push subscription: %w", err)
	}
	return nil
}

// ListPushSubscriptions returns all registered browser push subscriptions.
func (s *SQLite) ListPushSubscriptions(ctx context.Context) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT endpoint, p256dh, auth FROM push_subscriptions ORDER BY created_at_ms")
	if err != nil {
		return nil, fmt.Errorf("store: list push subscriptions: %w", err)
	}
	defer rows.Close()

	result := make([]PushSubscription, 0)
	for rows.Next() {
		var subscription PushSubscription
		if err := rows.Scan(&subscription.Endpoint, &subscription.P256DH, &subscription.Auth); err != nil {
			return nil, fmt.Errorf("store: scan push subscription: %w", err)
		}
		result = append(result, subscription)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list push subscriptions: %w", err)
	}
	return result, nil
}

// EnsureVAPIDKeys returns the persisted VAPID key pair, creating it atomically when absent.
func (s *SQLite) EnsureVAPIDKeys(ctx context.Context, privateKey, publicKey string) (string, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("store: begin VAPID key transaction: %w", err)
	}
	defer tx.Rollback()

	var storedPrivate, storedPublic string
	err = tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(MAX(CASE WHEN key = 'vapid_private_key' THEN value END), ''),
			COALESCE(MAX(CASE WHEN key = 'vapid_public_key' THEN value END), '')
		FROM app_settings`).Scan(&storedPrivate, &storedPublic)
	if err != nil {
		return "", "", fmt.Errorf("store: read VAPID keys: %w", err)
	}
	if storedPrivate == "" || storedPublic == "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app_settings (key, value) VALUES ('vapid_private_key', ?), ('vapid_public_key', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, privateKey, publicKey); err != nil {
			return "", "", fmt.Errorf("store: save VAPID keys: %w", err)
		}
		storedPrivate, storedPublic = privateKey, publicKey
	}
	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("store: commit VAPID keys: %w", err)
	}
	return storedPrivate, storedPublic, nil
}

// Create records a trip using the server's current time.
func (s *SQLite) Create(ctx context.Context, hasPoo bool, userID int64) (trips.Trip, error) {
	return s.create(ctx, s.now(), hasPoo, userID)
}

func (s *SQLite) create(ctx context.Context, occurredAt time.Time, hasPoo bool, userID int64) (trips.Trip, error) {
	occurredAt = time.UnixMilli(occurredAt.UnixMilli()).UTC()
	result, err := s.db.ExecContext(ctx,
		"INSERT INTO trips (occurred_at_ms, has_poo, user_id) VALUES (?, ?, NULLIF(?, 0))",
		occurredAt.UnixMilli(), hasPoo, userID,
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
		SELECT trips.id, trips.occurred_at_ms, trips.has_poo, COALESCE(users.username, '')
		FROM trips
		LEFT JOIN users ON users.id = trips.user_id
		WHERE trips.occurred_at_ms >= ? AND trips.occurred_at_ms < ?
		ORDER BY trips.occurred_at_ms DESC, trips.id DESC`,
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
		if err := rows.Scan(&trip.ID, &occurredAtMS, &trip.HasPoo, &trip.Username); err != nil {
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
