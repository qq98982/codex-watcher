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
    // Export policy toggles
    ExcludeShellCalls    bool // drop Tool: shell invocations
    ExcludeToolOutputs   bool // drop all function_call_output
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
        // Export policy controlled by filters
        typ := strings.ToLower(strings.TrimSpace(m.Type))
        if f.ExcludeToolOutputs && typ == "function_call_output" { continue }
        if f.ExcludeShellCalls && typ == "function_call" {
            tool := strings.ToLower(strings.TrimSpace(m.ToolName))
            if tool == "" {
                if n, ok := m.Raw["name"].(string); ok { tool = strings.ToLower(strings.TrimSpace(n)) }
            }
            if tool == "shell" { continue }
        }
        if f.TextOnly {
            if typ == "function_call" || typ == "function_call_output" { continue }
            if strings.TrimSpace(m.Content) == "" && typ != "reasoning" { continue }
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

// BuildDirAttachmentName produces a filename for directory exports.
func BuildDirAttachmentName(cwd string, mode string, format string) string {
    base := strings.TrimSpace(cwd)
    if base == "" { base = "export" }
    // reduce to cwd base component
    if i := strings.LastIndex(base, "/"); i >= 0 { base = base[i+1:] }
    ts := time.Now().UTC().Format("20060102_1504")
    name := fmt.Sprintf("%s__%s__%s.%s", sanitize(base), sanitize(mode), ts, strings.ToLower(format))
    return url.PathEscape(name)
}

// WriteByDirFlat writes a flattened export for a single directory (cwd prefix).
// Modes:
// - user: array of strings (user texts)
// - dialog: array of {role,text}
// - dialog_with_thinking: array of {role,text,type} where type in {message, reasoning}
// Formats: json, md
func WriteByDirFlat(w io.Writer, idx *indexer.Indexer, cwdPrefix string, mode string, format string, after, before time.Time) (int, error) {
    // Gather sessions under cwd prefix
    sessions := idx.Sessions()
    sel := make([]indexer.Session, 0)
    for _, s := range sessions {
        if cwdPrefix == "" || strings.HasPrefix(s.CWD, cwdPrefix) {
            sel = append(sel, s)
        }
    }
    // Sort sessions by FirstAt asc (old -> new)
    sort.SliceStable(sel, func(i, j int) bool {
        ai := sel[i].FirstAt
        aj := sel[j].FirstAt
        if ai.IsZero() && aj.IsZero() { return sel[i].ID < sel[j].ID }
        if ai.IsZero() { return true }
        if aj.IsZero() { return false }
        return ai.Before(aj)
    })

    // Helper filters
    inDate := func(ts time.Time) bool {
        if ts.IsZero() { return true }
        if !after.IsZero() && ts.Before(after) { return false }
        if !before.IsZero() && ts.After(before) { return false }
        return true
    }

    // Collect flattened result in memory (single pass) for both formats
    type dialogItem struct {
        Role string `json:"role"`
        Text string `json:"text"`
        Type string `json:"type,omitempty"` // message|reasoning for dialog_with_thinking
    }
    userTexts := make([]string, 0, 1024)
    dialogItems := make([]dialogItem, 0, 2048)
    includeThinking := strings.ToLower(mode) == "dialog_with_thinking"

    for _, s := range sel {
        msgs := idx.Messages(s.ID, 0)
        // Sort messages by ts asc
        sort.SliceStable(msgs, func(i, j int) bool {
            ti := msgs[i].Ts
            tj := msgs[j].Ts
            if !ti.Equal(tj) { return ti.Before(tj) }
            if msgs[i].Source != msgs[j].Source { return msgs[i].Source < msgs[j].Source }
            return msgs[i].LineNo < msgs[j].LineNo
        })
        for _, m := range msgs {
            if !inDate(m.Ts) { continue }
            typ := strings.ToLower(strings.TrimSpace(m.Type))
            role := strings.ToLower(strings.TrimSpace(m.Role))
            text := strings.TrimSpace(m.Content)
            if text == "" && typ != "reasoning" { continue }
            switch strings.ToLower(mode) {
            case "user":
                if role != "user" { continue }
                if typ == "function_call" || typ == "function_call_output" || typ == "reasoning" { continue }
                if text != "" { userTexts = append(userTexts, text) }
            case "dialog", "dialog_with_thinking":
                // exclude tools
                if typ == "function_call" || typ == "function_call_output" { continue }
                if typ == "reasoning" {
                    if includeThinking {
                        if text != "" { dialogItems = append(dialogItems, dialogItem{Role: "assistant", Text: text, Type: "reasoning"}) }
                    }
                    continue
                }
                // normal message text
                if role == "user" || role == "assistant" {
                    if text != "" {
                        di := dialogItem{Role: role, Text: text}
                        if includeThinking { di.Type = "message" }
                        dialogItems = append(dialogItems, di)
                    }
                }
            default:
                // default to dialog
                if typ == "function_call" || typ == "function_call_output" || typ == "reasoning" { continue }
                if role == "user" || role == "assistant" { if text != "" { dialogItems = append(dialogItems, dialogItem{Role: role, Text: text}) } }
            }
        }
    }

    // Emit output
    switch strings.ToLower(format) {
    case "json":
        if strings.ToLower(mode) == "user" {
            b, err := json.Marshal(userTexts)
            if err != nil { return 0, err }
            if _, err := w.Write(b); err != nil { return 0, err }
            return len(userTexts), nil
        }
        b, err := json.Marshal(dialogItems)
        if err != nil { return 0, err }
        if _, err := w.Write(b); err != nil { return 0, err }
        return len(dialogItems), nil
    case "md":
        count := 0
        if strings.ToLower(mode) == "user" {
            for _, t := range userTexts {
                if _, err := io.WriteString(w, t+"\n\n"); err != nil { return count, err }
                count++
            }
            return count, nil
        }
        // dialog variants
        for _, it := range dialogItems {
            head := "USER"
            if it.Role == "assistant" { head = "ASSISTANT" }
            if it.Type == "reasoning" { head = "THINKING" }
            if _, err := io.WriteString(w, "### "+head+"\n\n"); err != nil { return count, err }
            if _, err := io.WriteString(w, it.Text+"\n\n"); err != nil { return count, err }
            count++
        }
        return count, nil
    default:
        return 0, fmt.Errorf("unsupported format: %s", format)
    }
}

// WriteByDirAllMarkdown writes a markdown transcript for all messages (USER, TOOLS,
// ASSISTANT THINKING, ASSISTANT) under a cwd prefix, sessions ordered by FirstAt asc,
// messages ordered by timestamp asc.
func WriteByDirAllMarkdown(w io.Writer, idx *indexer.Indexer, cwdPrefix string, after, before time.Time, f Filters) (int, error) {
    sessions := idx.Sessions()
    sel := make([]indexer.Session, 0)
    for _, s := range sessions {
        if cwdPrefix == "" || strings.HasPrefix(s.CWD, cwdPrefix) {
            sel = append(sel, s)
        }
    }
    sort.SliceStable(sel, func(i, j int) bool {
        ai := sel[i].FirstAt
        aj := sel[j].FirstAt
        if ai.IsZero() && aj.IsZero() { return sel[i].ID < sel[j].ID }
        if ai.IsZero() { return true }
        if aj.IsZero() { return false }
        return ai.Before(aj)
    })
    inDate := func(ts time.Time) bool {
        if ts.IsZero() { return true }
        if !after.IsZero() && ts.Before(after) { return false }
        if !before.IsZero() && ts.After(before) { return false }
        return true
    }
    count := 0
    // Optional overall header
    if cwdPrefix != "" {
        _, _ = io.WriteString(w, "# Export for "+cwdPrefix+"\n\n")
    }
    for _, s := range sel {
        title := s.Title
        if strings.TrimSpace(title) == "" { title = s.ID }
        _, _ = io.WriteString(w, "## "+escapeMD(title)+"\n\n")
        if strings.TrimSpace(s.CWD) != "" {
            _, _ = io.WriteString(w, "CWD: "+escapeMD(s.CWD)+"\n\n")
        }
        msgs := idx.Messages(s.ID, 0)
        sort.SliceStable(msgs, func(i, j int) bool {
            ti := msgs[i].Ts
            tj := msgs[j].Ts
            if !ti.Equal(tj) { return ti.Before(tj) }
            if msgs[i].Source != msgs[j].Source { return msgs[i].Source < msgs[j].Source }
            return msgs[i].LineNo < msgs[j].LineNo
        })
        for _, m := range msgs {
            if !inDate(m.Ts) { continue }
            typ := strings.ToLower(strings.TrimSpace(m.Type))
            role := strings.ToLower(strings.TrimSpace(m.Role))
            text := strings.TrimSpace(m.Content)
            // Export policy controlled by filters
            if f.ExcludeToolOutputs && typ == "function_call_output" { continue }
            if f.ExcludeShellCalls && typ == "function_call" {
                tool := strings.ToLower(strings.TrimSpace(m.ToolName))
                if tool == "" {
                    if n, ok := m.Raw["name"].(string); ok { tool = strings.ToLower(strings.TrimSpace(n)) }
                }
                if tool == "shell" { continue }
            }
            switch typ {
            case "function_call":
                _, _ = io.WriteString(w, "### TOOLS\n\n")
                // name / command / arguments
                cmdLine, argsDump := parseFuncCall(m)
                if cmdLine != "" {
                    _, _ = io.WriteString(w, "~~~bash\n$ "+cmdLine+"\n~~~\n\n")
                } else if argsDump != "" {
                    _, _ = io.WriteString(w, "~~~json\n"+argsDump+"\n~~~\n\n")
                }
                count++
                continue
            case "function_call_output":
                _, _ = io.WriteString(w, "### TOOLS OUTPUT\n\n")
                out, errText := parseFuncOutput(m)
                if out != "" {
                    _, _ = io.WriteString(w, "~~~\n"+out+"\n~~~\n\n")
                    count++
                }
                if errText != "" {
                    _, _ = io.WriteString(w, "#### STDERR\n\n~~~\n"+errText+"\n~~~\n\n")
                }
                continue
            case "reasoning":
                if text != "" {
                    _, _ = io.WriteString(w, "### ASSISTANT THINKING\n\n"+text+"\n\n")
                    count++
                }
                continue
            }
            // Normal messages by role
            if role == "user" {
                if text != "" { _, _ = io.WriteString(w, "### USER\n\n"+text+"\n\n"); count++ }
                continue
            }
            if role == "assistant" {
                if text != "" { _, _ = io.WriteString(w, "### ASSISTANT\n\n"+text+"\n\n"); count++ }
                continue
            }
        }
    }
    return count, nil
}

func parseFuncCall(m *indexer.Message) (cmdLine string, argsDump string) {
    if m == nil || m.Raw == nil { return "", "" }
    args := m.Raw["arguments"]
    // Try JSON string first
    if s, ok := args.(string); ok {
        var obj map[string]any
        if json.Unmarshal([]byte(s), &obj) == nil {
            if cl := joinCommand(obj["command"]); cl != "" { return cl, "" }
            if b, err := json.MarshalIndent(obj, "", "  "); err == nil { return "", string(b) }
            return "", s
        }
        return "", s
    }
    if m2, ok := args.(map[string]any); ok {
        if cl := joinCommand(m2["command"]); cl != "" { return cl, "" }
        if b, err := json.MarshalIndent(m2, "", "  "); err == nil { return "", string(b) }
    }
    return "", ""
}

func joinCommand(v any) string {
    arr, ok := v.([]any)
    if !ok || len(arr) == 0 { return "" }
    parts := make([]string, 0, len(arr))
    for _, el := range arr {
        if s, ok := el.(string); ok { parts = append(parts, s) }
    }
    return strings.Join(parts, " ")
}

func parseFuncOutput(m *indexer.Message) (stdout string, stderr string) {
    if m == nil || m.Raw == nil { return "", "" }
    v := m.Raw["output"]
    switch t := v.(type) {
    case string:
        var obj map[string]any
        if json.Unmarshal([]byte(t), &obj) == nil {
            if s, _ := obj["output"].(string); s != "" { stdout = s }
            if s, _ := obj["stdout"].(string); s != "" { stdout = s }
            if s, _ := obj["stderr"].(string); s != "" { stderr = s }
            if stdout != "" || stderr != "" { return stdout, stderr }
        }
        return t, ""
    case map[string]any:
        if s, _ := t["output"].(string); s != "" { stdout = s }
        if s, _ := t["stdout"].(string); s != "" { stdout = s }
        if s, _ := t["stderr"].(string); s != "" { stderr = s }
    }
    return stdout, stderr
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
