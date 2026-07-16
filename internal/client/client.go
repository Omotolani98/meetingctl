package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client talks to the local meetingd control API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Load creates a client from environment defaults.
func Load() (*Client, error) {
	base := strings.TrimSpace(os.Getenv("MEETINGCTL_URL"))
	if base == "" {
		listen := os.Getenv("MEETINGCTL_LISTEN")
		if listen == "" {
			listen = "127.0.0.1:7337"
		}
		base = "http://" + listen
	}
	token := strings.TrimSpace(os.Getenv("MEETINGCTL_CONTROL_TOKEN"))
	if token == "" {
		dataDir := os.Getenv("MEETINGCTL_DATA_DIR")
		if dataDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			dataDir = filepath.Join(home, ".meetingctl")
		}
		b, err := os.ReadFile(filepath.Join(dataDir, "control.token"))
		if err == nil {
			token = strings.TrimSpace(string(b))
		}
	}
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Health checks /healthz.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("meetingd not reachable at %s: %w\nStart it with: meetingd", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("meetingd health status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("meetingd request failed: %w\nIs meetingd running? Try: meetingd", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var er struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &er)
		if er.Error != "" {
			return fmt.Errorf("%s", er.Error)
		}
		return fmt.Errorf("meetingd: %s", strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Status returns daemon status.
func (c *Client) Status(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/v1/status", nil, &out)
}

// StartMeeting starts a meeting.
func (c *Client) StartMeeting(ctx context.Context, title string, participants []string, source, input string) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodPost, "/v1/meetings", map[string]any{
		"title": title, "participants": participants, "source": source, "input": input,
	}, &out)
	return out, err
}

// StopMeeting stops the current/specified meeting.
func (c *Client) StopMeeting(ctx context.Context, meetingID, input string) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodPost, "/v1/meetings/current/stop", map[string]any{
		"meeting_id": meetingID, "input": input,
	}, &out)
	return out, err
}

// GetCurrent returns the active meeting.
func (c *Client) GetCurrent(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/v1/meetings/current", nil, &out)
}

// ListMeetings lists meetings.
func (c *Client) ListMeetings(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/v1/meetings", nil, &out)
}

// GetTranscript fetches transcript segments.
func (c *Client) GetTranscript(ctx context.Context, meetingID string, since int64, limit int, speaker string) (map[string]any, error) {
	if meetingID == "" {
		meetingID = "current"
	}
	path := fmt.Sprintf("/v1/meetings/%s/transcript?since_sequence=%d&limit=%d", meetingID, since, limit)
	if speaker != "" {
		path += "&speaker=" + speaker
	}
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// AddNote adds a note.
func (c *Client) AddNote(ctx context.Context, meetingID, note string) (map[string]any, error) {
	if meetingID == "" {
		meetingID = "current"
	}
	var out map[string]any
	return out, c.do(ctx, http.MethodPost, "/v1/meetings/"+meetingID+"/notes", map[string]any{"note": note}, &out)
}

// Mark adds a manual insight.
func (c *Client) Mark(ctx context.Context, meetingID, typ, text, owner string) (map[string]any, error) {
	if meetingID == "" {
		meetingID = "current"
	}
	var out map[string]any
	return out, c.do(ctx, http.MethodPost, "/v1/meetings/"+meetingID+"/marks", map[string]any{
		"type": typ, "text": text, "owner": owner,
	}, &out)
}

// DeleteMeeting deletes a meeting.
func (c *Client) DeleteMeeting(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/meetings/"+id, nil, nil)
}

// GetActionItems lists action items.
func (c *Client) GetActionItems(ctx context.Context, meetingID string) (map[string]any, error) {
	if meetingID == "" {
		meetingID = "current"
	}
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/v1/meetings/"+meetingID+"/action-items", nil, &out)
}

// GetDecisions lists decisions.
func (c *Client) GetDecisions(ctx context.Context, meetingID string) (map[string]any, error) {
	if meetingID == "" {
		meetingID = "current"
	}
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/v1/meetings/"+meetingID+"/decisions", nil, &out)
}

// GetSummary returns the latest summary.
func (c *Client) GetSummary(ctx context.Context, meetingID string) (map[string]any, error) {
	if meetingID == "" {
		meetingID = "current"
	}
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/v1/meetings/"+meetingID+"/summary", nil, &out)
}

// GetMeeting returns a meeting by id.
func (c *Client) GetMeeting(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/v1/meetings/"+id, nil, &out)
}

// CorrectSegment corrects transcript text.
func (c *Client) CorrectSegment(ctx context.Context, segmentID, text string) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodPatch, "/v1/transcript-segments/"+segmentID, map[string]any{"text": text}, &out)
}
