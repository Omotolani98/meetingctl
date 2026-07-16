package transcription

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FixtureTranscriber reads expected transcript updates from a JSONL fixture.
// Each non-empty line is a TranscriptUpdate JSON object.
type FixtureTranscriber struct {
	// ByIndex maps chunk index to updates. When empty, Updates is used for every call.
	ByIndex map[int][]TranscriptUpdate
	// Updates is the default list returned for chunk 0 / sequential drain mode.
	Updates []TranscriptUpdate
	// Sequential when true returns Updates[chunk.Index] as a single-element slice if present.
	Sequential bool
}

// LoadFixtureTranscriber loads transcript.jsonl from a fixture directory.
func LoadFixtureTranscriber(dir string) (*FixtureTranscriber, error) {
	path := filepath.Join(dir, "transcript.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open fixture transcript: %w", err)
	}
	defer f.Close()

	var updates []TranscriptUpdate
	sc := bufio.NewScanner(f)
	// allow long lines
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var u TranscriptUpdate
		if err := json.Unmarshal([]byte(line), &u); err != nil {
			return nil, fmt.Errorf("transcript.jsonl line %d: %w", lineNo, err)
		}
		if u.Idempotency == "" {
			u.Idempotency = fmt.Sprintf("fixture-line-%d", lineNo)
		}
		// Durations in JSON are milliseconds via helper fields if present.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err == nil {
			u.StartedAt = durationFromRaw(raw, "started_ms", "startedAt")
			u.EndedAt = durationFromRaw(raw, "ended_ms", "endedAt")
		}
		updates = append(updates, u)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &FixtureTranscriber{Updates: updates, Sequential: true}, nil
}

func durationFromRaw(raw map[string]json.RawMessage, msKey, altKey string) time.Duration {
	if v, ok := raw[msKey]; ok {
		var ms int64
		if json.Unmarshal(v, &ms) == nil {
			return time.Duration(ms) * time.Millisecond
		}
	}
	if v, ok := raw[altKey]; ok {
		var ms int64
		if json.Unmarshal(v, &ms) == nil {
			return time.Duration(ms) * time.Millisecond
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			if d, err := time.ParseDuration(s); err == nil {
				return d
			}
		}
	}
	return 0
}

// Transcribe returns fixture updates for the given chunk.
func (f *FixtureTranscriber) Transcribe(ctx context.Context, chunk AudioChunk) ([]TranscriptUpdate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.ByIndex != nil {
		if u, ok := f.ByIndex[chunk.Index]; ok {
			return cloneUpdates(u), nil
		}
	}
	if f.Sequential {
		if chunk.Index < 0 || chunk.Index >= len(f.Updates) {
			return nil, nil
		}
		return []TranscriptUpdate{f.Updates[chunk.Index]}, nil
	}
	if chunk.Index == 0 {
		return cloneUpdates(f.Updates), nil
	}
	return nil, nil
}

func cloneUpdates(in []TranscriptUpdate) []TranscriptUpdate {
	out := make([]TranscriptUpdate, len(in))
	copy(out, in)
	return out
}
