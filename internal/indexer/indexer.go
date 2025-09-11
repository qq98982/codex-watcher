package indexer

import (
    "bufio"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"
)

// Message represents a single JSONL event/message extracted from Codex logs.
type Message struct {
    ID        string                 `json:"id,omitempty"`
    SessionID string                 `json:"session_id,omitempty"`
    Ts        time.Time              `json:"ts,omitempty"`
    Role      string                 `json:"role,omitempty"`
    Content   string                 `json:"content,omitempty"`
    Model     string                 `json:"model,omitempty"`
    Type      string                 `json:"type,omitempty"`
    ToolName  string                 `json:"tool_name,omitempty"`
    Raw       map[string]any         `json:"raw,omitempty"`
    Source    string                 `json:"source"` // history.jsonl or sessions/<file>
    LineNo    int                    `json:"line_no"`
}

// Session aggregates messages by session id or file.
type Session struct {
    ID           string            `json:"id"`
    Title        string            `json:"title,omitempty"`
    FirstAt      time.Time         `json:"first_at,omitempty"`
    LastAt       time.Time         `json:"last_at,omitempty"`
    FileModAt    time.Time         `json:"file_mod_at,omitempty"`
    MessageCount int               `json:"message_count"`
    TextCount    int               `json:"text_count"`
    CWD          string            `json:"cwd,omitempty"`
    CWDBase      string            `json:"cwd_base,omitempty"`
    Models       map[string]int    `json:"models,omitempty"`
    Roles        map[string]int    `json:"roles,omitempty"`
    Tags         []string          `json:"tags,omitempty"`
    Sources      []string          `json:"sources,omitempty"`
}

// Indexer tails JSONL files under ~/.codex and builds an in-memory index.
type Indexer struct {
    codexDir string

    mu        sync.RWMutex
    sessions  map[string]*Session
    messages  map[string][]*Message // by session id
    stats     Stats
    positions map[string]int64 // file path -> byte offset (tail)
    lineNos   map[string]int    // file path -> last line number processed

    // control
    pollInterval time.Duration
}

type Stats struct {
    TotalMessages int            `json:"total_messages"`
    TotalSessions int            `json:"total_sessions"`
    ByRole        map[string]int `json:"by_role,omitempty"`
    ByModel       map[string]int `json:"by_model,omitempty"`
    Fields        map[string]int `json:"fields,omitempty"` // observed top-level JSON keys
    // observability
    BadLines      int            `json:"bad_lines,omitempty"`
    FilesScanned  int            `json:"files_scanned,omitempty"`
    LastScanMs    int            `json:"last_scan_ms,omitempty"`
}

func New(codexDir string) *Indexer {
    return &Indexer{
        codexDir:     codexDir,
        sessions:     make(map[string]*Session),
        messages:     make(map[string][]*Message),
        positions:    make(map[string]int64),
        lineNos:      make(map[string]int),
        pollInterval: 1500 * time.Millisecond,
        stats: Stats{
            ByRole:  make(map[string]int),
            ByModel: make(map[string]int),
            Fields:  make(map[string]int),
        },
    }
}

// Run starts a polling loop to scan and tail JSONL files.
func (x *Indexer) Run(ctxDone <-chan struct{}) {
    // Initial scan
    _ = x.scanAll()

    ticker := time.NewTicker(x.pollInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctxDone:
            return
        case <-ticker.C:
            _ = x.scanAll()
        }
    }
}

// scanAll locates known files and tails new lines.
func (x *Indexer) scanAll() error {
    start := time.Now()
    files := 0
    // sessions/*.jsonl
    sessionsDir := filepath.Join(x.codexDir, "sessions")
    _ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
        if err != nil {
            return nil // ignore errors per-file
        }
        if d == nil || d.IsDir() {
            return nil
        }
        if strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
            id := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
            if id == "" {
                id = d.Name()
            }
            _ = x.tailFile(id, path)
            files++
        }
        return nil
    })
    // update observability metrics
    x.mu.Lock()
    x.stats.FilesScanned = files
    x.stats.LastScanMs = int(time.Since(start).Milliseconds())
    x.mu.Unlock()
    return nil
}

