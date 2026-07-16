package cli

import "testing"

func TestDefaultMCPURL(t *testing.T) {
	t.Setenv("MEETINGCTL_MCP_LISTEN", "")
	t.Setenv("MEETINGCTL_LISTEN", "127.0.0.1:7337")
	if got := defaultMCPURL(); got != "http://127.0.0.1:7338/mcp" {
		t.Fatalf("got %q", got)
	}

	t.Setenv("MEETINGCTL_MCP_LISTEN", "127.0.0.1:9000")
	if got := defaultMCPURL(); got != "http://127.0.0.1:9000/mcp" {
		t.Fatalf("got %q", got)
	}
}

func TestControlTokenPath(t *testing.T) {
	t.Setenv("MEETINGCTL_TOKEN_FILE", "")
	t.Setenv("MEETINGCTL_DATA_DIR", "/tmp/meetingctl-data")
	if got := controlTokenPath(); got != "/tmp/meetingctl-data/control.token" {
		t.Fatalf("got %q", got)
	}

	t.Setenv("MEETINGCTL_TOKEN_FILE", "/tmp/custom.token")
	if got := controlTokenPath(); got != "/tmp/custom.token" {
		t.Fatalf("got %q", got)
	}
}
