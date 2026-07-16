package transcription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Omotolani98/meetingctl/internal/openai"
)

// OpenAITranscriber uses the OpenAI audio transcriptions API.
type OpenAITranscriber struct {
	Client   *openai.Client
	Model    string
	Language string
}

// Transcribe implements Transcriber.
// When chunk.PCM is empty, it returns no updates (fixture/metadata-only chunks).
func (t *OpenAITranscriber) Transcribe(ctx context.Context, chunk AudioChunk) ([]TranscriptUpdate, error) {
	if len(chunk.PCM) == 0 && chunk.FixturePath == "" {
		return nil, nil
	}
	if t.Client == nil {
		return nil, fmt.Errorf("openai client is nil")
	}
	model := t.Model
	if model == "" {
		model = "gpt-4o-mini-transcribe"
	}

	var audio []byte
	var name string
	if len(chunk.PCM) > 0 {
		// Expect WAV-wrapped PCM from capture; if raw PCM, wrap minimally.
		audio = chunk.PCM
		name = "chunk.wav"
	} else {
		return nil, fmt.Errorf("openai transcriber requires PCM audio bytes")
	}

	text, err := t.Client.TranscribeFile(ctx, model, name, audio, t.Language)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	sum := sha256.Sum256(audio)
	id := fmt.Sprintf("oai-%d-%s", chunk.Index, hex.EncodeToString(sum[:8]))
	return []TranscriptUpdate{{
		Idempotency: id,
		Text:        text,
		StartedAt:   chunk.StartedAt,
		EndedAt:     chunk.EndedAt,
		Confidence:  0,
		IsFinal:     true,
	}}, nil
}

// Ensure interface compliance.
var _ Transcriber = (*OpenAITranscriber)(nil)

// Idle check uses time package for future streaming timestamps.
var _ = time.Second
