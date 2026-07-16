package meetings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Omotolani98/meetingctl/internal/audio"
	"github.com/Omotolani98/meetingctl/internal/transcription"
)

// Repository is the persistence surface the service needs.
type Repository interface {
	CreateMeeting(ctx context.Context, title string, participantNames []string) (*Meeting, error)
	GetActiveMeeting(ctx context.Context) (*Meeting, error)
	GetMeeting(ctx context.Context, id string) (*Meeting, error)
	ListMeetings(ctx context.Context, limit int) ([]Meeting, error)
	SetMeetingStatus(ctx context.Context, id string, status MeetingStatus, endedAt *time.Time) error
	DeleteMeeting(ctx context.Context, id string) error

	InsertFinalSegment(ctx context.Context, meetingID string, in TranscriptSegment) (*TranscriptSegment, error)
	GetSegment(ctx context.Context, id string) (*TranscriptSegment, error)
	ListTranscript(ctx context.Context, meetingID string, f TranscriptFilter) ([]TranscriptSegment, error)
	CorrectSegment(ctx context.Context, segmentID, newText string) (*TranscriptSegment, error)

	InsertInsight(ctx context.Context, in MeetingInsight) (*MeetingInsight, error)
	ListInsights(ctx context.Context, meetingID string, typ *InsightType) ([]MeetingInsight, error)
	UpsertSummary(ctx context.Context, meetingID, text string, throughSeq int64) (*Summary, error)
	GetLatestSummary(ctx context.Context, meetingID string) (*Summary, error)
	InsertNote(ctx context.Context, meetingID, text string) (*ManualNote, error)
	ListNotes(ctx context.Context, meetingID string) ([]ManualNote, error)
}

// Service orchestrates capture, transcription, persistence, and analysis.
type Service struct {
	Repo       Repository
	Transcribe transcription.Transcriber
	Analyze    Analyzer
	Log        *slog.Logger
}

// StartRequest configures a new meeting.
type StartRequest struct {
	Title        string
	Participants []string
	// Source is optional; when set, RunIngest processes it to completion.
	Source audio.Source
}

// Start creates an active meeting. Only one active meeting is allowed.
func (s *Service) Start(ctx context.Context, req StartRequest) (*Meeting, error) {
	return s.Repo.CreateMeeting(ctx, req.Title, req.Participants)
}

// Status returns the active meeting or an error if none.
func (s *Service) Status(ctx context.Context) (*Meeting, error) {
	return s.Repo.GetActiveMeeting(ctx)
}

// IngestSource runs the audio→transcript pipeline until the source ends.
// Only finalized updates are persisted.
func (s *Service) IngestSource(ctx context.Context, meetingID string, src audio.Source) (int, error) {
	if s.Transcribe == nil {
		return 0, fmt.Errorf("no transcriber configured")
	}
	if src == nil {
		return 0, fmt.Errorf("no audio source")
	}
	chunks, errs := src.Chunks(ctx)
	persisted := 0
	for chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return persisted, err
		}
		chunk.MeetingID = meetingID
		updates, err := s.Transcribe.Transcribe(ctx, chunk)
		if err != nil {
			return persisted, fmt.Errorf("transcribe chunk %d: %w", chunk.Index, err)
		}
		for _, u := range updates {
			if !u.IsFinal {
				if s.Log != nil {
					s.Log.Debug("skipping partial transcript", "text", u.Text)
				}
				continue
			}
			seg := TranscriptSegment{
				MeetingID:   meetingID,
				Speaker:     u.Speaker,
				Text:        u.Text,
				StartedAt:   u.StartedAt,
				EndedAt:     u.EndedAt,
				Confidence:  u.Confidence,
				IsFinal:     true,
				Idempotency: u.Idempotency,
			}
			if _, err := s.Repo.InsertFinalSegment(ctx, meetingID, seg); err != nil {
				return persisted, err
			}
			persisted++
		}
	}
	select {
	case err, ok := <-errs:
		if ok && err != nil {
			return persisted, err
		}
	default:
	}
	return persisted, nil
}

