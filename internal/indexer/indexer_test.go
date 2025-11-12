package indexer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTrimTitle(t *testing.T) {
	short := "hello"
	if got := trimTitle(short); got != short {
		t.Fatalf("trimTitle short: got %q want %q", got, short)
	}
	long := "x" + string(make([]byte, 200))
	got := trimTitle(long)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("trimTitle long should end with ellipsis: %q", got)
	}
	if utf8.RuneCountInString(got) != 81 {
		t.Fatalf("trimTitle long rune count mismatch: runes=%d, got=%q", utf8.RuneCountInString(got), got)
	}
}

func TestParseTime(t *testing.T) {
	ts, ok := parseTime("2024-01-02T03:04:05Z")
	if !ok || ts.IsZero() {
		t.Fatal("parse RFC3339 failed")
	}
	ts, ok = parseTime("1700000000")
	if !ok || ts.Unix() != 1700000000 {
		t.Fatalf("parse unix string failed: %v %v", ts, ok)
	}
	ts, ok = parseTime(json.Number("1700000001"))
	if !ok || ts.Unix() != 1700000001 {
		t.Fatalf("parse json.Number failed: %v %v", ts, ok)
	}
}

func TestIngestAndSessions(t *testing.T) {
	x := New("/tmp/.codex", "")
	// first message should set title from content
	line1 := `{"id":"m1","session_id":"s1","role":"user","content":"Build a CLI tool","ts":"2024-01-02T03:04:05Z","model":"gpt-4","cwd":"/home/user/project1"}`
	x.ingestLine("codex", "", "s1", "/tmp/.codex/sessions/s1.jsonl", line1)

	// assistant reply
	line2 := `{"id":"m2","session_id":"s1","role":"assistant","content":"Sure, here is a plan","ts":"2024-01-02T03:05:05Z","model":"gpt-4"}`
	x.ingestLine("codex", "", "s1", "/tmp/.codex/sessions/s1.jsonl", line2)

	// second session with explicit title and cwd in environment_context
	line3 := `{"id":"m3","session_id":"s2","role":"user","title":"Project Setup","content":"Let's start","ts":"2024-01-02T04:05:05Z","environment_context":"<environment_context> <cwd>/workspace/app</cwd> </environment_context>"}`
	x.ingestLine("codex", "", "s2", "/tmp/.codex/sessions/s2.jsonl", line3)

	// assertions
	if x.stats.TotalMessages != 3 {
		t.Fatalf("TotalMessages=%d want 3", x.stats.TotalMessages)
	}
	if x.stats.TotalSessions != 2 {
		t.Fatalf("TotalSessions=%d want 2", x.stats.TotalSessions)
	}

	// sessions are sorted by LastAt desc; s2 should be first
	ss := x.Sessions()
	if len(ss) != 2 {
		t.Fatalf("Sessions len=%d want 2", len(ss))
	}
	if ss[0].ID != "s2" || ss[0].Title == "" {
		t.Fatalf("s2 should be first with title, got id=%s title=%q", ss[0].ID, ss[0].Title)
	}
	if ss[1].ID != "s1" || ss[1].Title == "" {
		t.Fatalf("s1 should have derived title, got id=%s title=%q", ss[1].ID, ss[1].Title)
	}

	// cwd extraction
	if ss[1].CWD != "/home/user/project1" {
		t.Fatalf("s1 CWD got %q", ss[1].CWD)
	}
	if ss[1].CWDBase != "project1" {
		t.Fatalf("s1 CWDBase got %q", ss[1].CWDBase)
	}
	if ss[0].CWD != "/workspace/app" { // extracted from <cwd> in environment_context
		t.Fatalf("s2 CWD got %q", ss[0].CWD)
	}
	if ss[0].CWDBase != "app" {
		t.Fatalf("s2 CWDBase got %q", ss[0].CWDBase)
	}

	// messages API returns latest N; with limit
	msgs := x.Messages("s1", 1)
	if len(msgs) != 1 || msgs[0].ID != "m2" {
		t.Fatalf("Messages limit=1 got %v", msgs)
	}

	// ensure timestamps were parsed and set on session bounds
	if ss[1].FirstAt.IsZero() || ss[1].LastAt.IsZero() || !ss[1].LastAt.After(ss[1].FirstAt) {
		t.Fatalf("s1 timestamps not set correctly: first=%v last=%v", ss[1].FirstAt, ss[1].LastAt)
	}

	// sanity: parseTime returns UTC times; compare date
	y, m, d := ss[1].FirstAt.Date()
	if y != 2024 || m != time.January || d != 2 {
		t.Fatalf("unexpected firstAt date: %v", ss[1].FirstAt)
	}
}