func (x *Indexer) tailFile(sessionID, path string) error {
    // stat file to capture mod time
    var modTime time.Time
    if fi, err := os.Stat(path); err == nil {
        modTime = fi.ModTime()
    }
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer f.Close()

    // seek to last position
    pos := x.positions[path]
    if pos > 0 {
        if _, err := f.Seek(pos, io.SeekStart); err != nil {
            // if seek fails (e.g., truncated), reset
            x.positions[path] = 0
            x.lineNos[path] = 0
            _, _ = f.Seek(0, io.SeekStart)
        }
    }

    reader := bufio.NewReader(f)
    var nBytes int64
    for {
        line, err := reader.ReadBytes('\n')
        nBytes += int64(len(line))
        if len(strings.TrimSpace(string(line))) > 0 {
            x.ingestLine(sessionID, path, string(line))
        }
        if errors.Is(err, io.EOF) {
            break
        }
        if err != nil {
            break
        }
    }
    // record new position
    if pos == 0 {
        // if starting at 0, we need current size
        if off, err := f.Seek(0, io.SeekCurrent); err == nil {
            x.positions[path] = off
        }
    } else {
        x.positions[path] = pos + nBytes
    }
    // update session file mod time (create session record if needed)
    if !modTime.IsZero() {
        x.mu.Lock()
        s := x.sessions[sessionID]
        if s == nil {
            s = &Session{ID: sessionID, Models: map[string]int{}, Roles: map[string]int{}}
            x.sessions[sessionID] = s
        }
        if modTime.After(s.FileModAt) {
            s.FileModAt = modTime
        }
        x.mu.Unlock()
    }
    return nil
}

func (x *Indexer) ingestLine(sessionID, path, line string) {
    var raw map[string]any
    if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &raw); err != nil {
        // ignore bad line but record count
        x.mu.Lock()
        x.stats.BadLines++
        x.mu.Unlock()
        return
    }

    // attempt to map common fields
    msg := &Message{
        ID:        stringOr(raw["id"]),
        SessionID: firstNonEmpty(stringOr(raw["session_id"]), sessionID),
        Role:      stringOr(raw["role"]),
        Content:   extractText(raw),
        Model:     stringOr(raw["model"]),
        Type:      stringOr(raw["type"]),
        ToolName:  stringOr(raw["tool_name"]),
        Raw:       raw,
        Source:    relSource(path, x.codexDir),
    }

    if ts, ok := parseTime(raw["timestamp"], raw["ts"], raw["created_at"]); ok {
        msg.Ts = ts
    }

    x.mu.Lock()
    defer x.mu.Unlock()

    // increment line number per file
    x.lineNos[path]++
    msg.LineNo = x.lineNos[path]

    // ensure session exists
    sID := msg.SessionID
    if sID == "" {
        sID = sessionID
        msg.SessionID = sID
    }
    s := x.sessions[sID]
    if s == nil {
        s = &Session{ID: sID, Models: map[string]int{}, Roles: map[string]int{}}
        x.sessions[sID] = s
    }
    // detect and set CWD the first time we see it
    if s.CWD == "" {
        if cwd := extractCWD(raw); strings.TrimSpace(cwd) != "" {
            s.CWD = cwd
            // compute base directory name
            base := strings.TrimRight(cwd, "/")
            if base != "" {
                s.CWDBase = filepath.Base(base)
            }
        }
    }
    // derive a human-friendly session title if missing
    if s.Title == "" {
        // prefer explicit title field if present
        if t := stringOr(raw["title"]); strings.TrimSpace(t) != "" {
            s.Title = trimTitle(t)
        } else {
            // otherwise, take text-only extracted content
            cand := strings.TrimSpace(msg.Content)
            if cand != "" {
                s.Title = trimTitle(cand)
            }
        }
    }
    // update session aggregates
    s.MessageCount++
    if strings.TrimSpace(msg.Content) != "" {
        s.TextCount++
    }
    if !msg.Ts.IsZero() {
        if s.FirstAt.IsZero() || msg.Ts.Before(s.FirstAt) {
            s.FirstAt = msg.Ts
        }
        if msg.Ts.After(s.LastAt) {
            s.LastAt = msg.Ts
        }
    }
    if msg.Model != "" {
        s.Models[msg.Model]++
        x.stats.ByModel[msg.Model]++
    }
    if msg.Role != "" {
        s.Roles[msg.Role]++
        x.stats.ByRole[msg.Role]++
    }
    for k := range raw {
        if k != "" {
            x.stats.Fields[k]++
        }
    }
    // track sources
    if path != "" {
        if !contains(s.Sources, msg.Source) {
            s.Sources = append(s.Sources, msg.Source)
            sort.Strings(s.Sources)
        }
    }

    // append message (cap in memory per session to 5k for safety)
    x.messages[sID] = append(x.messages[sID], msg)
    if len(x.messages[sID]) > 5000 {
        x.messages[sID] = x.messages[sID][len(x.messages[sID])-5000:]
    }

    x.stats.TotalMessages++
    x.stats.TotalSessions = len(x.sessions)
}

