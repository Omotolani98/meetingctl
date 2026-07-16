package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Omotolani98/meetingctl/internal/catalog"
	"github.com/Omotolani98/meetingctl/internal/config"
)

// Service orchestrates auth flows.
type Service struct {
	Store    *Store
	Cfg      *config.Config
	Prompter Prompter
	Out      io.Writer
	// HTTP is used for key validation and daemon reload.
	HTTP *http.Client
}

// RunInteractive runs the main auth selection flow.
func (s *Service) RunInteractive(ctx context.Context) error {
	method, err := s.Prompter.Select("Choose authentication method:", []Option{
		{ID: "api_key", Label: "API Key", Description: "Provider backend key for STT/analysis"},
		{ID: "subscription", Label: "ChatGPT Subscription", Description: "Connect meeting memory to ChatGPT via MCP"},
	})
	if err != nil {
		return err
	}
	switch method {
	case "api_key":
		return s.runAPIKey(ctx)
	case "subscription":
		return s.runSubscription(ctx)
	default:
		return fmt.Errorf("unknown method %q", method)
	}
}

func (s *Service) runAPIKey(ctx context.Context) error {
	cat, err := catalog.Load(ctx, s.cacheDir())
	if err != nil {
		return err
	}
	opts := make([]Option, 0, len(cat.Supported)+len(cat.Browse)+1)
	fmt.Fprintln(s.Out, "\nSupported:")
	for _, p := range cat.Supported {
		if p.Strategy != catalog.StrategyAPIKey {
			continue
		}
		opts = append(opts, Option{ID: p.ID, Label: p.Name, Description: p.Description})
	}
	// Show browse section header in UI text
	fmt.Fprintln(s.Out, "\nBrowse models.dev providers:")
	for _, p := range cat.Browse {
		desc := "Not yet supported"
		if p.ModelCount > 0 {
			desc = fmt.Sprintf("Not yet supported · %d models", p.ModelCount)
		}
		opts = append(opts, Option{ID: p.ID, Label: p.Name, Description: desc})
	}
	id, err := s.Prompter.Select("Choose AI provider:", opts)
	if err != nil {
		return err
	}
	p, ok := cat.Find(id)
	if !ok {
		return fmt.Errorf("unknown provider %q", id)
	}
	if !p.Supported {
		fmt.Fprintf(s.Out, "\n%s is listed on models.dev but not yet supported by meetingctl.\n", p.Name)
		if p.DocURL != "" {
			fmt.Fprintf(s.Out, "Docs: %s\n", p.DocURL)
		}
		if p.CredentialEnv != "" {
			fmt.Fprintf(s.Out, "Would use env: %s (not collected)\n", p.CredentialEnv)
		}
		fmt.Fprintln(s.Out, "No credentials were stored.")
		return nil
	}
	return s.configureOpenAIAPIKey(ctx, p)
}