func TestEnvironmentContextTitleFallback(t *testing.T) {
	x := New("/tmp/.codex", "")
	sid := "rollout-2025-11-04T18-33-09-019a4e36-8d3f-7b13-9df1-655d8e4f9bbd"
	line := fmt.Sprintf(`{"id":"env1","session_id":"%s","role":"system","content":"<environment_context><cwd>/workspace/app</cwd><approval_policy>never</approval_policy><sandbox_mode>danger-full-access</sandbox_mode><shell>zsh</shell></environment_context>","environment_context":"<environment_context><cwd>/workspace/app</cwd><approval_policy>never</approval_policy><sandbox_mode>danger-full-access</sandbox_mode><shell>zsh</shell></environment_context>","ts":"2024-01-02T03:04:05Z"}`, sid)
	x.ingestLine("codex", "", sid, "/tmp/.codex/sessions/env-session.jsonl", line)

	ss := x.Sessions()
	if len(ss) != 1 {
		t.Fatalf("expected 1 session, got %d", len(ss))
	}
	if got := ss[0].Title; got != "app" {
		t.Fatalf("expected fallback title 'app', got %q", got)
	}
}

func TestRolloutTitlePreferredContent(t *testing.T) {
	x := New("/tmp/.codex", "")
	sid := "rollout-2025-11-04T18-33-09-019a4e36-8d3f-7b13-9df1-655d8e4f9bbd"
	line := fmt.Sprintf(`{"id":"m1","session_id":"%s","role":"user","title":"%s","content":"Fix the search titles please","ts":"2025-11-04T18:33:09Z","cwd":"/workspace/app"}`, sid, sid)
	x.ingestLine("codex", "", sid, "/tmp/.codex/sessions/rollout.jsonl", line)

	ss := x.Sessions()
	if len(ss) != 1 {
		t.Fatalf("expected 1 session, got %d", len(ss))
	}
	if got := ss[0].Title; got != "Fix the search titles please" {
		t.Fatalf("expected content-derived title, got %q", got)
	}
}

func TestExtractTextVariants(t *testing.T) {
	tests := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{
			name: "payload content array",
			raw: map[string]any{
				"type": "response_item",
				"payload": map[string]any{
					"type": "message",
					"role": "user",
					"content": []any{
						map[string]any{"type": "input_text", "text": "那主要的文字意思是对的就可以了"},
					},
				},
			},
			want: "那主要的文字意思是对的就可以了",
		},
		{
			name: "payload message field",
			raw: map[string]any{
				"type": "event_msg",
				"payload": map[string]any{
					"type":    "agent_message",
					"message": "我搜索了一个对话，但是没有找到",
				},
			},
			want: "我搜索了一个对话，但是没有找到",
		},
		{
			name: "legacy content string",
			raw: map[string]any{
				"content": "Build a CLI tool",
			},
			want: "Build a CLI tool",
		},
		{
			name: "legacy content array with thinking ignored",
			raw: map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": "Part 1"},
					map[string]any{"type": "thinking", "thinking": "internal"},
					map[string]any{"type": "text", "text": "Part 2"},
				},
			},
			want: "Part 1\n\nPart 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractText(tt.raw); got != tt.want {
				t.Fatalf("extractText got %q want %q", got, tt.want)
			}
		})
	}
}

func TestIngestSkipsDuplicateEventMessages(t *testing.T) {
	x := New("/tmp/.codex", "")
	sessionPath := "/tmp/.codex/sessions/sdup.jsonl"

	responseUser := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Hi"}]}}`
	eventUser := `{"type":"event_msg","payload":{"type":"user_message","message":"Hi"}}`
	responseAssistant := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`
	eventAssistant := `{"type":"event_msg","payload":{"type":"agent_message","message":"Hello"}}`

	x.ingestLine("codex", "", "sdup", sessionPath, responseUser)
	x.ingestLine("codex", "", "sdup", sessionPath, eventUser)
	x.ingestLine("codex", "", "sdup", sessionPath, responseAssistant)
	x.ingestLine("codex", "", "sdup", sessionPath, eventAssistant)

	msgs := x.Messages("sdup", 0)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after skipping duplicates, got %d", len(msgs))
	}
	if msgs[0].Content == "" || msgs[1].Content == "" {
		t.Fatalf("messages should retain content: %+v", msgs)
	}
}
