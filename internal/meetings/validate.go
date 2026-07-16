package meetings

import (
	"fmt"
	"strings"
)

// NormalizeLimit clamps a transcript page size to safe bounds.
func NormalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultTranscriptLimit
	}
	if limit > MaxTranscriptLimit {
		return MaxTranscriptLimit
	}
	return limit
}

// ParseParticipants splits a comma-separated participant list.
func ParseParticipants(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// ValidateTitle ensures a meeting title is usable.
func ValidateTitle(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title is required")
	}
	if len(title) > 200 {
		return fmt.Errorf("title must be at most 200 characters")
	}
	return nil
}

// ValidateNote ensures a manual note is non-empty and bounded.
func ValidateNote(note string) error {
	note = strings.TrimSpace(note)
	if note == "" {
		return fmt.Errorf("note is required")
	}
	if len(note) > 4000 {
		return fmt.Errorf("note must be at most 4000 characters")
	}
	return nil
}

// ValidInsightType reports whether t is a known insight type.
func ValidInsightType(t InsightType) bool {
	switch t {
	case InsightDecision, InsightActionItem, InsightQuestion, InsightRisk, InsightBlocker, InsightNote:
		return true
	default:
		return false
	}
}

// ParseMarkType maps CLI mark names to insight types.
func ParseMarkType(raw string) (InsightType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "decision":
		return InsightDecision, nil
	case "action-item", "action_item", "action":
		return InsightActionItem, nil
	case "question":
		return InsightQuestion, nil
	case "risk":
		return InsightRisk, nil
	case "blocker":
		return InsightBlocker, nil
	case "note":
		return InsightNote, nil
	default:
		return "", fmt.Errorf("unknown mark type %q (want decision, action-item, question, risk, blocker, note)", raw)
	}
}
