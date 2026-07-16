package meetings

import "context"

// AnalysisInput is the meeting content available for structured extraction.
type AnalysisInput struct {
	Meeting      Meeting
	Segments     []TranscriptSegment
	Notes        []ManualNote
	PriorSummary string
}

// AnalysisResult is structured meeting knowledge.
type AnalysisResult struct {
	Summary  string
	Insights []MeetingInsight
}

// Analyzer extracts summaries and insights from meeting content.
type Analyzer interface {
	Analyze(ctx context.Context, input AnalysisInput) (AnalysisResult, error)
}
