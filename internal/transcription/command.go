package transcription

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CommandTranscriber runs an external executable for STT.
// Contract: executable receives JSON on stdin and writes JSONL TranscriptUpdate on stdout.
type CommandTranscriber struct {
	// Command is the executable path (no shell).
	Command string
	// Args are extra fixed arguments.
	Args []string
}

type commandInput struct {
	AudioPath string `json:"audioPath"`
	MeetingID string `json:"meetingId"`
	Track     string `json:"track"`
	Index     int    `json:"index"`
	StartedMs int64  `json:"startedMs"`
	EndedMs   int64  `json:"endedMs"`
}

// Transcribe implements Transcriber.
func (c *CommandTranscriber) Transcribe(ctx context.Context, chunk AudioChunk) ([]TranscriptUpdate, error) {
	if c.Command == "" {
		return nil, fmt.Errorf("command transcriber not configured")
	}
	if len(chunk.PCM) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "meetingctl-chunk-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "chunk.wav")
	if err := os.WriteFile(path, chunk.PCM, 0o600); err != nil {
		return nil, err
	}
	in := commandInput{
		AudioPath: path,
		MeetingID: chunk.MeetingID,
		Track:     "audio",
		Index:     chunk.Index,
		StartedMs: chunk.StartedAt.Milliseconds(),
		EndedMs:   chunk.EndedAt.Milliseconds(),
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("command transcriber: %w: %s", err, truncate(stderr.String(), 300))
	}
	var out []TranscriptUpdate
	sc := bufio.NewScanner(&stdout)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var u TranscriptUpdate
		if err := json.Unmarshal([]byte(text), &u); err != nil {
			return nil, fmt.Errorf("command output line %d: %w", line, err)
		}
		if u.Idempotency == "" {
			u.Idempotency = fmt.Sprintf("cmd-%d-%d", chunk.Index, line)
		}
		if u.StartedAt == 0 {
			u.StartedAt = chunk.StartedAt
		}
		if u.EndedAt == 0 {
			u.EndedAt = chunk.EndedAt
		}
		// Prefer final for permanent store.
		if !u.IsFinal && u.Text != "" {
			u.IsFinal = true
		}
		out = append(out, u)
	}
	return out, sc.Err()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ Transcriber = (*CommandTranscriber)(nil)
var _ = time.Second
