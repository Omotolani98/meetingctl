package transcription

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WhisperCPPTranscriber shells out to a whisper.cpp CLI binary.
// Expected CLI: whisper-cli -m model -f file -nt -np (or compatible).
type WhisperCPPTranscriber struct {
	Binary    string
	ModelPath string
	Language  string
}

// Transcribe implements Transcriber.
func (w *WhisperCPPTranscriber) Transcribe(ctx context.Context, chunk AudioChunk) ([]TranscriptUpdate, error) {
	if len(chunk.PCM) == 0 {
		return nil, nil
	}
	if w.Binary == "" || w.ModelPath == "" {
		return nil, fmt.Errorf("whisper.cpp binary/model not configured")
	}
	if _, err := os.Stat(w.Binary); err != nil {
		return nil, fmt.Errorf("whisper binary: %w", err)
	}
	if _, err := os.Stat(w.ModelPath); err != nil {
		return nil, fmt.Errorf("whisper model: %w", err)
	}
	dir, err := os.MkdirTemp("", "meetingctl-whisper-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	wav := filepath.Join(dir, "chunk.wav")
	if err := os.WriteFile(wav, chunk.PCM, 0o600); err != nil {
		return nil, err
	}

	args := []string{"-m", w.ModelPath, "-f", wav, "-nt", "-np"}
	if w.Language != "" && w.Language != "auto" {
		args = append(args, "-l", w.Language)
	}
	cmd := exec.CommandContext(ctx, w.Binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("whisper.cpp: %w: %s", err, truncate(string(out), 400))
	}
	text := strings.TrimSpace(string(out))
	// Some CLIs print progress; keep last non-empty line as transcript.
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" && !strings.HasPrefix(l, "[") {
			text = l
			break
		}
	}
	if text == "" {
		return nil, nil
	}
	return []TranscriptUpdate{{
		Idempotency: fmt.Sprintf("wcpp-%d-%d", chunk.Index, chunk.StartedAt.Milliseconds()),
		Text:        text,
		StartedAt:   chunk.StartedAt,
		EndedAt:     chunk.EndedAt,
		IsFinal:     true,
	}}, nil
}

var _ Transcriber = (*WhisperCPPTranscriber)(nil)
var _ = time.Second