func (s *Service) configureOpenAIAPIKey(ctx context.Context, p catalog.Provider) error {
	key, err := s.Prompter.Secret("API key")
	if err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("API key is required")
	}
	usageID, err := s.Prompter.Select("Use for:", []Option{
		{ID: "both", Label: "Transcription and analysis"},
		{ID: "transcription", Label: "Transcription only"},
		{ID: "analysis", Label: "Analysis only"},
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(s.Out, "Validating credentials...")
	if err := validateOpenAIKey(ctx, s.httpClient(), p.BaseURL, key); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	if err := s.Store.SetCredential("openai", "api-key", key); err != nil {
		return err
	}
	usage := usageFromID(usageID)
	st := State{
		Method:   "api_key",
		Provider: "openai",
		Usage:    usage,
	}
	if err := s.Store.SaveState(st); err != nil {
		return err
	}
	// Also export-friendly env file for daemon (restricted).
	if err := s.writeEnvHint(key, usage); err != nil {
		fmt.Fprintf(s.Out, "warn: could not write env hint: %v\n", err)
	}
	fmt.Fprintln(s.Out, "Credentials saved securely.")
	if err := s.reloadDaemon(ctx); err != nil {
		fmt.Fprintf(s.Out, "warn: daemon reload: %v\n(restart meetingd to apply)\n", err)
	} else {
		fmt.Fprintln(s.Out, "meetingd providers reloaded.")
	}
	return nil
}

func (s *Service) runSubscription(ctx context.Context) error {
	fmt.Fprintln(s.Out, "\nChatGPT Subscription uses MCP, not backend API calls.")
	fmt.Fprintln(s.Out, "The Secure MCP Tunnel requires a Platform runtime key for transport only.")
	fmt.Fprintln(s.Out)

	// Check meetingd
	if err := s.checkDaemon(ctx); err != nil {
		fmt.Fprintf(s.Out, "meetingd: FAIL (%v)\n", err)
		fmt.Fprintln(s.Out, "Start meetingd first, then re-run: meetingctl auth")
		return err
	}
	fmt.Fprintln(s.Out, "meetingd: ok")
	fmt.Fprintln(s.Out, "MCP endpoint: http://127.0.0.1:7338/mcp (default)")

	tunnelID, err := s.Prompter.Text("Tunnel ID (from platform.openai.com tunnels)")
	if err != nil {
		return err
	}
	if tunnelID == "" {
		return fmt.Errorf("tunnel ID is required")
	}
	runtimeKey, err := s.Prompter.Secret("Tunnel runtime key (CONTROL_PLANE_API_KEY)")
	if err != nil {
		return err
	}
	if runtimeKey == "" {
		return fmt.Errorf("runtime key is required")
	}
	if err := s.Store.SetCredential("openai-tunnel", "runtime-key", runtimeKey); err != nil {
		return err
	}
	st := State{
		Method:   "subscription",
		Provider: "chatgpt-subscription",
		TunnelID: tunnelID,
	}
	if err := s.Store.SaveState(st); err != nil {
		return err
	}

	// Configure tunnel-client if available.
	if path, err := exec.LookPath(s.Cfg.TunnelClientPath); err == nil {
		mcpURL := "http://127.0.0.1:7338/mcp"
		if v := os.Getenv("MEETINGCTL_MCP_LISTEN"); v != "" {
			mcpURL = "http://" + v + "/mcp"
		}
		cmd := exec.CommandContext(ctx, path, "init",
			"--profile", s.Cfg.TunnelProfile,
			"--tunnel-id", tunnelID,
			"--mcp-server-url", mcpURL,
		)
		cmd.Env = append(os.Environ(), "CONTROL_PLANE_API_KEY="+runtimeKey)
		cmd.Stdout = s.Out
		cmd.Stderr = s.Out
		fmt.Fprintln(s.Out, "Configuring tunnel-client...")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(s.Out, "warn: tunnel-client init: %v\n", err)
		} else {
			doc := exec.CommandContext(ctx, path, "doctor", "--profile", s.Cfg.TunnelProfile, "--explain")
			doc.Env = cmd.Env
			doc.Stdout = s.Out
			doc.Stderr = s.Out
			_ = doc.Run()
		}
		fmt.Fprintf(s.Out, "Start tunnel with:\n  CONTROL_PLANE_API_KEY=*** %s run --profile %s\n", path, s.Cfg.TunnelProfile)
	} else {
		fmt.Fprintln(s.Out, "tunnel-client not found in PATH.")
		fmt.Fprintln(s.Out, "Install from https://github.com/openai/tunnel-client/releases")
	}

	fmt.Fprintln(s.Out)
	fmt.Fprintln(s.Out, "Opening ChatGPT plugin settings...")
	_ = openBrowser("https://chatgpt.com/plugins")
	fmt.Fprintln(s.Out, "In ChatGPT: create a developer-mode app → Connection = Tunnel → select your tunnel.")
	fmt.Fprintln(s.Out, "Note: subscription mode does not fund OpenAI API STT/analysis.")
	fmt.Fprintln(s.Out, "Configure local STT (whispercpp/command) if you have no API key.")
	return nil
}

// StatusText prints auth status without secrets.
func (s *Service) StatusText() (string, error) {
	st, err := s.Store.LoadState()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "method: %s\n", st.Method)
	fmt.Fprintf(&b, "provider: %s\n", orDash(st.Provider))
	if len(st.Usage) > 0 {
		fmt.Fprintf(&b, "usage: %s\n", strings.Join(st.Usage, ", "))
	}
	if st.TunnelID != "" {
		fmt.Fprintf(&b, "tunnel_id: %s\n", st.TunnelID)
	}
	switch st.Method {
	case "api_key":
		if s.Store.HasCredential("openai", "api-key") {
			fmt.Fprintln(&b, "api_key: configured")
		} else {
			fmt.Fprintln(&b, "api_key: missing")
		}
	case "subscription":
		if s.Store.HasCredential("openai-tunnel", "runtime-key") {
			fmt.Fprintln(&b, "tunnel_runtime_key: configured")
		} else {
			fmt.Fprintln(&b, "tunnel_runtime_key: missing")
		}
	}
	return b.String(), nil
}

