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

// InsertFinalSegment stores a finalized transcript segment with the next sequence number.
// Duplicate idempotency keys for the same meeting return the existing segment without error.
func (s *Store) InsertFinalSegment(ctx context.Context, meetingID string, in meetings.TranscriptSegment) (*meetings.TranscriptSegment, error) {
	if !in.IsFinal {
		return nil, fmt.Errorf("only finalized segments may be persisted")
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, fmt.Errorf("segment text is required")
	}
	if in.Idempotency == "" {
		return nil, fmt.Errorf("idempotency key is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Existing by idempotency?
	var existingID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM transcript_segments WHERE meeting_id = ? AND idempotency = ?`,
		meetingID, in.Idempotency,
	).Scan(&existingID)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.GetSegment(ctx, existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var nextSeq int64
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1 FROM transcript_segments WHERE meeting_id = ?`,
		meetingID,
	).Scan(&nextSeq)
	if err != nil {
		return nil, err
	}

	id := in.ID
	if id == "" {
		id, err = newID("seg")
		if err != nil {
			return nil, err
		}
	}
	keyID, nonce, cipher, err := s.box.Seal(in.Text)
	if err != nil {
		return nil, err
	}
	now := nowUTC()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO transcript_segments(
			id, meeting_id, sequence, speaker,
			text_key_id, text_nonce, text_cipher,
			started_ms, ended_ms, confidence, is_final, idempotency, revision,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, 0, ?, ?)`,
		id, meetingID, nextSeq, in.Speaker,
		keyID, nonce, cipher,
		in.StartedAt.Milliseconds(), in.EndedAt.Milliseconds(),
		in.Confidence, in.Idempotency,
		formatTime(now), formatTime(now),
	)
	if err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, meetingID, "transcript.finalized", fmt.Sprintf("seq=%d", nextSeq)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSegment(ctx, id)
}

// GetSegment loads one segment by ID.
func (s *Store) GetSegment(ctx context.Context, id string) (*meetings.TranscriptSegment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, meeting_id, sequence, speaker,
			text_key_id, text_nonce, text_cipher,
			started_ms, ended_ms, confidence, is_final, idempotency, revision,
			original_key_id, original_nonce, original_cipher,
			created_at, updated_at
		FROM transcript_segments WHERE id = ?`, id)
	seg, err := s.scanSegment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return seg, err
}

// ListTranscript returns finalized segments for a meeting with optional filters.
func (s *Store) ListTranscript(ctx context.Context, meetingID string, f meetings.TranscriptFilter) ([]meetings.TranscriptSegment, error) {
	limit := meetings.NormalizeLimit(f.Limit)
	q := `
		SELECT id, meeting_id, sequence, speaker,
			text_key_id, text_nonce, text_cipher,
			started_ms, ended_ms, confidence, is_final, idempotency, revision,
			original_key_id, original_nonce, original_cipher,
			created_at, updated_at
		FROM transcript_segments
		WHERE meeting_id = ? AND is_final = 1 AND sequence > ?`
	args := []any{meetingID, f.SinceSequence}
	if f.Speaker != "" {
		q += ` AND speaker = ?`
		args = append(args, f.Speaker)
	}
	q += ` ORDER BY sequence ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []meetings.TranscriptSegment
	for rows.Next() {
		seg, err := s.scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *seg)
	}
	return out, rows.Err()
}

// CorrectSegment revises segment text while preserving the previous text.
func (s *Store) CorrectSegment(ctx context.Context, segmentID, newText string) (*meetings.TranscriptSegment, error) {
	newText = strings.TrimSpace(newText)
	if newText == "" {
		return nil, fmt.Errorf("corrected text is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var meetingID, keyID, nonce, cipher string
	var revision int
	err = tx.QueryRowContext(ctx, `
		SELECT meeting_id, text_key_id, text_nonce, text_cipher, revision
		FROM transcript_segments WHERE id = ?`, segmentID,
	).Scan(&meetingID, &keyID, &nonce, &cipher, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	oldText, err := s.box.Open(keyID, nonce, cipher)
	if err != nil {
		return nil, err
	}
	newKey, newNonce, newCipher, err := s.box.Seal(newText)
	if err != nil {
		return nil, err
	}
	// Preserve first original if not already set.
	var origKey, origNonce, origCipher sql.NullString
	_ = tx.QueryRowContext(ctx, `
		SELECT original_key_id, original_nonce, original_cipher
		FROM transcript_segments WHERE id = ?`, segmentID,
	).Scan(&origKey, &origNonce, &origCipher)

	okey, ononce, ocipher := keyID, nonce, cipher
	if origKey.Valid && origKey.String != "" {
		okey, ononce, ocipher = origKey.String, origNonce.String, origCipher.String
		_ = oldText // already have original sealed
	} else {
		// seal original from current plaintext for consistency
		okey, ononce, ocipher, err = s.box.Seal(oldText)
		if err != nil {
			return nil, err
		}
	}

	now := nowUTC()
	_, err = tx.ExecContext(ctx, `
		UPDATE transcript_segments SET
			text_key_id = ?, text_nonce = ?, text_cipher = ?,
			original_key_id = ?, original_nonce = ?, original_cipher = ?,
			revision = ?, updated_at = ?
		WHERE id = ?`,
		newKey, newNonce, newCipher,
		okey, ononce, ocipher,
		revision+1, formatTime(now), segmentID,
	)
	if err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, meetingID, "transcript.corrected", segmentID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSegment(ctx, segmentID)
}

func (s *Store) scanSegment(row scannable) (*meetings.TranscriptSegment, error) {
	var seg meetings.TranscriptSegment
	var keyID, nonce, cipher string
	var startedMs, endedMs int64
	var isFinal int
	var origKey, origNonce, origCipher sql.NullString
	var created, updated string
	if err := row.Scan(
		&seg.ID, &seg.MeetingID, &seg.Sequence, &seg.Speaker,
		&keyID, &nonce, &cipher,
		&startedMs, &endedMs, &seg.Confidence, &isFinal, &seg.Idempotency, &seg.Revision,
		&origKey, &origNonce, &origCipher,
		&created, &updated,
	); err != nil {
		return nil, err
	}
	text, err := s.box.Open(keyID, nonce, cipher)
	if err != nil {
		return nil, err
	}
	seg.Text = text
	seg.StartedAt = time.Duration(startedMs) * time.Millisecond
	seg.EndedAt = time.Duration(endedMs) * time.Millisecond
	seg.IsFinal = isFinal == 1
	if origKey.Valid && origKey.String != "" {
		orig, err := s.box.Open(origKey.String, origNonce.String, origCipher.String)
		if err != nil {
			return nil, err
		}
		seg.OriginalText = orig
	}
	if seg.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if seg.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &seg, nil
}
