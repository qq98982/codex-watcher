package search

import (
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
