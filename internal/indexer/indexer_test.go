package indexer

import (
    "encoding/json"
    "testing"
    "time"
)

func TestTrimTitle(t *testing.T) {
    short := "hello"
    if got := trimTitle(short); got != short {
        t.Fatalf("trimTitle short: got %q want %q", got, short)
    }
    long := "x" + string(make([]byte, 200))
    got := trimTitle(long)
    if len(got) != 81 || got[80] != '…' {
        t.Fatalf("trimTitle long: len=%d suffix=%q", len(got), got[len(got)-1:])
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
    x := New("/tmp/.codex")
    // first message should set title from content
    line1 := `{"id":"m1","session_id":"s1","role":"user","content":"Build a CLI tool","ts":"2024-01-02T03:04:05Z","model":"gpt-4","cwd":"/home/user/project1"}`
    x.ingestLine("s1", "/tmp/.codex/sessions/s1.jsonl", line1)

    // assistant reply
    line2 := `{"id":"m2","session_id":"s1","role":"assistant","content":"Sure, here is a plan","ts":"2024-01-02T03:05:05Z","model":"gpt-4"}`
    x.ingestLine("s1", "/tmp/.codex/sessions/s1.jsonl", line2)

    // second session with explicit title and cwd in environment_context
    line3 := `{"id":"m3","session_id":"s2","role":"user","title":"Project Setup","content":"Let's start","ts":"2024-01-02T04:05:05Z","environment_context":"<environment_context> <cwd>/workspace/app</cwd> </environment_context>"}`
    x.ingestLine("s2", "/tmp/.codex/sessions/s2.jsonl", line3)

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
    if ss[0].CWD != "/workspace/app" && ss[0].CWD != "/workspace/app" { // allow either exact
        t.Fatalf("s2 CWD got %q", ss[0].CWD)
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
