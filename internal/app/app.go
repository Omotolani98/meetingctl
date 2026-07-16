package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Omotolani98/meetingctl/internal/config"
	"github.com/Omotolani98/meetingctl/internal/crypto"
	"github.com/Omotolani98/meetingctl/internal/insights"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/Omotolani98/meetingctl/internal/openai"
	"github.com/Omotolani98/meetingctl/internal/storage"
	"github.com/Omotolani98/meetingctl/internal/transcription"
)

// App wires config, store, and meeting service.
type App struct {
	Config  *config.Config
	Store   *storage.Store
	Service *meetings.Service
	Log     *slog.Logger
}

// Options configures optional providers for a session.
type Options struct {
	// FixtureDir when set loads fixture transcriber + analyzer.
	FixtureDir string
	// SkipProviders when true leaves providers unset (CLI direct mode).
	SkipProviders bool
	Logger        *slog.Logger
}

// Open loads config, opens the encrypted store, and builds the service.
func Open(ctx context.Context, opts Options) (*App, error) {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	box, err := crypto.NewBoxFromEnv()
	if err != nil {
		return nil, err
	}
	store, err := storage.Open(ctx, cfg.DBPath, box)
	if err != nil {
		return nil, err
	}
	svc := &meetings.Service{
		Repo: store,
		Log:  log,
	}
	if opts.FixtureDir != "" {
		tr, err := transcription.LoadFixtureTranscriber(opts.FixtureDir)
		if err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("fixture transcriber: %w", err)
		}
		an, err := insights.LoadFixtureAnalyzer(opts.FixtureDir)
		if err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("fixture analyzer: %w", err)
		}
		svc.Transcribe = tr
		svc.Analyze = an
	} else if !opts.SkipProviders {
		if err := WireProviders(cfg, svc); err != nil {
			// Non-fatal for read-only operations: log and continue.
			log.Warn("provider wiring", "err", err)
		}
	}
	return &App{Config: cfg, Store: store, Service: svc, Log: log}, nil
}

// WireProviders configures transcription and analysis from config.
func WireProviders(cfg *config.Config, svc *meetings.Service) error {
	var errs []string

	switch strings.ToLower(cfg.TranscriptionProvider) {
	case "", "none", "fixture":
		// left unset unless fixture dir provided
	case "openai":
		cli, err := openai.NewFromEnv(cfg.OpenAIBaseURL)
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			svc.Transcribe = &transcription.OpenAITranscriber{
				Client:   cli,
				Model:    cfg.TranscriptionModel,
				Language: cfg.TranscriptionLang,
			}
		}
	case "whispercpp", "whisper":
		svc.Transcribe = &transcription.WhisperCPPTranscriber{
			Binary:    cfg.WhisperBinary,
			ModelPath: cfg.WhisperModelPath,
			Language:  cfg.TranscriptionLang,
		}
	case "command":
		if cfg.CommandTranscriber == "" {
			errs = append(errs, "MEETINGCTL_COMMAND_TRANSCRIBER required for command provider")
		} else {
			svc.Transcribe = &transcription.CommandTranscriber{Command: cfg.CommandTranscriber}
		}
	default:
		errs = append(errs, fmt.Sprintf("unknown transcription provider %q", cfg.TranscriptionProvider))
	}

	switch strings.ToLower(cfg.AnalysisProvider) {
	case "", "none":
		// optional
	case "openai":
		cli, err := openai.NewFromEnv(cfg.OpenAIBaseURL)
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			svc.Analyze = &insights.OpenAIAnalyzer{Client: cli, Model: cfg.AnalysisModel}
		}
	case "fixture":
		// requires explicit fixture dir
	default:
		errs = append(errs, fmt.Sprintf("unknown analysis provider %q", cfg.AnalysisProvider))
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Close releases resources.
func (a *App) Close() error {
	if a.Store != nil {
		return a.Store.Close()
	}
	return nil
}