// AddNote attaches a manual note to a meeting (active by default).
func (s *Service) AddNote(ctx context.Context, meetingID, note string) (*ManualNote, error) {
	id, err := s.resolveMeetingID(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	return s.Repo.InsertNote(ctx, id, note)
}

// Mark creates a manual insight on the meeting.
func (s *Service) Mark(ctx context.Context, meetingID string, typ InsightType, text, owner string) (*MeetingInsight, error) {
	id, err := s.resolveMeetingID(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	if text == "" {
		text = string(typ)
	}
	return s.Repo.InsertInsight(ctx, MeetingInsight{
		MeetingID: id,
		Type:      typ,
		Text:      text,
		Owner:     owner,
		Status:    "open",
		IsManual:  true,
	})
}

// Stop finalizes the active (or specified) meeting: analysis + status completed.
func (s *Service) Stop(ctx context.Context, meetingID string) (*Meeting, *Summary, error) {
	id, err := s.resolveMeetingID(ctx, meetingID)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.Repo.GetMeeting(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if m.Status == StatusCompleted {
		sum, _ := s.Repo.GetLatestSummary(ctx, id)
		return m, sum, nil
	}
	if m.Status != StatusActive && m.Status != StatusFailed {
		return nil, nil, fmt.Errorf("meeting %s is %s, cannot stop", id, m.Status)
	}

	if err := s.Repo.SetMeetingStatus(ctx, id, StatusFinalizing, nil); err != nil {
		return nil, nil, err
	}

	sum, err := s.finalize(ctx, id)
	if err != nil {
		_ = s.Repo.SetMeetingStatus(ctx, id, StatusFailed, nil)
		return nil, nil, err
	}
	now := time.Now().UTC()
	if err := s.Repo.SetMeetingStatus(ctx, id, StatusCompleted, &now); err != nil {
		return nil, nil, err
	}
	m, err = s.Repo.GetMeeting(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return m, sum, nil
}

// FinalizeMeeting runs analysis and marks completed (MCP tool entrypoint).
func (s *Service) FinalizeMeeting(ctx context.Context, meetingID string) (*Meeting, *Summary, error) {
	return s.Stop(ctx, meetingID)
}

func (s *Service) finalize(ctx context.Context, meetingID string) (*Summary, error) {
	m, err := s.Repo.GetMeeting(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	segs, err := s.Repo.ListTranscript(ctx, meetingID, TranscriptFilter{Limit: MaxTranscriptLimit})
	if err != nil {
		return nil, err
	}
	// Paginate if needed
	if len(segs) == MaxTranscriptLimit {
		var all []TranscriptSegment
		var since int64
		for {
			page, err := s.Repo.ListTranscript(ctx, meetingID, TranscriptFilter{SinceSequence: since, Limit: MaxTranscriptLimit})
			if err != nil {
				return nil, err
			}
			if len(page) == 0 {
				break
			}
			all = append(all, page...)
			since = page[len(page)-1].Sequence
			if len(page) < MaxTranscriptLimit {
				break
			}
		}
		segs = all
	}
	notes, err := s.Repo.ListNotes(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	var prior string
	if ps, err := s.Repo.GetLatestSummary(ctx, meetingID); err == nil {
		prior = ps.Text
	}

	var result AnalysisResult
	if s.Analyze != nil {
		result, err = s.Analyze.Analyze(ctx, AnalysisInput{
			Meeting:      *m,
			Segments:     segs,
			Notes:        notes,
			PriorSummary: prior,
		})
		if err != nil {
			return nil, fmt.Errorf("analyze: %w", err)
		}
	} else {
		result.Summary = fmt.Sprintf("Meeting %q ended with %d transcript segments.", m.Title, len(segs))
	}

	var through int64
	if len(segs) > 0 {
		through = segs[len(segs)-1].Sequence
	}
	sum, err := s.Repo.UpsertSummary(ctx, meetingID, result.Summary, through)
	if err != nil {
		return nil, err
	}
	for _, ins := range result.Insights {
		ins.MeetingID = meetingID
		if _, err := s.Repo.InsertInsight(ctx, ins); err != nil {
			return nil, fmt.Errorf("store insight: %w", err)
		}
	}
	return sum, nil
}

// GetTranscript is a thin pass-through with limit normalization.
func (s *Service) GetTranscript(ctx context.Context, meetingID string, f TranscriptFilter) ([]TranscriptSegment, error) {
	id, err := s.resolveMeetingID(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	f.Limit = NormalizeLimit(f.Limit)
	return s.Repo.ListTranscript(ctx, id, f)
}

// GetActionItems returns action_item insights.
func (s *Service) GetActionItems(ctx context.Context, meetingID string) ([]MeetingInsight, error) {
	id, err := s.resolveMeetingID(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	t := InsightActionItem
	return s.Repo.ListInsights(ctx, id, &t)
}

// GetDecisions returns decision insights.
func (s *Service) GetDecisions(ctx context.Context, meetingID string) ([]MeetingInsight, error) {
	id, err := s.resolveMeetingID(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	t := InsightDecision
	return s.Repo.ListInsights(ctx, id, &t)
}

// CorrectTranscriptSegment revises a segment's text.
func (s *Service) CorrectTranscriptSegment(ctx context.Context, segmentID, text string) (*TranscriptSegment, error) {
	return s.Repo.CorrectSegment(ctx, segmentID, text)
}

// Delete removes a meeting permanently.
func (s *Service) Delete(ctx context.Context, meetingID string) error {
	return s.Repo.DeleteMeeting(ctx, meetingID)
}

// ListMeetings lists recent meetings.
func (s *Service) ListMeetings(ctx context.Context, limit int) ([]Meeting, error) {
	return s.Repo.ListMeetings(ctx, limit)
}

// GetMeeting loads a meeting by ID.
func (s *Service) GetMeeting(ctx context.Context, id string) (*Meeting, error) {
	return s.Repo.GetMeeting(ctx, id)
}

// GetSummary returns the latest summary.
func (s *Service) GetSummary(ctx context.Context, meetingID string) (*Summary, error) {
	id, err := s.resolveMeetingID(ctx, meetingID)
	if err != nil {
		return nil, err
	}
	return s.Repo.GetLatestSummary(ctx, id)
}

func (s *Service) resolveMeetingID(ctx context.Context, meetingID string) (string, error) {
	if meetingID != "" && meetingID != "current" {
		return meetingID, nil
	}
	m, err := s.Repo.GetActiveMeeting(ctx)
	if err != nil {
		return "", err
	}
	return m.ID, nil
}

// IsNotFound reports whether err indicates a missing entity.
func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, errNotFound) || containsNotFound(err))
}

// Wired by storage package via errors.Is — we re-export a helper that
// storage tests and CLI use. The storage.ErrNotFound is the canonical error.
var errNotFound = errors.New("not found")

func containsNotFound(err error) bool {
	return err != nil && (err.Error() == "not found" || errors.Is(err, errNotFound))
}
