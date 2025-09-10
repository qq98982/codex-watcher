package exporter

import (
    "encoding/json"
    "fmt"
    "io"
    "net/url"
    "sort"
    "strings"
    "time"

    "codex-watcher/internal/indexer"
)

// Filters control which messages are included in an export.
type Filters struct {
    IncludeRoles []string // e.g., user, assistant
    IncludeTypes []string // e.g., message, reasoning, function_call, function_call_output
    TextOnly     bool
    After        time.Time
    Before       time.Time
    MaxMessages  int // 0 = all
}

// WriteSession writes a single session export to w in the given format.
// Supported formats: jsonl, json, md, txt.
func WriteSession(w io.Writer, idx *indexer.Indexer, sessionID string, format string, f Filters) (int, error) {
    msgs := idx.Messages(sessionID, 0)
    // Obtain session metadata for title/cwd
    var sess indexer.Session
    for _, s := range idx.Sessions() { // small set; acceptable scan
        if s.ID == sessionID { sess = s; break }
    }

    // Filter and normalize
    type outMsg struct {
        ID        string    `json:"id,omitempty"`
        SessionID string    `json:"session_id"`
        Ts        time.Time `json:"ts,omitempty"`
        Role      string    `json:"role,omitempty"`
        Type      string    `json:"type,omitempty"`
        Model     string    `json:"model,omitempty"`
        Content   string    `json:"content,omitempty"`
        ToolName  string    `json:"tool_name,omitempty"`
        Source    string    `json:"source,omitempty"`
        LineNo    int       `json:"line_no,omitempty"`
    }

    allowedRole := func(r string) bool {
        if len(f.IncludeRoles) == 0 { return true }
        r = strings.ToLower(strings.TrimSpace(r))
        for _, v := range f.IncludeRoles { if r == strings.ToLower(strings.TrimSpace(v)) { return true } }
        return false
    }
    normalizeType := func(t string) string { if strings.TrimSpace(t) == "" { return "message" }; return strings.ToLower(t) }
    allowedType := func(t string) bool {
        t = normalizeType(t)
        if len(f.IncludeTypes) == 0 { return true }
        for _, v := range f.IncludeTypes { if t == strings.ToLower(strings.TrimSpace(v)) { return true } }
        return false
    }
    inDate := func(ts time.Time) bool {
        if ts.IsZero() { return true }
        if !f.After.IsZero() && ts.Before(f.After) { return false }
        if !f.Before.IsZero() && ts.After(f.Before) { return false }
        return true
    }

    filtered := make([]outMsg, 0, len(msgs))
    for _, m := range msgs {
        if !inDate(m.Ts) { continue }
        if !allowedRole(m.Role) { continue }
        if !allowedType(m.Type) { continue }
        if f.TextOnly {
            if strings.ToLower(m.Type) == "function_call" || strings.ToLower(m.Type) == "function_call_output" { continue }
            if strings.TrimSpace(m.Content) == "" && strings.ToLower(m.Type) != "reasoning" { continue }
        }
        om := outMsg{
            ID:        m.ID,
            SessionID: m.SessionID,
            Ts:        m.Ts,
            Role:      m.Role,
            Type:      normalizeType(m.Type),
            Model:     m.Model,
            Content:   m.Content,
            ToolName:  m.ToolName,
            Source:    m.Source,
            LineNo:    m.LineNo,
        }
        filtered = append(filtered, om)
        if f.MaxMessages > 0 && len(filtered) >= f.MaxMessages { break }
    }

    // Order by timestamp asc (older first), fallback to line number
    sort.SliceStable(filtered, func(i, j int) bool {
        if !filtered[i].Ts.Equal(filtered[j].Ts) {
            return filtered[i].Ts.Before(filtered[j].Ts)
        }
        if filtered[i].Source != filtered[j].Source { return filtered[i].Source < filtered[j].Source }
        return filtered[i].LineNo < filtered[j].LineNo
    })

    switch strings.ToLower(format) {
    case "jsonl":
        enc := json.NewEncoder(w)
        enc.SetEscapeHTML(false)
        for _, m := range filtered {
            if err := enc.Encode(m); err != nil { return 0, err }
        }
        return len(filtered), nil
    case "json":
        // stream as JSON array: [obj,obj,...]
        if _, err := io.WriteString(w, "["); err != nil { return 0, err }
        for i, m := range filtered {
            b, err := json.Marshal(m)
            if err != nil { return 0, err }
            if i > 0 { if _, err := io.WriteString(w, ","); err != nil { return 0, err } }
            if _, err := w.Write(b); err != nil { return 0, err }
        }
        if _, err := io.WriteString(w, "]"); err != nil { return 0, err }
        return len(filtered), nil
    case "md":
        // Header
        title := sess.Title
        if strings.TrimSpace(title) == "" { title = sessionID }
        if _, err := io.WriteString(w, "# "+escapeMD(title)+"\n\n"); err != nil { return 0, err }
        if strings.TrimSpace(sess.CWD) != "" {
            if _, err := io.WriteString(w, "CWD: "+escapeMD(sess.CWD)+"\n\n"); err != nil { return 0, err }
        }
        for _, m := range filtered {
            role := strings.ToUpper(strings.TrimSpace(m.Role))
            if role == "" { role = "MESSAGE" }
            // Reasoning hint
            if m.Type == "reasoning" { role = "ASSISTANT THINKING" }
            if _, err := io.WriteString(w, "### "+role+"\n\n"); err != nil { return 0, err }
            if strings.TrimSpace(m.Content) != "" {
                if _, err := io.WriteString(w, m.Content+"\n\n"); err != nil { return 0, err }
            }
        }
        return len(filtered), nil
    case "txt":
        title := sess.Title
        if strings.TrimSpace(title) == "" { title = sessionID }
        if _, err := io.WriteString(w, title+"\n"); err != nil { return 0, err }
        if strings.TrimSpace(sess.CWD) != "" {
            if _, err := io.WriteString(w, "CWD: "+sess.CWD+"\n\n"); err != nil { return 0, err }
        }
        for _, m := range filtered {
            role := strings.ToUpper(strings.TrimSpace(m.Role))
            if role == "" { role = "MESSAGE" }
            if m.Type == "reasoning" { role = "ASSISTANT THINKING" }
            if _, err := io.WriteString(w, "== "+role+" ==\n"); err != nil { return 0, err }
            if strings.TrimSpace(m.Content) != "" {
                if _, err := io.WriteString(w, m.Content+"\n\n"); err != nil { return 0, err }
            }
        }
        return len(filtered), nil
    default:
        return 0, fmt.Errorf("unsupported format: %s", format)
    }
}

func escapeMD(s string) string {
    // Minimal MD escaping for header lines
    r := s
    r = strings.ReplaceAll(r, "#", "\u0023")
    return r
}

// BuildAttachmentName builds a filename for Content-Disposition.
func BuildAttachmentName(sess indexer.Session, format string) string {
    base := strings.TrimSpace(sess.CWDBase)
    if base == "" { base = "session" }
    t := sess.FirstAt
    if t.IsZero() { t = time.Now() }
    ts := t.UTC().Format("20060102_1504")
    name := fmt.Sprintf("%s__%s__%s.%s", sanitize(base), sanitize(shorten(sess.Title, 40)), ts, strings.ToLower(format))
    return url.PathEscape(name)
}

func sanitize(s string) string {
    if s == "" { return "_" }
    s = strings.TrimSpace(s)
    s = strings.ReplaceAll(s, " ", "_")
    bad := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
    for _, b := range bad { s = strings.ReplaceAll(s, b, "_") }
    return s
}

func shorten(s string, n int) string {
    s = strings.TrimSpace(s)
    if s == "" { return "untitled" }
    if len(s) <= n { return s }
    if n <= 1 { return s[:1] }
    return s[:n-1] + "_"
}

