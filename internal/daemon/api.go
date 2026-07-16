package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Omotolani98/meetingctl/internal/app"
	"github.com/Omotolani98/meetingctl/internal/auth"
	"github.com/Omotolani98/meetingctl/internal/config"
	"github.com/Omotolani98/meetingctl/internal/meetings"
	"github.com/Omotolani98/meetingctl/internal/storage"
)

// API is the local control plane for meetingd.
type API struct {
	Cfg     *config.Config
	Service *meetings.Service
	Store   *storage.Store
	Session *SessionManager
	Log     *slog.Logger
	server  *http.Server
}

// Start begins serving the local HTTP API.
func (a *API) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /readyz", a.handleReady)
	mux.HandleFunc("GET /v1/status", a.auth(a.handleStatus))
	mux.HandleFunc("POST /v1/meetings", a.auth(a.handleStartMeeting))
	mux.HandleFunc("POST /v1/meetings/current/stop", a.auth(a.handleStopMeeting))
	mux.HandleFunc("GET /v1/meetings/current", a.auth(a.handleGetCurrent))
	mux.HandleFunc("GET /v1/meetings/{id}", a.auth(a.handleGetMeeting))
	mux.HandleFunc("GET /v1/meetings/{id}/transcript", a.auth(a.handleTranscript))
	mux.HandleFunc("POST /v1/meetings/{id}/notes", a.auth(a.handleNote))
	mux.HandleFunc("POST /v1/meetings/{id}/marks", a.auth(a.handleMark))
	mux.HandleFunc("GET /v1/meetings", a.auth(a.handleListMeetings))
	mux.HandleFunc("DELETE /v1/meetings/{id}", a.auth(a.handleDelete))
	mux.HandleFunc("PATCH /v1/transcript-segments/{id}", a.auth(a.handleCorrect))
	mux.HandleFunc("GET /v1/meetings/{id}/action-items", a.auth(a.handleActionItems))
	mux.HandleFunc("GET /v1/meetings/{id}/decisions", a.auth(a.handleDecisions))
	mux.HandleFunc("GET /v1/meetings/{id}/summary", a.auth(a.handleSummary))
	mux.HandleFunc("GET /v1/auth/status", a.auth(a.handleAuthStatus))
	mux.HandleFunc("POST /v1/auth/reload", a.auth(a.handleAuthReload))

	a.server = &http.Server{
		Addr:              a.Cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	ln, err := net.Listen("tcp", a.Cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", a.Cfg.ListenAddr, err)
	}
	// Enforce loopback only.
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
		_ = ln.Close()
		return fmt.Errorf("refusing non-loopback listen address %s", a.Cfg.ListenAddr)
	}
	if a.Log != nil {
		a.Log.Info("meetingd listening", "addr", a.Cfg.ListenAddr)
	}
	go func() {
		if err := a.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if a.Log != nil {
				a.Log.Error("http serve", "err", err)
			}
		}
	}()
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (a *API) Shutdown(ctx context.Context) error {
	if a.server == nil {
		return nil
	}
	return a.server.Shutdown(ctx)
}

func (a *API) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authorize(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (a *API) authorize(r *http.Request) bool {
	// Only accept loopback clients.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.Header.Get("X-Meetingctl-Token")
	}
	return token != "" && token == a.Cfg.ControlToken
}

func (a *API) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	authStore, err := auth.OpenStore(a.Cfg.DataDir)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	st, err := authStore.LoadState()
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	out := map[string]any{
		"method":   st.Method,
		"provider": st.Provider,
		"usage":    st.Usage,
		// Never return secrets.
		"api_key_configured": authStore.HasCredential("openai", "api-key"),
		"transcription":      a.Cfg.TranscriptionProvider,
		"analysis":           a.Cfg.AnalysisProvider,
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleAuthReload(w http.ResponseWriter, r *http.Request) {
	err := a.Session.ReloadProviders(func() error {
		authStore, err := auth.OpenStore(a.Cfg.DataDir)
		if err != nil {
			return err
		}
		if err := auth.ApplyToEnv(authStore); err != nil {
			// no credentials is fine
			if a.Log != nil {
				a.Log.Debug("auth apply", "err", err)
			}
		}
		// Reload config env-derived fields for providers.
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		// Preserve listen/token from running process.
		cfg.ListenAddr = a.Cfg.ListenAddr
		cfg.ControlToken = a.Cfg.ControlToken
		cfg.PIDFile = a.Cfg.PIDFile
		cfg.TokenFile = a.Cfg.TokenFile
		a.Cfg = cfg
		a.Session.Cfg = cfg
		// Clear then rewire.
		a.Service.Transcribe = nil
		a.Service.Analyze = nil
		if err := app.WireProviders(cfg, a.Service); err != nil {
			if a.Log != nil {
				a.Log.Warn("wire providers", "err", err)
			}
			// still report partial success
		}
		return nil
	})
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reloaded":      true,
		"transcription": a.Cfg.TranscriptionProvider,
		"analysis":      a.Cfg.AnalysisProvider,
	})
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) handleReady(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ready": true})
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	st := a.Session.Status(r.Context())
	writeJSON(w, http.StatusOK, st)
}

