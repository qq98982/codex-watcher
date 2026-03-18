package search

import (
	"strings"
	"testing"
	"time"

	"codex-watcher/internal/indexer"
)

// helper to build an indexer with a few messages
func buildTestIndexer(t *testing.T) *indexer.Indexer {
	t.Helper()
	x := indexer.New("/tmp/.codex", "")
	now := time.Now()
	// Session s1: content with "go build"
	x.IngestForTest("s1", map[string]any{
		"id": "m1", "session_id": "s1", "role": "user", "content": "please run go build for me", "ts": now.Format(time.RFC3339),
	})
	// Session s2: tool outputs with go build
	x.IngestForTest("s2", map[string]any{
		"id": "m2", "session_id": "s2", "type": "function_call", "arguments": `{"command":["bash","-lc","go build ./..."]}`,
	})
	x.IngestForTest("s2", map[string]any{
		"id": "m3", "session_id": "s2", "type": "function_call_output", "output": `{"output":"go build ok","stderr":""}`,
	})
	return x
}

func TestRegexContent(t *testing.T) {
	idx := buildTestIndexer(t)
	q := Parse(`/go\s+build/i`, "content")
	res := Exec(idx, q, 50, 0)
	if res.Total <= 0 {
		t.Fatalf("want content regex hits > 0, got %d; hits=%v", res.Total, res.Hits)
	}
}

func TestRegexTools(t *testing.T) {
	idx := buildTestIndexer(t)
	q := Parse(`/go\s+build/i`, "tools")
	res := Exec(idx, q, 50, 0)
	if res.Total <= 0 {
		t.Fatalf("want tools regex hits > 0, got %d; hits=%v", res.Total, res.Hits)
	}
}

func TestInScopeOverridesParam(t *testing.T) {
	idx := buildTestIndexer(t)
	// Even if param says content, in:tools should switch to tool scope
	q := Parse(`in:tools go build`, "content")
	res := Exec(idx, q, 50, 0)
	if res.Total <= 0 {
		t.Fatalf("in:tools should search tools scope, got %d", res.Total)
	}
}

func TestSearchSkipsMemoryMessagesAndUsesVisibleTitle(t *testing.T) {
	idx := indexer.New("/tmp/.codex", "")
	now := time.Date(2026, time.March, 18, 12, 0, 0, 0, time.UTC)

	idx.IngestForTest("s1", map[string]any{
		"id":         "mem-1",
		"session_id": "s1",
		"role":       "user",
		"content":    "Hello memory agent, you are continuing to observe the primary Claude session.",
		"cwd":        "/workspace/app",
		"ts":         now.Format(time.RFC3339),
	})
	idx.IngestForTest("s1", map[string]any{
		"id":         "msg-1",
		"session_id": "s1",
		"role":       "user",
		"content":    "Ship the dashboard fix today",
		"cwd":        "/workspace/app",
		"ts":         now.Add(time.Minute).Format(time.RFC3339),
	})
	idx.IngestForTest("s2", map[string]any{
		"id":         "mem-2",
		"session_id": "s2",
		"role":       "assistant",
		"content":    "MEMORY PROCESSING CONTINUED",
		"cwd":        "/workspace/hidden",
		"ts":         now.Format(time.RFC3339),
	})

	memoryOnly := Exec(idx, Parse(`"memory agent"`, "content"), 50, 0)
	if memoryOnly.Total != 0 {
		t.Fatalf("memory search should be filtered, got total=%d hits=%v", memoryOnly.Total, memoryOnly.Hits)
	}

	continuedOnly := Exec(idx, Parse(`"MEMORY PROCESSING CONTINUED"`, "content"), 50, 0)
	if continuedOnly.Total != 0 {
		t.Fatalf("memory continuation search should be filtered, got total=%d hits=%v", continuedOnly.Total, continuedOnly.Hits)
	}

	visible := Exec(idx, Parse(`dashboard`, "content"), 50, 0)
	if visible.Total != 1 {
		t.Fatalf("visible search should still work, got total=%d hits=%v", visible.Total, visible.Hits)
	}
	if got := visible.Hits[0].SessionTitle; got != "Ship the dashboard fix today" {
		t.Fatalf("session title=%q want %q", got, "Ship the dashboard fix today")
	}
	if strings.Contains(strings.ToLower(visible.Hits[0].Content), "memory agent") {
		t.Fatalf("visible hit content should not include memory prompt: %q", visible.Hits[0].Content)
	}
}
