package auth_test

import (
	"os"
	"testing"

	"github.com/Omotolani98/meetingctl/internal/auth"
)

func TestCredentialRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCredential("openai", "api-key", "sk-test-secret"); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetCredential("openai", "api-key")
	if err != nil || v != "sk-test-secret" {
		t.Fatalf("got %q err %v", v, err)
	}
	if !s.HasCredential("openai", "api-key") {
		t.Fatal("expected has")
	}
	st := auth.State{Method: "api_key", Provider: "openai", Usage: []string{"transcription", "analysis"}}
	if err := s.SaveState(st); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadState()
	if err != nil || loaded.Method != "api_key" || loaded.Provider != "openai" {
		t.Fatalf("%+v %v", loaded, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if s.HasCredential("openai", "api-key") {
		t.Fatal("expected cleared")
	}
}

func TestSelectPrompter(t *testing.T) {
	in := "2\n"
	p := &auth.TerminalPrompter{
		In:  stringsReader(in),
		Out: discard{},
	}
	id, err := p.Select("pick", []auth.Option{
		{ID: "a", Label: "A"},
		{ID: "b", Label: "B"},
	})
	if err != nil || id != "b" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func stringsReader(s string) *stringReader { return &stringReader{s: s} }

type stringReader struct {
	s string
	i int
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, errEOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}

var errEOF = eof{}

type eof struct{}

func (eof) Error() string { return "EOF" }

func TestApplyToEnvClearsStaleSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveState(auth.State{Method: "none"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "stale")
	t.Setenv("CONTROL_PLANE_API_KEY", "stale")
	t.Setenv("MEETINGCTL_TRANSCRIPTION_PROVIDER", "openai")
	t.Setenv("MEETINGCTL_ANALYSIS_PROVIDER", "openai")
	if err := auth.ApplyToEnv(s); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"OPENAI_API_KEY", "CONTROL_PLANE_API_KEY", "MEETINGCTL_TRANSCRIPTION_PROVIDER", "MEETINGCTL_ANALYSIS_PROVIDER"} {
		if got := os.Getenv(k); got != "" {
			t.Fatalf("%s = %q, want cleared", k, got)
		}
	}
}

func TestApplyToEnvMissingCredentialClearsEnv(t *testing.T) {
	dir := t.TempDir()
	s, err := auth.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveState(auth.State{Method: "api_key", Provider: "openai"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "stale")
	if err := auth.ApplyToEnv(s); err == nil {
		t.Fatal("expected error")
	}
	if got := os.Getenv("OPENAI_API_KEY"); got != "" {
		t.Fatalf("OPENAI_API_KEY = %q, want cleared", got)
	}
}
