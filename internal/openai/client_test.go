package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Omotolani98/meetingctl/internal/openai"
)

func TestTranscribeFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatal("missing auth")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "hello world"})
	}))
	defer srv.Close()

	cli := &openai.Client{
		APIKey:  "sk-test",
		BaseURL: srv.URL + "/v1",
		HTTP:    srv.Client(),
	}
	text, err := cli.TranscribeFile(context.Background(), "gpt-4o-mini-transcribe", "a.wav", []byte("RIFF"), "en")
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello world" {
		t.Fatalf("got %q", text)
	}
}

func TestResponsesCreateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate"}`))
	}))
	defer srv.Close()
	cli := &openai.Client{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := cli.ResponsesCreate(context.Background(), map[string]any{"model": "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}
