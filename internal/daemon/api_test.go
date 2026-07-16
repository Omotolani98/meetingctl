package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Omotolani98/meetingctl/internal/config"
	"github.com/Omotolani98/meetingctl/internal/crypto"
	"github.com/Omotolani98/meetingctl/internal/daemon"
	"github.com/Omotolani98/meetingctl/internal/insights"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/Omotolani98/meetingctl/internal/storage"
	"github.com/Omotolani98/meetingctl/internal/transcription"
)

func TestDaemonAPIFixtureFlow(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 11)
	}
	box, err := crypto.NewBox("test", key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(dir, "t.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	fixture := filepath.Join("..", "..", "testdata", "platform-review")
	tr, err := transcription.LoadFixtureTranscriber(fixture)
	if err != nil {
		t.Fatal(err)
	}
	an, err := insights.LoadFixtureAnalyzer(fixture)
	if err != nil {
		t.Fatal(err)
	}
	svc := &meetings.Service{Repo: store, Transcribe: tr, Analyze: an}
	cfg := &config.Config{
		ListenAddr:            "127.0.0.1:0",
		ControlToken:          "test-token",
		TranscriptionProvider: "fixture",
		AnalysisProvider:      "fixture",
		DataDir:               dir,
	}
	// pick free port via Start which listens on configured addr — use fixed high port for test
	cfg.ListenAddr = "127.0.0.1:18737"
	sess := &daemon.SessionManager{Cfg: cfg, Service: svc}
	api := &daemon.API{Cfg: cfg, Service: svc, Store: store, Session: sess}
	if err := api.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sh, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = api.Shutdown(sh)
	})

	// unauthorized
	resp, err := http.Get("http://127.0.0.1:18737/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// start fixture meeting
	body := map[string]any{
		"title": "API Test", "participants": []string{"A"},
		"source": "fixture", "input": fixture,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:18737/v1/meetings", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start status %d", resp.StatusCode)
	}
	var startOut map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&startOut)
	if startOut["ingested_segments"].(float64) != 5 {
		t.Fatalf("ingested %+v", startOut)
	}

	// stop
	stopBody, _ := json.Marshal(map[string]string{"input": fixture})
	req, _ = http.NewRequest(http.MethodPost, "http://127.0.0.1:18737/v1/meetings/current/stop",
		bytes.NewReader(stopBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop status %d", resp.StatusCode)
	}
}