// Public API

func (x *Indexer) Sessions() []Session {
    x.mu.RLock()
    defer x.mu.RUnlock()
    out := make([]Session, 0, len(x.sessions))
    for _, s := range x.sessions {
        out = append(out, *s)
    }
    sort.Slice(out, func(i, j int) bool {
        return out[i].LastAt.After(out[j].LastAt)
    })
    return out
}

func (x *Indexer) Messages(sessionID string, limit int) []*Message {
    x.mu.RLock()
    defer x.mu.RUnlock()
    msgs := x.messages[sessionID]
    if limit <= 0 || limit >= len(msgs) {
        return append([]*Message(nil), msgs...)
    }
    return append([]*Message(nil), msgs[len(msgs)-limit:]...)
}

func (x *Indexer) Stats() Stats {
    x.mu.RLock()
    defer x.mu.RUnlock()
    return x.stats
}

func (x *Indexer) Reindex() error {
    x.mu.Lock()
    x.sessions = make(map[string]*Session)
    x.messages = make(map[string][]*Message)
    x.positions = make(map[string]int64)
    x.lineNos = make(map[string]int)
    x.stats = Stats{ByRole: map[string]int{}, ByModel: map[string]int{}, Fields: map[string]int{}}
    x.mu.Unlock()
    return x.scanAll()
}

// IngestForTest allows tests to inject a raw JSON object as a line for a session.
// It bypasses file I/O and directly feeds the ingest pipeline with minimal locking.
func (x *Indexer) IngestForTest(sessionID string, raw map[string]any) {
    if raw == nil { return }
    b, _ := json.Marshal(raw)
    // mimic a file path for line numbers and source
    path := "/tmp/.codex/sessions/" + sessionID + ".jsonl"
    x.ingestLine(sessionID, path, string(b))
}

// Helpers

func stringOr(v any) string {
    switch t := v.(type) {
    case string:
        return t
    case fmt.Stringer:
        return t.String()
    case float64:
        return fmt.Sprintf("%g", t)
    case int64:
        return fmt.Sprintf("%d", t)
    case json.Number:
        return t.String()
    default:
        return ""
    }
}

func parseTime(values ...any) (time.Time, bool) {
    for _, v := range values {
        switch t := v.(type) {
        case string:
            // try common formats
            if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
                return ts, true
            }
            if ts, err := time.Parse(time.RFC3339, t); err == nil {
                return ts, true
            }
            // unix seconds as string
            if n, err := parseUnixMaybe(t); err == nil {
                return time.Unix(n, 0), true
            }
        case float64:
            // unix seconds
            if t > 1000000000 {
                return time.Unix(int64(t), 0), true
            }
        case json.Number:
            if n, err := t.Int64(); err == nil {
                return time.Unix(n, 0), true
            }
        }
    }
    return time.Time{}, false
}

func parseUnixMaybe(s string) (int64, error) {
    // crude: only ints
    for _, ch := range s {
        if ch < '0' || ch > '9' {
            return 0, fmt.Errorf("not unix")
        }
    }
    var n int64
    // use base 10 parse for safety
    // fallback to Sscan if needed
    if _, err := fmt.Sscan(s, &n); err == nil {
        return n, nil
    }
    return 0, fmt.Errorf("parse error")
}

func contains(sl []string, t string) bool {
    for _, v := range sl {
        if v == t {
            return true
        }
    }
    return false
}

func relSource(path, root string) string {
    if path == "" {
        return ""
    }
    if r, err := filepath.Rel(root, path); err == nil {
        return r
    }
    return path
}

func firstNonEmpty(a, b string) string {
    if strings.TrimSpace(a) != "" {
        return a
    }
    return b
}

func trimTitle(s string) string {
    s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
    if len(s) <= 80 {
        return s
    }
    return s[:80] + "…"
}

