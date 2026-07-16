package meetings

import (
	"time"
)

// MeetingStatus is the lifecycle state of a meeting.
type MeetingStatus string

const (
	StatusActive     MeetingStatus = "active"
	StatusFinalizing MeetingStatus = "finalizing"
	StatusCompleted  MeetingStatus = "completed"
	StatusFailed     MeetingStatus = "failed"
)

// InsightType classifies extracted or manual meeting knowledge.
type InsightType string

const (
	InsightDecision   InsightType = "decision"
	InsightActionItem InsightType = "action_item"
	InsightQuestion   InsightType = "question"
	InsightRisk       InsightType = "risk"
	InsightBlocker    InsightType = "blocker"
	InsightNote       InsightType = "note"
)

// Meeting is a recorded conversation with structured memory.
type Meeting struct {
	ID           string
	Title        string
	Status       MeetingStatus
	StartedAt    time.Time
	EndedAt      *time.Time
	Participants []Participant
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Participant is someone present in a meeting.
type Participant struct {
	ID        string
	MeetingID string
	Name      string
	Email     string
}

// TranscriptSegment is a finalized unit of speech in a meeting.
type TranscriptSegment struct {
	ID           string
	MeetingID    string
	Sequence     int64
	Speaker      string
	Text         string
	StartedAt    time.Duration
	EndedAt      time.Duration
	Confidence   float64
	IsFinal      bool
	Idempotency  string
	Revision     int
	OriginalText string // set when corrected
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// MeetingInsight is structured knowledge derived from a meeting.
type MeetingInsight struct {
	ID          string
	MeetingID   string
	Type        InsightType
	Text        string
	Owner       string
	Status      string
	Confidence  float64
	SourceIDs   []string
	IsManual    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Summary is a rolling or final meeting summary.
type Summary struct {
	ID              string
	MeetingID       string
	Text            string
	Version         int
	ThroughSequence int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ManualNote is user-supplied context not present in the transcript.
type ManualNote struct {
	ID        string
	MeetingID string
	Text      string
	CreatedAt time.Time
}

// MeetingEvent is an audit/lifecycle record.
type MeetingEvent struct {
	ID        string
	MeetingID string
	Type      string
	Payload   string
	CreatedAt time.Time
}

// TranscriptFilter controls transcript retrieval.
type TranscriptFilter struct {
	SinceSequence int64 // exclusive lower bound; 0 means from start
	Speaker       string
	Limit         int
}

// DefaultTranscriptLimit is the default page size for transcript queries.
const DefaultTranscriptLimit = 100

// MaxTranscriptLimit is the hard upper bound for transcript queries.
const MaxTranscriptLimit = 500
