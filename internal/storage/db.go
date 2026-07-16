package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Omotolani98/meetingctl/internal/crypto"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed meeting repository.
type Store struct {
	db  *sql.DB
	box *crypto.Box
}

// Open creates or opens a SQLite database, runs migrations, and returns a Store.
func Open(ctx context.Context, path string, box *crypto.Box) (*Store, error) {
	if box == nil {
		return nil, fmt.Errorf("encryption box is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && filepath.Dir(path) != "." {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	// modernc DSN: path with query params
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Allow a small pool so nested reads (e.g. insights + sources) do not deadlock.
	// Writes still serialize via SQLite's database lock + WAL.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Restrict file mode when we created/own the path.
	_ = os.Chmod(path, 0o600)

	return &Store{db: db, box: box}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for tests.
func (s *Store) DB() *sql.DB {
	return s.db
}

func nowUTC() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func scanNullTime(v sql.NullString) (*time.Time, error) {
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	t, err := parseTime(v.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
