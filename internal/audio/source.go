package audio

import (
	"context"
	"time"

	"github.com/Omotolani98/meetingctl/internal/transcription"
)

// Source produces audio chunks for a meeting session.
type Source interface {
	// Chunks returns chunks until the source is exhausted or ctx is cancelled.
	Chunks(ctx context.Context) (<-chan transcription.AudioChunk, <-chan error)
}

// FixtureSource emits one empty chunk per transcript line so the fixture
// transcriber can return sequential updates without real PCM.
type FixtureSource struct {
	MeetingID string
	Count     int
	// Step is the simulated duration between chunks.
	Step time.Duration
}

// Chunks implements Source.
func (f *FixtureSource) Chunks(ctx context.Context) (<-chan transcription.AudioChunk, <-chan error) {
	out := make(chan transcription.AudioChunk)
	errs := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		step := f.Step
		if step <= 0 {
			step = 2 * time.Second
		}
		n := f.Count
		if n <= 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case out <- transcription.AudioChunk{
				MeetingID:  f.MeetingID,
				Index:      i,
				StartedAt:  time.Duration(i) * step,
				EndedAt:    time.Duration(i+1) * step,
				SampleRate: 16000,
			}:
			}
		}
	}()
	return out, errs
}