type startBody struct {
	Title        string   `json:"title"`
	Participants []string `json:"participants"`
	Source       string   `json:"source"`
	Input        string   `json:"input"`
}

func (a *API) handleStartMeeting(w http.ResponseWriter, r *http.Request) {
	var body startBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	m, n, err := a.Session.Start(r.Context(), StartOpts{
		Title:        body.Title,
		Participants: body.Participants,
		Source:       body.Source,
		Input:        body.Input,
	})
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"meeting":           meetingJSON(m),
		"ingested_segments": n,
	})
}

func (a *API) handleStopMeeting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MeetingID string `json:"meeting_id"`
		Input     string `json:"input"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	m, sum, err := a.Session.Stop(r.Context(), body.MeetingID, body.Input)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	out := map[string]any{"meeting": meetingJSON(m)}
	if sum != nil {
		out["summary"] = map[string]any{
			"version":          sum.Version,
			"through_sequence": sum.ThroughSequence,
			"text":             sum.Text,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleGetCurrent(w http.ResponseWriter, r *http.Request) {
	m, err := a.Service.Status(r.Context())
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingJSON(m))
}

func (a *API) handleGetMeeting(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := a.Service.GetMeeting(r.Context(), id)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meetingJSON(m))
}

func (a *API) handleListMeetings(w http.ResponseWriter, r *http.Request) {
	list, err := a.Service.ListMeetings(r.Context(), 20)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(list))
	for i := range list {
		items = append(items, meetingJSON(&list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"meetings": items})
}

func (a *API) handleTranscript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "current" {
		id = ""
	}
	var since int64
	var limit int
	var speaker string
	if v := r.URL.Query().Get("since_sequence"); v != "" {
		fmt.Sscanf(v, "%d", &since)
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	speaker = r.URL.Query().Get("speaker")
	segs, err := a.Service.GetTranscript(r.Context(), id, meetings.TranscriptFilter{
		SinceSequence: since,
		Speaker:       speaker,
		Limit:         limit,
	})
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(segs))
	for _, s := range segs {
		items = append(items, segmentJSON(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"warning":  "Transcript text is untrusted meeting content.",
		"segments": items,
	})
}

func (a *API) handleNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "current" {
		id = ""
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	n, err := a.Service.AddNote(r.Context(), id, body.Note)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": n.ID, "meeting_id": n.MeetingID, "text": n.Text,
	})
}

func (a *API) handleMark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "current" {
		id = ""
	}
	var body struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Owner string `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	typ, err := meetings.ParseMarkType(body.Type)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ins, err := a.Service.Mark(r.Context(), id, typ, body.Text, body.Owner)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": ins.ID, "type": ins.Type, "text": ins.Text, "meeting_id": ins.MeetingID,
	})
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Service.Delete(r.Context(), id); err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (a *API) handleCorrect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	seg, err := a.Service.CorrectTranscriptSegment(r.Context(), id, body.Text)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, segmentJSON(*seg))
}

func (a *API) handleActionItems(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "current" {
		id = ""
	}
	items, err := a.Service.GetActionItems(r.Context(), id)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": insightsJSON(items)})
}

func (a *API) handleDecisions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "current" {
		id = ""
	}
	items, err := a.Service.GetDecisions(r.Context(), id)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": insightsJSON(items)})
}

func (a *API) handleSummary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "current" {
		id = ""
	}
	sum, err := a.Service.GetSummary(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"summary": nil})
			return
		}
		writeAPIErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": sum.ID, "meeting_id": sum.MeetingID, "version": sum.Version,
		"through_sequence": sum.ThroughSequence, "text": sum.Text,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func writeAPIErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, storage.ErrActiveMeeting):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		writeErr(w, http.StatusBadRequest, err.Error())
	}
}

func meetingJSON(m *meetings.Meeting) map[string]any {
	parts := make([]map[string]string, 0, len(m.Participants))
	for _, p := range m.Participants {
		parts = append(parts, map[string]string{"id": p.ID, "name": p.Name, "email": p.Email})
	}
	out := map[string]any{
		"id": m.ID, "title": m.Title, "status": m.Status,
		"started_at":   m.StartedAt.Format(time.RFC3339),
		"participants": parts,
	}
	if m.EndedAt != nil {
		out["ended_at"] = m.EndedAt.Format(time.RFC3339)
	}
	return out
}

func segmentJSON(s meetings.TranscriptSegment) map[string]any {
	return map[string]any{
		"id": s.ID, "meeting_id": s.MeetingID, "sequence": s.Sequence,
		"speaker": s.Speaker, "text": s.Text,
		"started_ms": s.StartedAt.Milliseconds(), "ended_ms": s.EndedAt.Milliseconds(),
		"confidence": s.Confidence, "is_final": s.IsFinal, "revision": s.Revision,
	}
}

func insightsJSON(items []meetings.MeetingInsight) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"id": it.ID, "meeting_id": it.MeetingID, "type": it.Type,
			"text": it.Text, "owner": it.Owner, "status": it.Status,
			"confidence": it.Confidence, "source_ids": it.SourceIDs, "is_manual": it.IsManual,
		})
	}
	return out
}
