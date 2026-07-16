package storage

import (
	"context"
	"database/sql"
	"fmt"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS meetings (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		status TEXT NOT NULL,
		started_at TEXT NOT NULL,
		ended_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_meetings_one_active
		ON meetings(status) WHERE status = 'active'`,
	`CREATE INDEX IF NOT EXISTS idx_meetings_started_at ON meetings(started_at DESC)`,
	`CREATE TABLE IF NOT EXISTS participants (
		id TEXT PRIMARY KEY,
		meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_participants_meeting ON participants(meeting_id)`,
	`CREATE TABLE IF NOT EXISTS transcript_segments (
		id TEXT PRIMARY KEY,
		meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
		sequence INTEGER NOT NULL,
		speaker TEXT NOT NULL DEFAULT '',
		text_key_id TEXT NOT NULL,
		text_nonce TEXT NOT NULL,
		text_cipher TEXT NOT NULL,
		started_ms INTEGER NOT NULL DEFAULT 0,
		ended_ms INTEGER NOT NULL DEFAULT 0,
		confidence REAL NOT NULL DEFAULT 0,
		is_final INTEGER NOT NULL DEFAULT 1,
		idempotency TEXT NOT NULL DEFAULT '',
		revision INTEGER NOT NULL DEFAULT 0,
		original_key_id TEXT,
		original_nonce TEXT,
		original_cipher TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(meeting_id, sequence),
		UNIQUE(meeting_id, idempotency)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_segments_meeting_seq ON transcript_segments(meeting_id, sequence)`,
	`CREATE TABLE IF NOT EXISTS meeting_insights (
		id TEXT PRIMARY KEY,
		meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		text_key_id TEXT NOT NULL,
		text_nonce TEXT NOT NULL,
		text_cipher TEXT NOT NULL,
		owner TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'open',
		confidence REAL NOT NULL DEFAULT 0,
		is_manual INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_insights_meeting_type ON meeting_insights(meeting_id, type)`,
	`CREATE INDEX IF NOT EXISTS idx_insights_owner ON meeting_insights(meeting_id, owner)`,
	`CREATE TABLE IF NOT EXISTS insight_sources (
		insight_id TEXT NOT NULL REFERENCES meeting_insights(id) ON DELETE CASCADE,
		segment_id TEXT NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
		PRIMARY KEY (insight_id, segment_id)
	)`,
	`CREATE TABLE IF NOT EXISTS meeting_summaries (
		id TEXT PRIMARY KEY,
		meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
		text_key_id TEXT NOT NULL,
		text_nonce TEXT NOT NULL,
		text_cipher TEXT NOT NULL,
		version INTEGER NOT NULL,
		through_sequence INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(meeting_id, version)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_summaries_meeting ON meeting_summaries(meeting_id, version DESC)`,
	`CREATE TABLE IF NOT EXISTS manual_notes (
		id TEXT PRIMARY KEY,
		meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
		text_key_id TEXT NOT NULL,
		text_nonce TEXT NOT NULL,
		text_cipher TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_notes_meeting ON manual_notes(meeting_id)`,
	`CREATE TABLE IF NOT EXISTS meeting_events (
		id TEXT PRIMARY KEY,
		meeting_id TEXT NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		payload TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_events_meeting ON meeting_events(meeting_id, created_at)`,
}

// Migrate applies schema migrations in order.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, migrations[0]); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current)
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	// version 1 = first real schema after schema_migrations itself
	for i := 1; i < len(migrations); i++ {
		version := i
		if version <= current {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`,
			version,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
