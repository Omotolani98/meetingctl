package meetings_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Omotolani98/meetingctl/internal/audio"
	"github.com/Omotolani98/meetingctl/internal/crypto"
	"github.com/Omotolani98/meetingctl/internal/insights"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/Omotolani98/meetingctl/internal/storage"
	"github.com/Omotolani98/meetingctl/internal/transcription"
)

func openService(t *testing.T, fixtureDir string) *meetings.Service {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	box, err := crypto.NewBox("test", key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := &meetings.Service{Repo: store}
	if fixtureDir != "" {
		tr, err := transcription.LoadFixtureTranscriber(fixtureDir)
		if err != nil {
			t.Fatal(err)
		}
		an, err := insights.LoadFixtureAnalyzer(fixtureDir)
		if err != nil {
			t.Fatal(err)
		}
		svc.Transcribe = tr
		svc.Analyze = an
	}
	return svc
}

func TestFixtureVerticalSlice(t *testing.T) {
	// Resolve fixture relative to module root via testdata path from this package.
	// Tests run with cwd = package dir, so walk up.
	fixture := filepath.Join("..", "..", "testdata", "platform-review")
	if _, err := transcription.LoadFixtureTranscriber(fixture); err != nil {
		// try from repo root
		fixture = filepath.Join("testdata", "platform-review")
	}
	svc := openService(t, fixture)
	ctx := context.Background()

	m, err := svc.Start(ctx, meetings.StartRequest{
		Title:        "Platform Architecture Review",
		Participants: []string{"Tolani", "Sarah", "Daniel"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := transcription.LoadFixtureTranscriber(fixture)
	if err != nil {
		t.Fatal(err)
	}
	src := &audio.FixtureSource{MeetingID: m.ID, Count: len(tr.Updates)}
	n, err := svc.IngestSource(ctx, m.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("ingested %d want 5", n)
	}

	// partials should not pollute — already only finals in fixture
	segs, err := svc.GetTranscript(ctx, m.ID, meetings.TranscriptFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 5 || segs[0].Sequence != 1 {
		t.Fatalf("segments %+v", segs)
	}

	// since_sequence
	page, err := svc.GetTranscript(ctx, m.ID, meetings.TranscriptFilter{SinceSequence: 3, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Sequence != 4 {
		t.Fatalf("page %+v", page)
	}

	_, err = svc.AddNote(ctx, m.ID, "Daniel owns the Redis migration investigation")
	if err != nil {
		t.Fatal(err)
	}

	done, sum, err := svc.Stop(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != meetings.StatusCompleted {
		t.Fatalf("status %s", done.Status)
	}
	if sum == nil || sum.Text == "" {
		t.Fatal("expected summary")
	}
	actions, err := svc.GetActionItems(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Owner != "Daniel" {
		t.Fatalf("actions %+v", actions)
	}
	if len(actions[0].SourceIDs) == 0 {
		t.Fatal("expected provenance source ids")
	}
	decisions, err := svc.GetDecisions(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions %+v", decisions)
	}

	// idempotent stop
	_, _, err = svc.Stop(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSkipPartials(t *testing.T) {
	key := make([]byte, 32)
	box, _ := crypto.NewBox("t", key)
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "p.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := &meetings.Service{
		Repo: store,
		Transcribe: &transcription.FixtureTranscriber{
			Updates: []transcription.TranscriptUpdate{
				{Idempotency: "p0", Text: "partial", IsFinal: false},
				{Idempotency: "f0", Text: "final", IsFinal: true},
			},
			Sequential: true,
		},
	}
	ctx := context.Background()
	m, err := svc.Start(ctx, meetings.StartRequest{Title: "P"})
	if err != nil {
		t.Fatal(err)
	}
	src := &audio.FixtureSource{MeetingID: m.ID, Count: 2}
	n, err := svc.IngestSource(ctx, m.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("persisted %d want 1", n)
	}
}
