package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Omotolani98/meetingctl/internal/meetings"
)

// InsertInsight stores an insight and optional source segment links.
func (s *Store) InsertInsight(ctx context.Context, in meetings.MeetingInsight) (*meetings.MeetingInsight, error) {
	if !meetings.ValidInsightType(in.Type) {
		return nil, fmt.Errorf("invalid insight type %q", in.Type)
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, fmt.Errorf("insight text is required")
	}
	id := in.ID
	var err error
	if id == "" {
		id, err = newID("ins")
		if err != nil {
			return nil, err
		}
	}
	keyID, nonce, cipher, err := s.box.Seal(in.Text)
	if err != nil {
		return nil, err
	}
	status := in.Status
	if status == "" {
		status = "open"
	}
	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	isManual := 0
	if in.IsManual {
		isManual = 1
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO meeting_insights(
			id, meeting_id, type, text_key_id, text_nonce, text_cipher,
			owner, status, confidence, is_manual, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.MeetingID, string(in.Type), keyID, nonce, cipher,
		in.Owner, status, in.Confidence, isManual, formatTime(now), formatTime(now),
	)
	if err != nil {
		return nil, err
	}
	for _, sid := range in.SourceIDs {
		if sid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO insight_sources(insight_id, segment_id) VALUES (?, ?)`,
			id, sid,
		); err != nil {
			return nil, fmt.Errorf("link source %s: %w", sid, err)
		}
	}
	evt := "insight.created"
	if in.IsManual {
		evt = "insight.manual"
	}
	if err := insertEvent(ctx, tx, in.MeetingID, evt, string(in.Type)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetInsight(ctx, id)
}

// GetInsight loads one insight with sources.
func (s *Store) GetInsight(ctx context.Context, id string) (*meetings.MeetingInsight, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, meeting_id, type, text_key_id, text_nonce, text_cipher,
			owner, status, confidence, is_manual, created_at, updated_at
		FROM meeting_insights WHERE id = ?`, id)
	ins, err := s.scanInsight(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sources, err := s.listSources(ctx, id)
	if err != nil {
		return nil, err
	}
	ins.SourceIDs = sources
	return ins, nil
}

// ListInsights returns insights for a meeting, optionally filtered by type.
func (s *Store) ListInsights(ctx context.Context, meetingID string, typ *meetings.InsightType) ([]meetings.MeetingInsight, error) {
	q := `
		SELECT id, meeting_id, type, text_key_id, text_nonce, text_cipher,
			owner, status, confidence, is_manual, created_at, updated_at
		FROM meeting_insights WHERE meeting_id = ?`
	args := []any{meetingID}
	if typ != nil {
		q += ` AND type = ?`
		args = append(args, string(*typ))
	}
	q += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []meetings.MeetingInsight
	for rows.Next() {
		ins, err := s.scanInsight(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, *ins)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// Close rows before nested queries: MaxOpenConns may be 1.
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		sources, err := s.listSources(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].SourceIDs = sources
	}
	return out, nil
}

// UpsertSummary stores a new summary version for a meeting.
func (s *Store) UpsertSummary(ctx context.Context, meetingID, text string, throughSeq int64) (*meetings.Summary, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("summary text is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1 FROM meeting_summaries WHERE meeting_id = ?`,
		meetingID,
	).Scan(&version)
	if err != nil {
		return nil, err
	}
	id, err := newID("sum")
	if err != nil {
		return nil, err
	}
	keyID, nonce, cipher, err := s.box.Seal(text)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO meeting_summaries(
			id, meeting_id, text_key_id, text_nonce, text_cipher,
			version, through_sequence, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, meetingID, keyID, nonce, cipher, version, throughSeq,
		formatTime(now), formatTime(now),
	)
	if err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, meetingID, "summary.updated", fmt.Sprintf("v=%d", version)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetLatestSummary(ctx, meetingID)
}

// GetLatestSummary returns the newest summary for a meeting.
func (s *Store) GetLatestSummary(ctx context.Context, meetingID string) (*meetings.Summary, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, meeting_id, text_key_id, text_nonce, text_cipher,
			version, through_sequence, created_at, updated_at
		FROM meeting_summaries WHERE meeting_id = ?
		ORDER BY version DESC LIMIT 1`, meetingID)
	sum, err := s.scanSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sum, err
}

// InsertNote stores a manual note.
func (s *Store) InsertNote(ctx context.Context, meetingID, text string) (*meetings.ManualNote, error) {
	if err := meetings.ValidateNote(text); err != nil {
		return nil, err
	}
	id, err := newID("note")
	if err != nil {
		return nil, err
	}
	keyID, nonce, cipher, err := s.box.Seal(strings.TrimSpace(text))
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO manual_notes(id, meeting_id, text_key_id, text_nonce, text_cipher, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, meetingID, keyID, nonce, cipher, formatTime(now),
	)
	if err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, meetingID, "note.added", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &meetings.ManualNote{
		ID:        id,
		MeetingID: meetingID,
		Text:      strings.TrimSpace(text),
		CreatedAt: now,
	}, nil
}

// ListNotes returns manual notes for a meeting.
func (s *Store) ListNotes(ctx context.Context, meetingID string) ([]meetings.ManualNote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, meeting_id, text_key_id, text_nonce, text_cipher, created_at
		FROM manual_notes WHERE meeting_id = ? ORDER BY created_at ASC`, meetingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []meetings.ManualNote
	for rows.Next() {
		var n meetings.ManualNote
		var keyID, nonce, cipher, created string
		if err := rows.Scan(&n.ID, &n.MeetingID, &keyID, &nonce, &cipher, &created); err != nil {
			return nil, err
		}
		text, err := s.box.Open(keyID, nonce, cipher)
		if err != nil {
			return nil, err
		}
		n.Text = text
		if n.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) listSources(ctx context.Context, insightID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT segment_id FROM insight_sources WHERE insight_id = ? ORDER BY segment_id`, insightID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) scanInsight(row scannable) (*meetings.MeetingInsight, error) {
	var ins meetings.MeetingInsight
	var typ string
	var keyID, nonce, cipher string
	var isManual int
	var created, updated string
	if err := row.Scan(
		&ins.ID, &ins.MeetingID, &typ, &keyID, &nonce, &cipher,
		&ins.Owner, &ins.Status, &ins.Confidence, &isManual, &created, &updated,
	); err != nil {
		return nil, err
	}
	text, err := s.box.Open(keyID, nonce, cipher)
	if err != nil {
		return nil, err
	}
	ins.Text = text
	ins.Type = meetings.InsightType(typ)
	ins.IsManual = isManual == 1
	if ins.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if ins.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &ins, nil
}

func (s *Store) scanSummary(row scannable) (*meetings.Summary, error) {
	var sum meetings.Summary
	var keyID, nonce, cipher string
	var created, updated string
	if err := row.Scan(
		&sum.ID, &sum.MeetingID, &keyID, &nonce, &cipher,
		&sum.Version, &sum.ThroughSequence, &created, &updated,
	); err != nil {
		return nil, err
	}
	text, err := s.box.Open(keyID, nonce, cipher)
	if err != nil {
		return nil, err
	}
	sum.Text = text
	if sum.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if sum.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &sum, nil
}
