package indexer

import (
	"sort"
	"strings"
	"time"
)

var memoryIntermediateMarkers = []string{
	"memory processing continued",
}

// IsHiddenIntermediateMessage reports whether a message is an internal memory
// coordination artifact that should be hidden from display, search, and export.
func IsHiddenIntermediateMessage(m *Message) bool {
	if m == nil {
		return false
	}
	for _, text := range messageVisibilityTexts(m) {
		if looksLikeMemoryIntermediateText(text) {
			return true
		}
	}
	return false
}

// VisibleMessages returns a filtered copy of msgs with hidden intermediate
// messages removed. If limit is positive, the newest limit visible messages are
// returned.
func VisibleMessages(msgs []*Message, limit int) []*Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]*Message, 0, len(msgs))
	for _, msg := range msgs {
		if IsHiddenIntermediateMessage(msg) {
			continue
		}
		out = append(out, msg)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return append([]*Message(nil), out...)
}

// SessionDisplayTitle derives a safe display title from a session and its
// visible messages. Hidden intermediate messages are never used as titles.
func SessionDisplayTitle(s Session, visibleMsgs []*Message) string {
	fallback := strings.TrimSpace(fallbackTitleFromSession(&s))
	if t := normalizeTitleCandidate(strings.TrimSpace(s.Title), &s); t != "" && (fallback == "" || t != trimTitle(fallback)) {
		return t
	}
	for _, msg := range visibleMsgs {
		if msg == nil {
			continue
		}
		if t := normalizeTitleCandidate(strings.TrimSpace(msg.Content), &s); t != "" {
			return t
		}
	}
	if fallback != "" {
		return trimTitle(fallback)
	}
	if id := strings.TrimSpace(s.ID); id != "" {
		return id
	}
	return ""
}

// SessionView projects session metadata from visible messages only. Sessions
// with no visible messages should be hidden by callers.
func SessionView(s Session, visibleMsgs []*Message) (Session, bool) {
	if len(visibleMsgs) == 0 {
		return Session{}, false
	}

	view := s
	view.MessageCount = 0
	view.TextCount = 0
	view.FirstAt = time.Time{}
	view.LastAt = time.Time{}
	view.Models = make(map[string]int)
	view.Roles = make(map[string]int)
	view.Sources = nil

	sourcesSeen := make(map[string]struct{})
	for _, msg := range visibleMsgs {
		if msg == nil {
			continue
		}
		view.MessageCount++
		if strings.TrimSpace(msg.Content) != "" {
			view.TextCount++
		}
		if !msg.Ts.IsZero() {
			if view.FirstAt.IsZero() || msg.Ts.Before(view.FirstAt) {
				view.FirstAt = msg.Ts
			}
			if view.LastAt.IsZero() || msg.Ts.After(view.LastAt) {
				view.LastAt = msg.Ts
			}
		}
		if model := strings.TrimSpace(msg.Model); model != "" {
			view.Models[model]++
		}
		if role := strings.TrimSpace(msg.Role); role != "" {
			view.Roles[role]++
		}
		if src := strings.TrimSpace(msg.Source); src != "" {
			if _, ok := sourcesSeen[src]; !ok {
				sourcesSeen[src] = struct{}{}
				view.Sources = append(view.Sources, src)
			}
		}
	}
	sort.Strings(view.Sources)
	view.Title = SessionDisplayTitle(view, visibleMsgs)
	return view, true
}

func messageVisibilityTexts(m *Message) []string {
	parts := make([]string, 0, 6)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, existing := range parts {
			if existing == s {
				return
			}
		}
		parts = append(parts, s)
	}

	add(m.Content)
	add(m.Thinking)
	if m.Raw != nil {
		add(extractText(m.Raw))
		add(stringOr(m.Raw["text"]))
		add(stringOr(m.Raw["message"]))
		add(extractSummaryText(m.Raw["summary"]))
	}
	return parts
}

func looksLikeMemoryIntermediateText(s string) bool {
	if s == "" {
		return false
	}
	normalized := normalizeVisibilityText(s)
	if strings.Contains(normalized, "memory agent") &&
		(strings.Contains(normalized, "primary claude session") ||
			strings.Contains(normalized, "observe the primary claude session")) {
		return true
	}
	for _, marker := range memoryIntermediateMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func normalizeVisibilityText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func extractSummaryText(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, el := range t {
			switch item := el.(type) {
			case string:
				if strings.TrimSpace(item) != "" {
					parts = append(parts, item)
				}
			case map[string]any:
				if s := strings.TrimSpace(stringOr(item["text"])); s != "" {
					parts = append(parts, s)
					continue
				}
				if s := strings.TrimSpace(stringOr(item["content"])); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}