// Logout clears credentials and state.
func (s *Service) Logout(ctx context.Context) error {
	if err := s.Store.Clear(); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.Cfg.DataDir, "auth.env"))
	if err := s.reloadDaemon(ctx); err != nil {
		fmt.Fprintf(s.Out, "warn: daemon reload: %v\n", err)
	}
	fmt.Fprintln(s.Out, "Logged out. Credentials removed.")
	return nil
}

// ApplyToEnv sets process env from stored credentials (for daemon reload).
func ApplyToEnv(store *Store) error {
	st, err := store.LoadState()
	if err != nil {
		return err
	}
	clearAuthEnv := func() {
		_ = os.Unsetenv("OPENAI_API_KEY")
		_ = os.Unsetenv("CONTROL_PLANE_API_KEY")
		_ = os.Unsetenv("MEETINGCTL_TRANSCRIPTION_PROVIDER")
		_ = os.Unsetenv("MEETINGCTL_ANALYSIS_PROVIDER")
	}
	switch st.Method {
	case "api_key":
		key, err := store.GetCredential("openai", "api-key")
		if err != nil {
			clearAuthEnv()
			return err
		}
		_ = os.Unsetenv("CONTROL_PLANE_API_KEY")
		_ = os.Setenv("OPENAI_API_KEY", key)
		useT, useA := false, false
		for _, u := range st.Usage {
			if u == "transcription" {
				useT = true
			}
			if u == "analysis" {
				useA = true
			}
		}
		if len(st.Usage) == 0 {
			useT, useA = true, true
		}
		if useT {
			_ = os.Setenv("MEETINGCTL_TRANSCRIPTION_PROVIDER", "openai")
		} else {
			_ = os.Setenv("MEETINGCTL_TRANSCRIPTION_PROVIDER", "none")
		}
		if useA {
			_ = os.Setenv("MEETINGCTL_ANALYSIS_PROVIDER", "openai")
		} else {
			_ = os.Setenv("MEETINGCTL_ANALYSIS_PROVIDER", "none")
		}
	case "subscription":
		_ = os.Unsetenv("OPENAI_API_KEY")
		// Do not set OPENAI_API_KEY from subscription.
		key, err := store.GetCredential("openai-tunnel", "runtime-key")
		if err != nil {
			clearAuthEnv()
			return err
		}
		_ = os.Setenv("CONTROL_PLANE_API_KEY", key)
	case "none", "":
		clearAuthEnv()
	}
	return nil
}

func (s *Service) writeEnvHint(key string, usage []string) error {
	// Restricted env file the daemon can source via ApplyToEnv; also useful for docs.
	// We do NOT write the raw key into a shell-export file by default for safety.
	// Key stays in credential store only.
	_ = key
	path := filepath.Join(s.Cfg.DataDir, "auth.env")
	var b strings.Builder
	b.WriteString("# Generated by meetingctl auth — secrets are in auth/creds, not here.\n")
	b.WriteString("MEETINGCTL_AUTH_METHOD=api_key\n")
	b.WriteString("MEETINGCTL_AUTH_PROVIDER=openai\n")
	if contains(usage, "transcription") {
		b.WriteString("MEETINGCTL_TRANSCRIPTION_PROVIDER=openai\n")
	}
	if contains(usage, "analysis") {
		b.WriteString("MEETINGCTL_ANALYSIS_PROVIDER=openai\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func (s *Service) reloadDaemon(ctx context.Context) error {
	base := s.Cfg.BaseURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/auth/reload", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Cfg.ControlToken)
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}

func (s *Service) checkDaemon(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Cfg.BaseURL()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}

func (s *Service) cacheDir() string {
	return filepath.Join(s.Cfg.DataDir, "cache")
}

func (s *Service) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func validateOpenAIKey(ctx context.Context, httpc *http.Client, baseURL, key string) error {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("invalid API key")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("openai returned %s", resp.Status)
	}
	return nil
}

func usageFromID(id string) []string {
	switch id {
	case "transcription":
		return []string{"transcription"}
	case "analysis":
		return []string{"analysis"}
	default:
		return []string{"transcription", "analysis"}
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
