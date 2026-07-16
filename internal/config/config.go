package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds process-wide settings.
type Config struct {
	DataDir  string
	DBPath   string
	SpoolDir string

	// Daemon
	ListenAddr   string
	ControlToken string
	PIDFile      string
	TokenFile    string

	// Encryption env var name (not the secret).
	EncryptionEnv string

	// Providers
	TranscriptionProvider string
	TranscriptionModel    string
	TranscriptionLang     string
	WhisperBinary         string
	WhisperModelPath      string
	CommandTranscriber    string

	AnalysisProvider     string
	AnalysisModel        string
	AnalysisInterval     time.Duration
	AnalysisSegThreshold int

	// Audio
	MicDevice    string
	SystemDevice string
	ChunkSeconds int

	// OpenAI
	OpenAIBaseURL string
}

// Load resolves configuration from the environment with safe defaults.
func Load() (*Config, error) {
	dataDir := os.Getenv("MEETINGCTL_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		dataDir = filepath.Join(home, ".meetingctl")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	spoolDir := filepath.Join(dataDir, "spool")
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}

	dbPath := envOr("MEETINGCTL_DB", filepath.Join(dataDir, "meetings.db"))
	listen := envOr("MEETINGCTL_LISTEN", "127.0.0.1:7337")
	pidFile := envOr("MEETINGCTL_PID_FILE", filepath.Join(dataDir, "meetingd.pid"))
	tokenFile := envOr("MEETINGCTL_TOKEN_FILE", filepath.Join(dataDir, "control.token"))

	chunkSec := envInt("MEETINGCTL_CHUNK_SECONDS", 15)
	if chunkSec < 3 {
		chunkSec = 3
	}
	if chunkSec > 60 {
		chunkSec = 60
	}

	analysisInterval := 2 * time.Minute
	if v := os.Getenv("MEETINGCTL_ANALYSIS_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			analysisInterval = d
		}
	}

	cfg := &Config{
		DataDir:               dataDir,
		DBPath:                dbPath,
		SpoolDir:              spoolDir,
		ListenAddr:            listen,
		PIDFile:               pidFile,
		TokenFile:             tokenFile,
		EncryptionEnv:         "MEETINGCTL_ENCRYPTION_KEY",
		TranscriptionProvider: envOr("MEETINGCTL_TRANSCRIPTION_PROVIDER", "fixture"),
		TranscriptionModel:    envOr("MEETINGCTL_TRANSCRIPTION_MODEL", "gpt-4o-mini-transcribe"),
		TranscriptionLang:     envOr("MEETINGCTL_TRANSCRIPTION_LANG", "auto"),
		WhisperBinary:         envOr("MEETINGCTL_WHISPER_BINARY", filepath.Join(dataDir, "bin", "whisper-cli")),
		WhisperModelPath:      envOr("MEETINGCTL_WHISPER_MODEL", filepath.Join(dataDir, "models", "ggml-small.bin")),
		CommandTranscriber:    os.Getenv("MEETINGCTL_COMMAND_TRANSCRIBER"),
		AnalysisProvider:      envOr("MEETINGCTL_ANALYSIS_PROVIDER", "none"),
		AnalysisModel:         envOr("MEETINGCTL_ANALYSIS_MODEL", "gpt-4o-mini"),
		AnalysisInterval:      analysisInterval,
		AnalysisSegThreshold:  envInt("MEETINGCTL_ANALYSIS_SEG_THRESHOLD", 20),
		MicDevice:             os.Getenv("MEETINGCTL_MIC_DEVICE"),
		SystemDevice:          os.Getenv("MEETINGCTL_SYSTEM_DEVICE"),
		ChunkSeconds:          chunkSec,
		OpenAIBaseURL:         envOr("OPENAI_BASE_URL", "https://api.openai.com/v1"),
	}

	token, err := loadOrCreateToken(tokenFile)
	if err != nil {
		return nil, err
	}
	cfg.ControlToken = token
	return cfg, nil
}

// BaseURL returns the local daemon HTTP base URL.
func (c *Config) BaseURL() string {
	return "http://" + c.ListenAddr
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func loadOrCreateToken(path string) (string, error) {
	if t := strings.TrimSpace(os.Getenv("MEETINGCTL_CONTROL_TOKEN")); t != "" {
		return t, nil
	}
	if b, err := os.ReadFile(path); err == nil {
		t := strings.TrimSpace(string(b))
		if t != "" {
			return t, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write control token: %w", err)
	}
	return token, nil
}
