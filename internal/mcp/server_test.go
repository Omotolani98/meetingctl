package mcp_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Omotolani98/meetingctl/internal/audio"
	"github.com/Omotolani98/meetingctl/internal/crypto"
	"github.com/Omotolani98/meetingctl/internal/insights"
	mcpserver "github.com/Omotolani98/meetingctl/internal/mcp"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/Omotolani98/meetingctl/internal/storage"
	"github.com/Omotolani98/meetingctl/internal/transcription"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPTools(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 9)
	}
	box, err := crypto.NewBox("test", key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "mcp.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	fixture := filepath.Join("..", "..", "testdata", "platform-review")
	tr, err := transcription.LoadFixtureTranscriber(fixture)
	if err != nil {
		t.Fatal(err)
	}
	an, err := insights.LoadFixtureAnalyzer(fixture)
	if err != nil {
		t.Fatal(err)
	}
	svc := &meetings.Service{Repo: store, Transcribe: tr, Analyze: an}

	m, err := svc.Start(ctx, meetings.StartRequest{Title: "MCP Test", Participants: []string{"A"}})
	if err != nil {
		t.Fatal(err)
	}
	src := &audio.FixtureSource{MeetingID: m.ID, Count: len(tr.Updates)}
	if _, err := svc.IngestSource(ctx, m.ID, src); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Stop(ctx, m.ID); err != nil {
		t.Fatal(err)
	}

	server := mcpserver.NewMCPServer(svc, store)
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	// Meeting is completed so get_active_meeting should fail / not found.
	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_meeting",
		Arguments: map[string]any{"meeting_id": m.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("get_meeting error: %+v", res.Content)
	}

	tres, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_transcript",
		Arguments: map[string]any{
			"meeting_id":     m.ID,
			"since_sequence": 0,
			"limit":          10,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tres.IsError {
		t.Fatalf("get_transcript error: %+v", tres.Content)
	}

	ares, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_action_items",
		Arguments: map[string]any{"meeting_id": m.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ares.IsError {
		t.Fatalf("get_action_items error: %+v", ares.Content)
	}

	// List tools exists
	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 5 {
		t.Fatalf("tools %d", len(tools.Tools))
	}
}
