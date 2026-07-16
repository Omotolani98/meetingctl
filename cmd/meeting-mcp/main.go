package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Omotolani98/meetingctl/internal/app"
	mcpserver "github.com/Omotolani98/meetingctl/internal/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// meeting-mcp is a stdio MCP adapter for local clients.
func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Prefer reading through the same DB as meetingd (shared data dir).
	// For write tools, coordinate by running only one mutator, or prefer meetingd HTTP MCP.
	a, err := app.Open(ctx, app.Options{Logger: log, SkipProviders: true})
	if err != nil {
		log.Error("open app", "err", err)
		os.Exit(1)
	}
	defer a.Close()

	server := mcpserver.NewMCPServer(a.Service, a.Store)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Error("mcp server stopped", "err", err)
		os.Exit(1)
	}
}
