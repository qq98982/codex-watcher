package api

import (
	"strings"
	"testing"

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

func TestIndexHTMLOpensToolBlocksByDefault(t *testing.T) {
	if !strings.Contains(indexHTML, "let collapseTools = false;") {
		t.Fatalf("indexHTML should render tool blocks expanded by default")
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
