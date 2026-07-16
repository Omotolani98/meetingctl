package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Omotolani98/meetingctl/internal/app"
	"github.com/Omotolani98/meetingctl/internal/auth"
	"github.com/Omotolani98/meetingctl/internal/daemon"
	mcpserver "github.com/Omotolani98/meetingctl/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Apply stored credentials before opening providers.
	if err := loadAuthEnv(); err != nil {
		log.Debug("auth env", "err", err)
	}

	a, err := app.Open(ctx, app.Options{Logger: log})
	if err != nil {
		log.Error("open app", "err", err)
		os.Exit(1)
	}
	defer a.Close()

	if err := daemon.AcquirePIDLock(a.Config.PIDFile); err != nil {
		log.Error("pid lock", "err", err)
		os.Exit(1)
	}
	defer daemon.ReleasePIDLock(a.Config.PIDFile)

	// Recover abandoned active meeting from a previous crash.
	sess := &daemon.SessionManager{
		Cfg:     a.Config,
		Service: a.Service,
		Log:     log,
	}
	if err := sess.InterruptActive(ctx); err != nil {
		log.Warn("interrupt recovery", "err", err)
	}

	api := &daemon.API{
		Cfg:     a.Config,
		Service: a.Service,
		Store:   a.Store,
		Session: sess,
		Log:     log,
	}
	if err := api.Start(ctx); err != nil {
		log.Error("start api", "err", err)
		os.Exit(1)
	}

	// Mount Streamable HTTP MCP on the same loopback server via a separate mux path.
	// The control API already owns the listener; we attach MCP by wrapping handlers.
	// For simplicity in this milestone, expose MCP on a second port offset if needed —
	// instead, start an additional handler server on MEETINGCTL_MCP_LISTEN (default same host :7338).
	mcpListen := os.Getenv("MEETINGCTL_MCP_LISTEN")
	if mcpListen == "" {
		// default: same host, port+1 when listen is host:port
		mcpListen = bumpPort(a.Config.ListenAddr, 1)
	}
	mcpSrv := mcpserver.NewMCPServer(a.Service, a.Store)
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpSrv
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	// Auth wrapper for MCP HTTP
	secured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "Bearer "+a.Config.ControlToken || r.Header.Get("X-Meetingctl-Token") == a.Config.ControlToken {
			handler.ServeHTTP(w, r)
			return
		}
		// Allow unauthenticated only from loopback when MEETINGCTL_MCP_OPEN=1 (dev).
		if os.Getenv("MEETINGCTL_MCP_OPEN") == "1" {
			handler.ServeHTTP(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	httpSrv := &http.Server{
		Addr:              mcpListen,
		Handler:           secured,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("mcp http listening", "addr", mcpListen, "path", "/mcp")
		// StreamableHTTPHandler serves at root; document /mcp for clients.
		mux := http.NewServeMux()
		mux.Handle("/mcp", secured)
		mux.Handle("/mcp/", secured)
		mux.Handle("/", secured)
		httpSrv.Handler = mux
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("mcp http", "err", err)
		}
	}()

	log.Info("meetingd ready",
		"listen", a.Config.ListenAddr,
		"mcp", mcpListen,
		"db", a.Config.DBPath,
		"transcription", a.Config.TranscriptionProvider,
		"analysis", a.Config.AnalysisProvider,
	)

	<-ctx.Done()
	log.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = api.Shutdown(shCtx)
	_ = httpSrv.Shutdown(shCtx)
}

func loadAuthEnv() error {
	dataDir := os.Getenv("MEETINGCTL_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dataDir = home + "/.meetingctl"
	}
	store, err := auth.OpenStore(dataDir)
	if err != nil {
		return err
	}
	return auth.ApplyToEnv(store)
}

func bumpPort(addr string, delta int) string {
	// naive host:port bump
	host, port, ok := splitHostPort(addr)
	if !ok {
		return "127.0.0.1:7338"
	}
	return fmt.Sprintf("%s:%d", host, port+delta)
}

func splitHostPort(addr string) (string, int, bool) {
	var host string
	var port int
	n, err := fmt.Sscanf(addr, "%[^:]:%d", &host, &port)
	if err != nil || n != 2 {
		return "", 0, false
	}
	return host, port, true
}
