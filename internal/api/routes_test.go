package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex-watcher/internal/indexer"
)

func TestIndexHTMLShowsResumeButtonForCodexSessions(t *testing.T) {
	if !strings.Contains(indexHTML, "provider === 'claude' || provider === 'codex'") {
		t.Fatalf("indexHTML should treat codex sessions as resumable")
	}

	legacyGuards := []string{
		"sess && sess.cwd && sess.provider === 'claude'",
		"it.cwd && it.provider === 'claude'",
	}
	for _, guard := range legacyGuards {
		if strings.Contains(indexHTML, guard) {
			t.Fatalf("indexHTML still uses claude-only resume guard: %q", guard)
		}
	}
}

func TestIndexHTMLCollapsesToolBlocksByDefault(t *testing.T) {
	if !strings.Contains(indexHTML, "let collapseTools = true;") {
		t.Fatalf("indexHTML should render tool blocks collapsed by default")
	}

	if !strings.Contains(indexHTML, "Show more") {
		t.Fatalf("indexHTML should keep the Show more toggle for long tool output")
	}
}

func TestIndexHTMLReadsToolFieldsFromPayload(t *testing.T) {
	if !strings.Contains(indexHTML, "function toolEventData(m)") {
		t.Fatalf("indexHTML should normalize tool payload access")
	}

	legacyReads := []string{
		"var toolNameRaw = (m.raw && m.raw.name) || 'tool';",
		"var name = (m.raw && m.raw.name) || '';",
		"var args = (m.raw && m.raw.arguments);",
		"var out = (m.raw && m.raw.output);",
	}
	for _, read := range legacyReads {
		if strings.Contains(indexHTML, read) {
			t.Fatalf("indexHTML still reads tool data from top-level raw object: %q", read)
		}
	}
}

func TestIndexHTMLLoadsFullVisibleSessionHistory(t *testing.T) {
	if !strings.Contains(indexHTML, "fetch('/api/messages?session_id=' + encodeURIComponent(id) + '&limit=0')") {
		t.Fatalf("indexHTML should request the full visible session history")
	}
	if strings.Contains(indexHTML, "fetch('/api/messages?session_id=' + encodeURIComponent(id) + '&limit=500')") {
		t.Fatalf("indexHTML should not cap the session history at 500 messages")
	}
}

func TestReorderMessagesForDisplayPairsOutputsWithMatchingCalls(t *testing.T) {
	msgs := []*indexer.Message{
		testToolMessage("call-1", "function_call", "call-a"),
		testToolMessage("call-2", "function_call", "call-b"),
		testToolMessage("out-1", "function_call_output", "call-a"),
		testToolMessage("out-2", "function_call_output", "call-b"),
	}

	got := reorderMessagesForDisplay(msgs)
	want := []string{"call-1", "out-1", "call-2", "out-2"}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

func TestReorderMessagesForDisplayKeepsMultipleOutputsAfterSameCall(t *testing.T) {
	msgs := []*indexer.Message{
		testToolMessage("call-1", "function_call", "call-a"),
		testToolMessage("call-2", "function_call", "call-b"),
		testToolMessage("out-1a", "function_call_output", "call-a"),
		testToolMessage("out-1b", "function_call_output", "call-a"),
		testToolMessage("out-2", "function_call_output", "call-b"),
	}

	got := reorderMessagesForDisplay(msgs)
	want := []string{"call-1", "out-1a", "out-1b", "call-2", "out-2"}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("got[%d].ID=%q want %q", i, got[i].ID, id)
		}
	}
}

func testToolMessage(id, typ, callID string) *indexer.Message {
	payload := map[string]any{"type": typ}
	if callID != "" {
		payload["call_id"] = callID
	}
	return &indexer.Message{
		ID:   id,
		Type: typ,
		Raw: map[string]any{
			"type":    "response_item",
			"payload": payload,
		},
	}
}

func TestAPIHidesMemoryMessagesFromSessionsAndMessages(t *testing.T) {
	idx := indexer.New("/tmp/.codex", "")
	now := time.Date(2026, time.March, 18, 12, 0, 0, 0, time.UTC)

	idx.IngestForTest("s-visible", map[string]any{
		"id":         "mem-1",
		"session_id": "s-visible",
		"role":       "user",
		"content":    "Hello memory agent, you are continuing to observe the primary Claude session.",
		"cwd":        "/workspace/app",
		"ts":         now.Format(time.RFC3339),
	})
	idx.IngestForTest("s-visible", map[string]any{
		"id":         "msg-1",
		"session_id": "s-visible",
		"role":       "user",
		"content":    "Ship the dashboard fix today",
		"cwd":        "/workspace/app",
		"ts":         now.Add(time.Minute).Format(time.RFC3339),
	})
	idx.IngestForTest("s-hidden", map[string]any{
		"id":         "mem-2",
		"session_id": "s-hidden",
		"role":       "assistant",
		"content":    "MEMORY PROCESSING CONTINUED",
		"cwd":        "/workspace/hidden",
		"ts":         now.Format(time.RFC3339),
	})

	mux := http.NewServeMux()
	AttachRoutes(mux, idx)

	msgReq := httptest.NewRequest(http.MethodGet, "/api/messages?session_id=s-visible", nil)
	msgRec := httptest.NewRecorder()
	mux.ServeHTTP(msgRec, msgReq)
	if msgRec.Code != http.StatusOK {
		t.Fatalf("/api/messages status=%d want %d", msgRec.Code, http.StatusOK)
	}
	var msgs []indexer.Message
	if err := json.NewDecoder(msgRec.Body).Decode(&msgs); err != nil {
		t.Fatalf("decode /api/messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("visible session should expose 1 message after filtering, got %d", len(msgs))
	}
	if msgs[0].ID != "msg-1" {
		t.Fatalf("visible message id=%q want %q", msgs[0].ID, "msg-1")
	}

	sessReq := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	sessRec := httptest.NewRecorder()
	mux.ServeHTTP(sessRec, sessReq)
	if sessRec.Code != http.StatusOK {
		t.Fatalf("/api/sessions status=%d want %d", sessRec.Code, http.StatusOK)
	}
	var sessions []indexer.Session
	if err := json.NewDecoder(sessRec.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode /api/sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected only the visible session to remain, got %d sessions", len(sessions))
	}
	if sessions[0].ID != "s-visible" {
		t.Fatalf("session id=%q want %q", sessions[0].ID, "s-visible")
	}
	if sessions[0].Title != "Ship the dashboard fix today" {
		t.Fatalf("session title=%q want %q", sessions[0].Title, "Ship the dashboard fix today")
	}
	if sessions[0].MessageCount != 1 {
		t.Fatalf("session message_count=%d want 1", sessions[0].MessageCount)
	}
}
