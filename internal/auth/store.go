package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// State is non-secret auth configuration.
type State struct {
	Method   string   `json:"method"`          // api_key | none
	Provider string   `json:"provider"`        // openai | ...
	Usage    []string `json:"usage,omitempty"` // transcription, analysis
}

// Store persists auth state and credentials with user-only file permissions.
type Store struct {
	Dir string
	mu  sync.Mutex
}

// OpenStore creates a store under dataDir/auth.
func OpenStore(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, "auth")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) statePath() string { return filepath.Join(s.Dir, "state.json") }
func (s *Store) credPath(provider, name string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, provider+"__"+name)
	return filepath.Join(s.Dir, "creds", safe)
}

// LoadState returns auth state (empty if missing).
func (s *Store) LoadState() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return State{Method: "none"}, nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	if st.Method == "" {
		st.Method = "none"
	}
	return st, nil
}

// SaveState writes non-secret state.
func (s *Store) SaveState(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath())
}

// SetCredential stores a secret value.
func (s *Store) SetCredential(provider, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(s.Dir, "creds"), 0o700); err != nil {
		return err
	}
	path := s.credPath(provider, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// GetCredential reads a secret value.
func (s *Store) GetCredential(provider, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.credPath(provider, name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("credential not found")
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// DeleteCredential removes a secret.
func (s *Store) DeleteCredential(provider, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.credPath(provider, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Clear removes state and all credentials.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.statePath())
	_ = os.RemoveAll(filepath.Join(s.Dir, "creds"))
	return os.MkdirAll(filepath.Join(s.Dir, "creds"), 0o700)
}

// HasCredential reports whether a credential exists.
func (s *Store) HasCredential(provider, name string) bool {
	_, err := s.GetCredential(provider, name)
	return err == nil
}
