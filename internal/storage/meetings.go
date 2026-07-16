package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Omotolani98/meetingctl/internal/meetings"
)

// ErrNotFound is returned when a meeting or related entity is missing.
var ErrNotFound = errors.New("not found")

// ErrActiveMeeting exists when a second active meeting is requested.
var ErrActiveMeeting = errors.New("an active meeting already exists")

// CreateMeeting inserts a new active meeting and participants.
func (s *Store) CreateMeeting(ctx context.Context, title string, participantNames []string) (*meetings.Meeting, error) {
	if err := meetings.ValidateTitle(title); err != nil {
		return nil, err
	}
	id, err := newID("mtg")
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM meetings WHERE status = 'active' LIMIT 1`).Scan(&existing)
	if err == nil {
		return nil, fmt.Errorf("%w: %s", ErrActiveMeeting, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO meetings(id, title, status, started_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(title), string(meetings.StatusActive),
		formatTime(now), formatTime(now), formatTime(now),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrActiveMeeting
		}
		return nil, err
	}

	parts := make([]meetings.Participant, 0, len(participantNames))
	for _, name := range participantNames {
		pid, err := newID("p")
		if err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO participants(id, meeting_id, name, email) VALUES (?, ?, ?, '')`,
			pid, id, name,
		); err != nil {
			return nil, err
		}
		parts = append(parts, meetings.Participant{ID: pid, MeetingID: id, Name: name})
	}

	if err := insertEvent(ctx, tx, id, "meeting.started", title); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &meetings.Meeting{
		ID:           id,
		Title:        strings.TrimSpace(title),
		Status:       meetings.StatusActive,
		StartedAt:    now,
		Participants: parts,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// GetActiveMeeting returns the currently active meeting, if any.
func (s *Store) GetActiveMeeting(ctx context.Context) (*meetings.Meeting, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, status, started_at, ended_at, created_at, updated_at
		FROM meetings WHERE status = 'active' LIMIT 1`)
	m, err := scanMeeting(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	parts, err := s.listParticipants(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	m.Participants = parts
	return m, nil
}

// GetMeeting returns a meeting by ID.
func (s *Store) GetMeeting(ctx context.Context, id string) (*meetings.Meeting, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, status, started_at, ended_at, created_at, updated_at
		FROM meetings WHERE id = ?`, id)
	m, err := scanMeeting(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	parts, err := s.listParticipants(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	m.Participants = parts
	return m, nil
}

// ListMeetings returns meetings newest first.
func (s *Store) ListMeetings(ctx context.Context, limit int) ([]meetings.Meeting, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, status, started_at, ended_at, created_at, updated_at
		FROM meetings ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []meetings.Meeting
	for rows.Next() {
		m, err := scanMeeting(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// SetMeetingStatus updates status and optional end time.
func (s *Store) SetMeetingStatus(ctx context.Context, id string, status meetings.MeetingStatus, endedAt *time.Time) error {
	now := nowUTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE meetings SET status = ?, ended_at = COALESCE(?, ended_at), updated_at = ?
		WHERE id = ?`, string(status), nullTime(endedAt), formatTime(now), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMeeting removes a meeting and related rows (CASCADE).
func (s *Store) DeleteMeeting(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertEvent(ctx, tx, id, "meeting.deleted", ""); err != nil {
		// event insert requires meeting to exist; check first
		var exists string
		if err2 := tx.QueryRowContext(ctx, `SELECT id FROM meetings WHERE id = ?`, id).Scan(&exists); errors.Is(err2, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	// Keep a lightweight audit outside cascade: store event then delete is fine for local single-user.
	// For permanent audit of deletes, copy event first — we insert then cascade deletes it.
	// Re-insert into a detached log is overkill for MVP; record payload before delete in caller logs.
	res, err := tx.ExecContext(ctx, `DELETE FROM meetings WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) listParticipants(ctx context.Context, meetingID string) ([]meetings.Participant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, meeting_id, name, email FROM participants WHERE meeting_id = ? ORDER BY name`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []meetings.Participant
	for rows.Next() {
		var p meetings.Participant
		if err := rows.Scan(&p.ID, &p.MeetingID, &p.Name, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanMeeting(row scannable) (*meetings.Meeting, error) {
	var m meetings.Meeting
	var status string
	var started, created, updated string
	var ended sql.NullString
	if err := row.Scan(&m.ID, &m.Title, &status, &started, &ended, &created, &updated); err != nil {
		return nil, err
	}
	m.Status = meetings.MeetingStatus(status)
	var err error
	if m.StartedAt, err = parseTime(started); err != nil {
		return nil, err
	}
	if m.EndedAt, err = scanNullTime(ended); err != nil {
		return nil, err
	}
	if m.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if m.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &m, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}

func insertEvent(ctx context.Context, tx *sql.Tx, meetingID, typ, payload string) error {
	id, err := newID("evt")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO meeting_events(id, meeting_id, type, payload, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, meetingID, typ, payload, formatTime(nowUTC()),
	)
	return err
}