// extractText returns only human-readable text content from a raw JSONL line.
// Rules:
// - If content is a string, return it.
// - If content is an array, concatenate parts with type=="text" and .text string.
// - Else if raw["text"] is string, return it.
// - Otherwise return empty.
func extractText(raw map[string]any) string {
    if raw == nil {
        return ""
    }
    if v, ok := raw["content"]; ok {
        if s, ok := v.(string); ok {
            return s
        }
        if arr, ok := v.([]any); ok {
            var b strings.Builder
            for _, el := range arr {
                if ss, ok := el.(string); ok {
                    if strings.TrimSpace(ss) != "" {
                        if b.Len() > 0 {
                            b.WriteString("\n\n")
                        }
                        b.WriteString(ss)
                    }
                    continue
                }
                if m, ok := el.(map[string]any); ok {
                    if t, _ := m["type"].(string); t == "text" || t == "input_text" || t == "output_text" {
                        if tx, _ := m["text"].(string); strings.TrimSpace(tx) != "" {
                            if b.Len() > 0 {
                                b.WriteString("\n\n")
                            }
                            b.WriteString(tx)
                            continue
                        }
                        if cx, _ := m["content"].(string); strings.TrimSpace(cx) != "" {
                            if b.Len() > 0 {
                                b.WriteString("\n\n")
                            }
                            b.WriteString(cx)
                            continue
                        }
                    }
                }
            }
            return b.String()
        }
    }
    if s, ok := raw["text"].(string); ok {
        return s
    }
    return ""
}

// extractCWD attempts to find a current working directory value from common fields.
// Priority:
// 1) raw["cwd"], raw["working_dir"], raw["current_working_directory"] (string)
// 2) raw["git"].(map)["cwd"|"root"] (string)
// 3) raw["environment_context"] (string) with a <cwd>...</cwd> segment
// Otherwise returns empty string.
func extractCWD(raw map[string]any) string {
    if raw == nil {
        return ""
    }
    // direct fields
    for _, k := range []string{"cwd", "working_dir", "current_working_directory"} {
        if v, ok := raw[k].(string); ok {
            v = strings.TrimSpace(v)
            if v != "" {
                return v
            }
        }
    }
    // nested git object
    if g, ok := raw["git"].(map[string]any); ok && g != nil {
        for _, k := range []string{"cwd", "root"} {
            if v, ok := g[k].(string); ok {
                v = strings.TrimSpace(v)
                if v != "" {
                    return v
                }
            }
        }
    }
    // environment_context with <cwd>... markup
    if s, ok := raw["environment_context"].(string); ok {
        s = strings.TrimSpace(s)
        if s != "" {
            // try exact <cwd>...</cwd>
            if cwd := between(s, "<cwd>", "</cwd>"); cwd != "" {
                return cwd
            }
            // fallback: find substring after <cwd> up to next <
            if i := strings.Index(strings.ToLower(s), "<cwd>"); i >= 0 {
                rest := s[i+5:] // len("<cwd>")
                if j := strings.Index(rest, "<"); j > 0 {
                    cwd := strings.TrimSpace(rest[:j])
                    if cwd != "" {
                        return cwd
                    }
                }
            }
        }
    }
    // content-based extraction: look for <cwd>... in content strings or parts
    if v, ok := raw["content"]; ok {
        switch c := v.(type) {
        case string:
            if cwd := findCWDInText(c); cwd != "" { return cwd }
        case []any:
            for _, el := range c {
                if s, ok := el.(string); ok {
                    if cwd := findCWDInText(s); cwd != "" { return cwd }
                    continue
                }
                if m, ok := el.(map[string]any); ok {
                    if t, _ := m["type"].(string); t == "text" || t == "input_text" || t == "output_text" {
                        if tx, _ := m["text"].(string); strings.TrimSpace(tx) != "" {
                            if cwd := findCWDInText(tx); cwd != "" { return cwd }
                        }
                        if cx, _ := m["content"].(string); strings.TrimSpace(cx) != "" {
                            if cwd := findCWDInText(cx); cwd != "" { return cwd }
                        }
                    }
                }
            }
        }
    }
    return ""
}

func between(s, a, b string) string {
    i := strings.Index(s, a)
    if i < 0 {
        return ""
    }
    i += len(a)
    j := strings.Index(s[i:], b)
    if j < 0 {
        return ""
    }
    return strings.TrimSpace(s[i : i+j])
}

func findCWDInText(s string) string {
    if s == "" { return "" }
    if cwd := between(s, "<cwd>", "</cwd>"); cwd != "" {
        return cwd
    }
    return ""
}
