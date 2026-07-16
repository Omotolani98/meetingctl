package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultURL   = "https://models.dev/api.json"
	maxBodyBytes = 32 << 20
	cacheTTL     = 24 * time.Hour
)

// Strategy is how a provider authenticates.
type Strategy string

const (
	StrategyAPIKey Strategy = "api_key"
)

// Provider is a selectable auth/provider entry.
type Provider struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Supported     bool     `json:"supported"`
	Strategy      Strategy `json:"strategy"`
	ModelsDevID   string   `json:"modelsDevId,omitempty"`
	CredentialEnv string   `json:"credentialEnv,omitempty"`
	BaseURL       string   `json:"baseUrl,omitempty"`
	DocURL        string   `json:"docUrl,omitempty"`
	ModelCount    int      `json:"modelCount,omitempty"`
	Description   string   `json:"description,omitempty"`
}

// modelsDevProvider matches models.dev provider objects.
type modelsDevProvider struct {
	ID     string                     `json:"id"`
	Name   string                     `json:"name"`
	Env    []string                   `json:"env"`
	API    string                     `json:"api"`
	Doc    string                     `json:"doc"`
	Models map[string]json.RawMessage `json:"models"`
}

// Catalog holds curated supported providers plus models.dev browse list.
type Catalog struct {
	Supported []Provider
	Browse    []Provider
	Source    string // models.dev or offline-fallback
	FetchedAt time.Time
}

// Load builds a catalog: curated supported first, then models.dev browse providers.
func Load(ctx context.Context, cacheDir string) (*Catalog, error) {
	supported := curatedSupported()
	browse, source, fetched, err := loadModelsDev(ctx, cacheDir)
	if err != nil {
		// Offline fallback: still return curated list.
		return &Catalog{
			Supported: supported,
			Browse:    offlineBrowse(),
			Source:    "offline-fallback",
			FetchedAt: time.Now().UTC(),
		}, nil
	}
	// Exclude models.dev entries that are already represented as supported.
	supportedIDs := map[string]bool{"openai": true}
	filtered := make([]Provider, 0, len(browse))
	for _, p := range browse {
		if supportedIDs[p.ModelsDevID] || supportedIDs[p.ID] {
			continue
		}
		filtered = append(filtered, p)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
	})
	return &Catalog{
		Supported: supported,
		Browse:    filtered,
		Source:    source,
		FetchedAt: fetched,
	}, nil
}

// All returns supported then browse providers.
func (c *Catalog) All() []Provider {
	out := make([]Provider, 0, len(c.Supported)+len(c.Browse))
	out = append(out, c.Supported...)
	out = append(out, c.Browse...)
	return out
}

// Find returns a provider by ID from supported or browse lists.
func (c *Catalog) Find(id string) (Provider, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range c.All() {
		if strings.ToLower(p.ID) == id {
			return p, true
		}
	}
	return Provider{}, false
}

func curatedSupported() []Provider {
	return []Provider{
		{
			ID:            "openai",
			Name:          "OpenAI",
			Supported:     true,
			Strategy:      StrategyAPIKey,
			ModelsDevID:   "openai",
			CredentialEnv: "OPENAI_API_KEY",
			BaseURL:       "https://api.openai.com/v1",
			DocURL:        "https://platform.openai.com/docs",
			Description:   "API Key for transcription and analysis (Platform billing)",
		},
	}
}

func offlineBrowse() []Provider {
	return []Provider{
		{ID: "anthropic", Name: "Anthropic", Supported: false, Strategy: StrategyAPIKey, ModelsDevID: "anthropic", CredentialEnv: "ANTHROPIC_API_KEY", Description: "Not yet supported"},
		{ID: "google", Name: "Google", Supported: false, Strategy: StrategyAPIKey, ModelsDevID: "google", CredentialEnv: "GOOGLE_GENERATIVE_AI_API_KEY", Description: "Not yet supported"},
		{ID: "xai", Name: "xAI", Supported: false, Strategy: StrategyAPIKey, ModelsDevID: "xai", CredentialEnv: "XAI_API_KEY", Description: "Not yet supported"},
	}
}

func loadModelsDev(ctx context.Context, cacheDir string) ([]Provider, string, time.Time, error) {
	cachePath := filepath.Join(cacheDir, "models-dev.json")
	if b, info, err := readCache(cachePath); err == nil && time.Since(info.ModTime()) < cacheTTL {
		providers, err := parseModelsDev(b)
		if err == nil {
			return providers, "models.dev-cache", info.ModTime().UTC(), nil
		}
	}
	body, err := fetchModelsDev(ctx)
	if err != nil {
		// try stale cache
		if b, info, cerr := readCache(cachePath); cerr == nil {
			providers, perr := parseModelsDev(b)
			if perr == nil {
				return providers, "models.dev-stale-cache", info.ModTime().UTC(), nil
			}
		}
		return nil, "", time.Time{}, err
	}
	_ = writeCache(cachePath, body)
	providers, err := parseModelsDev(body)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	return providers, "models.dev", time.Now().UTC(), nil
}

func fetchModelsDev(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, defaultURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "meetingctl/0.2")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

func parseModelsDev(body []byte) ([]Provider, error) {
	var raw map[string]modelsDevProvider
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse models.dev: %w", err)
	}
	out := make([]Provider, 0, len(raw))
	for id, p := range raw {
		if p.ID == "" {
			p.ID = id
		}
		name := p.Name
		if name == "" {
			name = p.ID
		}
		env := ""
		if len(p.Env) > 0 {
			env = p.Env[0]
		}
		out = append(out, Provider{
			ID:            p.ID,
			Name:          name,
			Supported:     false,
			Strategy:      StrategyAPIKey,
			ModelsDevID:   p.ID,
			CredentialEnv: env,
			BaseURL:       p.API,
			DocURL:        p.Doc,
			ModelCount:    len(p.Models),
			Description:   "Browse only — not yet supported in meetingctl",
		})
	}
	return out, nil
}

func readCache(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return b, info, nil
}

func writeCache(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
