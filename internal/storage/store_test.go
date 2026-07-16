package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Omotolani98/meetingctl/internal/crypto"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/Omotolani98/meetingctl/internal/storage"
)

func testBox(t *testing.T) *crypto.Box {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	box, err := crypto.NewBox("test", key)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func openStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := storage.Open(context.Background(), path, testBox(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndActiveMeeting(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	m, err := s.CreateMeeting(ctx, "Arch Review", []string{"Tolani", "Sarah"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != meetings.StatusActive {
		t.Fatalf("status %s", m.Status)
	}
	if len(m.Participants) != 2 {
		t.Fatalf("participants %d", len(m.Participants))
	}
	active, err := s.GetActiveMeeting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != m.ID {
		t.Fatalf("active id %s want %s", active.ID, m.ID)
	}
	_, err = s.CreateMeeting(ctx, "Second", nil)
	if err == nil {
		t.Fatal("expected second active meeting to fail")
	}
}

func TestTranscriptSequenceAndIdempotency(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	m, err := s.CreateMeeting(ctx, "T", nil)
	if err != nil {
		t.Fatal(err)
	}
	seg1, err := s.InsertFinalSegment(ctx, m.ID, meetings.TranscriptSegment{
		Text: "hello", IsFinal: true, Idempotency: "a1", Speaker: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seg1.Sequence != 1 {
		t.Fatalf("seq %d", seg1.Sequence)
	}
	// duplicate idempotency
	seg1b, err := s.InsertFinalSegment(ctx, m.ID, meetings.TranscriptSegment{
		Text: "hello again", IsFinal: true, Idempotency: "a1", Speaker: "A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seg1b.ID != seg1.ID || seg1b.Text != "hello" {
		t.Fatalf("idempotency should return original: %+v", seg1b)
	}
	seg2, err := s.InsertFinalSegment(ctx, m.ID, meetings.TranscriptSegment{
		Text: "world", IsFinal: true, Idempotency: "a2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seg2.Sequence != 2 {
		t.Fatalf("seq %d", seg2.Sequence)
	}
	page, err := s.ListTranscript(ctx, m.ID, meetings.TranscriptFilter{SinceSequence: 1, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Sequence != 2 {
		t.Fatalf("since_sequence exclusive: %+v", page)
	}
}

func TestEncryptionAtRest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enc.db")
	ctx := context.Background()
	s, err := storage.Open(ctx, path, testBox(t))
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateMeeting(ctx, "Secret Meeting", nil)
	if err != nil {
		t.Fatal(err)
	}
	secret := "UNIQUE_PLAINTEXT_MARKER_XYZ"
	_, err = s.InsertFinalSegment(ctx, m.ID, meetings.TranscriptSegment{
		Text: secret, IsFinal: true, Idempotency: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("plaintext found in database file")
	}
}

func TestInsightProvenance(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	m, err := s.CreateMeeting(ctx, "I", nil)
	if err != nil {
		t.Fatal(err)
	}
	seg, err := s.InsertFinalSegment(ctx, m.ID, meetings.TranscriptSegment{
		Text: "we decided X", IsFinal: true, Idempotency: "i1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.InsertInsight(ctx, meetings.MeetingInsight{
		MeetingID: m.ID,
		Type:      meetings.InsightDecision,
		Text:      "Decided X",
		SourceIDs: []string{seg.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ins.SourceIDs) != 1 || ins.SourceIDs[0] != seg.ID {
		t.Fatalf("sources %+v", ins.SourceIDs)
	}
}

func TestCorrectSegment(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	m, err := s.CreateMeeting(ctx, "C", nil)
	if err != nil {
		t.Fatal(err)
	}
	seg, err := s.InsertFinalSegment(ctx, m.ID, meetings.TranscriptSegment{
		Text: "wrng", IsFinal: true, Idempotency: "c1",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := s.CorrectSegment(ctx, seg.ID, "wrong fixed")
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Text != "wrong fixed" || fixed.Revision != 1 {
		t.Fatalf("%+v", fixed)
	}
	if fixed.OriginalText != "wrng" {
		t.Fatalf("original %q", fixed.OriginalText)
	}
}
