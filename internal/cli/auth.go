package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Omotolani98/meetingctl/internal/auth"
	"github.com/Omotolani98/meetingctl/internal/catalog"
	"github.com/Omotolani98/meetingctl/internal/config"
	"github.com/spf13/cobra"
)

func newAuthCmd() *cobra.Command {
	var (
		method   string
		provider string
		keyStdin bool
		usage    string
	)
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with AI providers or ChatGPT Subscription",
		Long: `Interactive authentication for meetingctl.

Methods:
  API Key               — store a provider key (OpenAI) for STT/analysis
  ChatGPT Subscription  — connect MCP to ChatGPT via Secure Tunnel

Browse providers from models.dev; only supported providers accept credentials.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newAuthService(cmd)
			if err != nil {
				return err
			}
			// Non-interactive API key path
			if method == "api-key" || method == "api_key" {
				return runNonInteractiveAPIKey(cmd.Context(), svc, provider, usage, keyStdin)
			}
			if method == "subscription" {
				return svc.RunInteractive(cmd.Context()) // still interactive for tunnel fields
			}
			return svc.RunInteractive(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&method, "method", "", "api-key | subscription (optional; interactive if empty)")
	cmd.Flags().StringVar(&provider, "provider", "openai", "provider id for api-key method")
	cmd.Flags().BoolVar(&keyStdin, "key-stdin", false, "read API key from stdin")
	cmd.Flags().StringVar(&usage, "usage", "both", "both | transcription | analysis")

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show authentication status (no secrets)",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newAuthService(cmd)
			if err != nil {
				return err
			}
			text, err := svc.StatusText()
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), text)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newAuthService(cmd)
			if err != nil {
				return err
			}
			return svc.Logout(cmd.Context())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "refresh-providers",
		Short: "Refresh models.dev provider catalog cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// Force refresh by deleting cache then loading.
			cache := cfg.DataDir + string(os.PathSeparator) + "cache" + string(os.PathSeparator) + "models-dev.json"
			_ = os.Remove(cache)
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			cat, err := catalog.Load(ctx, cfg.DataDir+string(os.PathSeparator)+"cache")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "source: %s\n", cat.Source)
			fmt.Fprintf(cmd.OutOrStdout(), "supported: %d\n", len(cat.Supported))
			fmt.Fprintf(cmd.OutOrStdout(), "browse: %d\n", len(cat.Browse))
			fmt.Fprintf(cmd.OutOrStdout(), "fetched_at: %s\n", cat.FetchedAt.Format(time.RFC3339))
			return nil
		},
	})
	return cmd
}

func newAuthService(cmd *cobra.Command) (*auth.Service, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	store, err := auth.OpenStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	return &auth.Service{
		Store: store,
		Cfg:   cfg,
		Prompter: &auth.TerminalPrompter{
			In:  cmd.InOrStdin(),
			Out: cmd.OutOrStdout(),
			FD:  int(os.Stdin.Fd()),
		},
		Out: cmd.OutOrStdout(),
	}, nil
}

func runNonInteractiveAPIKey(ctx context.Context, svc *auth.Service, provider, usage string, keyStdin bool) error {
	if provider != "openai" {
		return fmt.Errorf("non-interactive api-key currently supports --provider openai only")
	}
	if !keyStdin {
		return fmt.Errorf("pass --key-stdin and pipe the API key (never use flags for secrets)")
	}
	key, err := svc.Prompter.Secret("API key")
	if err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("empty API key")
	}
	if err := svc.Store.SetCredential("openai", "api-key", key); err != nil {
		return err
	}
	usageList := []string{"transcription", "analysis"}
	switch usage {
	case "transcription":
		usageList = []string{"transcription"}
	case "analysis":
		usageList = []string{"analysis"}
	}
	if err := svc.Store.SaveState(auth.State{
		Method: "api_key", Provider: "openai", Usage: usageList,
	}); err != nil {
		return err
	}
	fmt.Fprintln(svc.Out, "Credentials saved.")
	if err := reloadDaemonSimple(ctx, svc); err != nil {
		fmt.Fprintf(svc.Out, "warn: daemon reload: %v\n", err)
	} else {
		fmt.Fprintln(svc.Out, "meetingd providers reloaded.")
	}
	return nil
}

func reloadDaemonSimple(ctx context.Context, svc *auth.Service) error {
	cfg := svc.Cfg
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL()+"/v1/auth/reload", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ControlToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}
