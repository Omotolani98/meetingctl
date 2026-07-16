package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Omotolani98/meetingctl/internal/audio"
	"github.com/Omotolani98/meetingctl/internal/config"
	"github.com/Omotolani98/meetingctl/internal/insights"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/Omotolani98/meetingctl/internal/transcription"
)

// SessionManager owns the active capture session.
type SessionManager struct {
	Cfg     *config.Config
	Service *meetings.Service
	Log     *slog.Logger

	mu          sync.Mutex
	cancelIngest context.CancelFunc
	activeID    string
	source      string
	startedAt   time.Time
	ingesting   bool
	lastError   string
}

// StartOpts configures a capture session.
type StartOpts struct {
	Title        string
	Participants []string
	Source       string // none | fixture | mic | mic+system
	Input        string // fixture dir
}

// StatusSnapshot is the daemon runtime status.
type StatusSnapshot struct {
	Daemon       string `json:"daemon"`
	Listen       string `json:"listen"`
	Transcription string `json:"transcription_provider"`
	Analysis     string `json:"analysis_provider"`
	ActiveMeeting string `json:"active_meeting,omitempty"`
	Source       string `json:"source,omitempty"`
	Ingesting    bool   `json:"ingesting"`
	StartedAt    string `json:"session_started_at,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}

// Status returns the current daemon/session state.
func (s *SessionManager) Status(ctx context.Context) StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := StatusSnapshot{
		Daemon:        "running",
		Listen:        s.Cfg.ListenAddr,
		Transcription: s.Cfg.TranscriptionProvider,
		Analysis:      s.Cfg.AnalysisProvider,
		ActiveMeeting: s.activeID,
		Source:        s.source,
		Ingesting:     s.ingesting,
		LastError:     s.lastError,
	}
	if !s.startedAt.IsZero() {
		out.StartedAt = s.startedAt.Format(time.RFC3339)
	}
	// Prefer DB active meeting if present.
	if m, err := s.Service.Status(ctx); err == nil {
		out.ActiveMeeting = m.ID
	}
	return out
}

// Start creates a meeting and optionally begins ingestion.
func (s *SessionManager) Start(ctx context.Context, opts StartOpts) (*meetings.Meeting, int, error) {
	s.mu.Lock()
	if s.ingesting {
		s.mu.Unlock()
		return nil, 0, fmt.Errorf("capture already in progress")
	}
	s.mu.Unlock()

	source := opts.Source
	if source == "" {
		source = "none"
	}

	// Wire providers for this session when fixture input is provided.
	if source == "fixture" {
		if opts.Input == "" {
			return nil, 0, fmt.Errorf("input is required for fixture source")
		}
		if err := s.wireFixture(opts.Input); err != nil {
			return nil, 0, err
		}
	}

	m, err := s.Service.Start(ctx, meetings.StartRequest{
		Title:        opts.Title,
		Participants: opts.Participants,
	})
	if err != nil {
		return nil, 0, err
	}

	s.mu.Lock()
	s.activeID = m.ID
	s.source = source
	s.startedAt = time.Now().UTC()
	s.lastError = ""
	s.mu.Unlock()

	ingested := 0
	switch source {
	case "none":
		// Idle active meeting; capture not started.
	case "fixture":
		tr, err := transcription.LoadFixtureTranscriber(opts.Input)
		if err != nil {
			return m, 0, err
		}
		src := &audio.FixtureSource{MeetingID: m.ID, Count: len(tr.Updates)}
		s.mu.Lock()
		s.ingesting = true
		s.mu.Unlock()
		n, err := s.Service.IngestSource(ctx, m.ID, src)
		s.mu.Lock()
		s.ingesting = false
		if err != nil {
			s.lastError = err.Error()
		}
		s.mu.Unlock()
		if err != nil {
			return m, n, err
		}
		ingested = n
	case "mic", "mic+system", "system":
		return m, 0, fmt.Errorf("live audio source %q requires FFmpeg capture (not yet enabled in this build; use fixture or none)", source)
	default:
		return m, 0, fmt.Errorf("unsupported source %q", source)
	}
	return m, ingested, nil
}

// Stop ends capture (if any) and finalizes the meeting.
func (s *SessionManager) Stop(ctx context.Context, meetingID, fixtureInput string) (*meetings.Meeting, *meetings.Summary, error) {
	s.mu.Lock()
	if s.cancelIngest != nil {
		s.cancelIngest()
		s.cancelIngest = nil
	}
	s.ingesting = false
	s.mu.Unlock()

	if fixtureInput != "" {
		if err := s.wireFixture(fixtureInput); err != nil {
			// Non-fatal if analysis already configured.
			if s.Log != nil {
				s.Log.Warn("fixture analyzer", "err", err)
			}
		}
	}
	// Ensure analysis provider is configured from env if not fixture.
	s.ensureProviders()

	m, sum, err := s.Service.Stop(ctx, meetingID)
	if err != nil {
		s.mu.Lock()
		s.lastError = err.Error()
		s.mu.Unlock()
		return nil, nil, err
	}
	s.mu.Lock()
	s.activeID = ""
	s.source = ""
	s.startedAt = time.Time{}
	s.mu.Unlock()
	return m, sum, nil
}

func (s *SessionManager) wireFixture(dir string) error {
	tr, err := transcription.LoadFixtureTranscriber(dir)
	if err != nil {
		return err
	}
	an, err := insights.LoadFixtureAnalyzer(dir)
	if err != nil {
		return err
	}
	s.Service.Transcribe = tr
	s.Service.Analyze = an
	return nil
}

func (s *SessionManager) ensureProviders() {
	// Providers are set at daemon boot; fixture may override temporarily.
	if s.Service.Analyze == nil && s.Cfg.AnalysisProvider == "none" {
		// leave nil — service generates basic summary
	}
}

// IsCapturing reports whether live ingest is in progress.
func (s *SessionManager) IsCapturing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ingesting
}

// ReloadProviders rewires STT/analysis from auth store + config.
// Fails if a capture is in progress.
func (s *SessionManager) ReloadProviders(reload func() error) error {
	s.mu.Lock()
	if s.ingesting {
		s.mu.Unlock()
		return fmt.Errorf("cannot reload providers while capture is in progress")
	}
	s.mu.Unlock()
	return reload()
}

// InterruptActive marks an abandoned active meeting after crash recovery.
func (s *SessionManager) InterruptActive(ctx context.Context) error {
	m, err := s.Service.Status(ctx)
	if err != nil {
		return nil // none
	}
	// Mark failed so a new meeting can start; keep transcript.
	now := time.Now().UTC()
	_ = now
	return s.Service.Repo.SetMeetingStatus(ctx, m.ID, meetings.StatusFailed, &now)
}
