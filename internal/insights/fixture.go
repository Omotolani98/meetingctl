package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Omotolani98/meetingctl/internal/meetings"
)

// FixtureAnalyzer loads analysis.json from a fixture directory.
type FixtureAnalyzer struct {
	Result meetings.AnalysisResult
}

type fixtureFile struct {
	Summary  string `json:"summary"`
	Insights []struct {
		Type            string   `json:"type"`
		Text            string   `json:"text"`
		Owner           string   `json:"owner"`
		Status          string   `json:"status"`
		Confidence      float64  `json:"confidence"`
		SourceSequences []int64  `json:"sourceSequences"`
		SourceIDs       []string `json:"sourceIds"`
	} `json:"insights"`
}

// LoadFixtureAnalyzer loads analysis.json from dir.
func LoadFixtureAnalyzer(dir string) (*FixtureAnalyzer, error) {
	path := filepath.Join(dir, "analysis.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open fixture analysis: %w", err)
	}
	var ff fixtureFile
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("parse analysis.json: %w", err)
	}
	fa := &FixtureAnalyzer{
		Result: meetings.AnalysisResult{Summary: ff.Summary},
	}
	for _, raw := range ff.Insights {
		ins := meetings.MeetingInsight{
			Type:       meetings.InsightType(raw.Type),
			Text:       raw.Text,
			Owner:      raw.Owner,
			Status:     raw.Status,
			Confidence: raw.Confidence,
			SourceIDs:  append([]string{}, raw.SourceIDs...),
		}
		for _, seq := range raw.SourceSequences {
			ins.SourceIDs = append(ins.SourceIDs, fmt.Sprintf("seq:%d", seq))
		}
		fa.Result.Insights = append(fa.Result.Insights, ins)
	}
	return fa, nil
}

// Analyze returns the fixture result with source sequences resolved to segment IDs.
func (f *FixtureAnalyzer) Analyze(ctx context.Context, input meetings.AnalysisInput) (meetings.AnalysisResult, error) {
	if err := ctx.Err(); err != nil {
		return meetings.AnalysisResult{}, err
	}
	seqToID := make(map[int64]string, len(input.Segments))
	for _, seg := range input.Segments {
		seqToID[seg.Sequence] = seg.ID
	}
	out := meetings.AnalysisResult{
		Summary:  f.Result.Summary,
		Insights: make([]meetings.MeetingInsight, 0, len(f.Result.Insights)),
	}
	if out.Summary == "" {
		out.Summary = defaultSummary(input)
	}
	for _, ins := range f.Result.Insights {
		resolved := meetings.MeetingInsight{
			Type:       ins.Type,
			Text:       ins.Text,
			Owner:      ins.Owner,
			Status:     ins.Status,
			Confidence: ins.Confidence,
		}
		if resolved.Status == "" {
			resolved.Status = "open"
		}
		for _, src := range ins.SourceIDs {
			if strings.HasPrefix(src, "seq:") {
				var seq int64
				if _, err := fmt.Sscanf(src, "seq:%d", &seq); err == nil {
					if id, ok := seqToID[seq]; ok {
						resolved.SourceIDs = append(resolved.SourceIDs, id)
					}
				}
				continue
			}
			resolved.SourceIDs = append(resolved.SourceIDs, src)
		}
		out.Insights = append(out.Insights, resolved)
	}
	return out, nil
}

func defaultSummary(input meetings.AnalysisInput) string {
	if len(input.Segments) == 0 {
		return fmt.Sprintf("Meeting %q has no transcript segments.", input.Meeting.Title)
	}
	return fmt.Sprintf("Meeting %q captured %d finalized transcript segments.", input.Meeting.Title, len(input.Segments))
}

// StaticAnalyzer always returns the same result (for unit tests).
type StaticAnalyzer struct {
	Result meetings.AnalysisResult
	Err    error
}

// Analyze implements meetings.Analyzer.
func (s *StaticAnalyzer) Analyze(ctx context.Context, _ meetings.AnalysisInput) (meetings.AnalysisResult, error) {
	if s.Err != nil {
		return meetings.AnalysisResult{}, s.Err
	}
	return s.Result, ctx.Err()
}
