package exporter

import (
    "bytes"
    "strings"
    "testing"
    "time"

    "codex-watcher/internal/indexer"
)

func buildIdxForExport(t *testing.T) *indexer.Indexer {
    t.Helper()
    x := indexer.New("/tmp/.codex", "")
    now := time.Now().Format(time.RFC3339)
    // Normal messages
    x.IngestForTest("s1", map[string]any{"id":"m1","session_id":"s1","role":"user","content":"hello","ts":now})
    x.IngestForTest("s1", map[string]any{"id":"m2","session_id":"s1","role":"assistant","content":"world","ts":now})
    // Tool: shell (should be excluded)
    x.IngestForTest("s1", map[string]any{"id":"m3","session_id":"s1","type":"function_call","name":"shell","arguments":"{\"command\":[\"echo\",\"hi\"]}"})
    // Tool output (should be excluded)
    x.IngestForTest("s1", map[string]any{"id":"m4","session_id":"s1","type":"function_call_output","output":"{\"output\":\"ok\"}"})
    return x
}

func TestWriteSession_ExcludesShellAndOutputs(t *testing.T) {
    idx := buildIdxForExport(t)
    var buf bytes.Buffer
    n, err := WriteSession(&buf, idx, "s1", "json", Filters{ExcludeShellCalls: true, ExcludeToolOutputs: true})
    if err != nil { t.Fatalf("WriteSession error: %v", err) }
    out := buf.String()
    if strings.Contains(out, "function_call_output") || strings.Contains(out, "\"function_call\"") {
        t.Fatalf("export should exclude tool outputs and shell calls: %s", out)
    }
    if n <= 0 { t.Fatalf("expected some messages exported, got %d", n) }
}

func TestWriteByDirAllMarkdown_ExcludesShellAndOutputs(t *testing.T) {
    idx := buildIdxForExport(t)
    var buf bytes.Buffer
    n, err := WriteByDirAllMarkdown(&buf, idx, "", time.Time{}, time.Time{}, Filters{ExcludeShellCalls: true, ExcludeToolOutputs: true})
    if err != nil { t.Fatalf("WriteByDirAllMarkdown error: %v", err) }
    s := buf.String()
    if strings.Contains(s, "TOOLS OUTPUT") || strings.Contains(s, "### TOOLS\n\n") {
        t.Fatalf("markdown export should exclude tool outputs and shell calls: %s", s)
    }
    if n <= 0 { t.Fatalf("expected some lines exported, got %d", n) }
}
