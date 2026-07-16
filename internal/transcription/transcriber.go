package transcription

import (
	"context"
	"time"
)

// AudioChunk is a unit of audio for transcription.
type AudioChunk struct {
	MeetingID string
	Index     int
	StartedAt time.Duration
	EndedAt   time.Duration
	// PCM16 mono at SampleRate, or empty when fixtures only supply metadata.
	PCM       []byte
	SampleRate int
	// FixturePath is optional path metadata for file-backed sources.
	FixturePath string
}

// TranscriptUpdate is a partial or final transcription result.
type TranscriptUpdate struct {
	Idempotency string
	Speaker     string
	Text        string
	StartedAt   time.Duration
	EndedAt     time.Duration
	Confidence  float64
	IsFinal     bool
}

// Transcriber converts audio chunks into transcript updates.
type Transcriber interface {
	Transcribe(ctx context.Context, chunk AudioChunk) ([]TranscriptUpdate, error)
}
