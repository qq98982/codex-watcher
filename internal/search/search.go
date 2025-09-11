package search

import (
    "regexp"
    "sort"
    "strings"
    "time"

    "codex-watcher/internal/indexer"
    "encoding/json"
)

// Package search provides a minimal zero-dependency, in-memory search engine
// over the indexer's messages. It implements a basic Google-style query
// parser with AND/OR, phrase, exclude, field filters, regex, and simple
// wildcard handling. This is a pragmatic baseline that can be upgraded to
// SQLite FTS-backed search later without changing the API.

// Scope controls which textual fields are considered for term/phrase/regex matching.
type Scope int

const (
    ScopeContent Scope = iota // content-only (default)
    ScopeTools                // tool command + outputs only
    ScopeAll                  // all textual fields
)

// Query describes a parsed search.
// It is represented as a disjunction (OR) of conjunctions (AND) of clauses.
// Each clause may be a text match (term, phrase, regex, prefix/wildcard)
// or a field filter applied to metadata (role/type/model/cwd/cwd_base).
type Query struct {
    // OR-groups of AND-clauses
    Groups [][]Clause

    // Scope for text matching
    Scope Scope
}

// Clause represents one atomic condition.
type Clause struct {
    // Negative indicates exclusion (-term)
    Negative bool

    // Fielded metadata filters
    Field string // one of: role, type, model, cwd, cwd_base, in
    Value string // raw value for field filters or text clauses

    // Text matching
    Kind   ClauseKind
    Regex  *regexp.Regexp // for KindRegex or wildcard converted to regex
}

type ClauseKind int

const (
    KindUnknown ClauseKind = iota
    KindTerm                 // case-insensitive substring (AND default)
    KindPhrase               // quoted phrase
    KindPrefix               // foo*
    KindRegex                // /re/
    KindField                // role:assistant, etc.
)

// Result is one matched message with minimal context for Phase 1.
type Result struct {
    SessionID string          `json:"session_id"`
    MessageID string          `json:"message_id,omitempty"`
    Role      string          `json:"role,omitempty"`
    Type      string          `json:"type,omitempty"`
    Model     string          `json:"model,omitempty"`
    Source    string          `json:"source,omitempty"`
    LineNo    int             `json:"line_no,omitempty"`
    Ts        time.Time       `json:"ts,omitempty"`
    Field     string          `json:"field,omitempty"` // which field matched: content|tool_cmd|stdout|stderr
    Content   string          `json:"content,omitempty"`
}

// Response shapes the API output for /api/search.
type Response struct {
    TookMS    int       `json:"took_ms"`
    Truncated bool      `json:"truncated"`
    Total     int       `json:"total"` // count before offset/limit (best-effort)
    Hits      []Result  `json:"hits"`
}

// Parse converts a raw query string and optional scope string into a Query.
func Parse(raw string, scopeStr string) Query {
    scope := ScopeContent
    switch strings.ToLower(strings.TrimSpace(scopeStr)) {
    case "tools":
        scope = ScopeTools
    case "all":
        scope = ScopeAll
    }

    tokens := tokenize(raw)
    // Detect in: scope inside the query and let it override explicit param.
    filtered := make([]token, 0, len(tokens))
    for _, t := range tokens {
        if t.isField && t.field == "in" {
            switch strings.ToLower(strings.TrimSpace(stripQuotes(t.raw))) {
            case "tools":
                scope = ScopeTools
            case "all":
                scope = ScopeAll
            default:
                scope = ScopeContent
            }
            // drop this token from parsed clauses
            continue
        }
        filtered = append(filtered, t)
    }
    groups := parseToDNF(filtered)
    return Query{Groups: groups, Scope: scope}
}

// Tunables (can be adjusted by callers, e.g., via flags/env in main)
var (
    MaxReturn = 200
    Budget    = 350 * time.Millisecond
)

// Exec evaluates the Query against the in-memory index and returns results.
// limit is the number of rows to return; offset skips that many initial hits.
// A soft time budget is enforced to avoid long scans on large datasets.
func Exec(idx *indexer.Indexer, q Query, limit, offset int) Response {
    start := time.Now()
    if limit <= 0 {
        limit = 50
    }
    if offset < 0 {
        offset = 0
    }
    // soft caps
    if limit > MaxReturn {
        limit = MaxReturn
    }
    budget := Budget // conservative baseline

    // sessions lookup for CWD filters
    sessions := idx.Sessions()
    sessByID := make(map[string]indexer.Session, len(sessions))
    for _, s := range sessions {
        sessByID[s.ID] = s
    }

    // collect in deterministic order: by session last_at desc (already sorted),
    // then by message line number ascending (natural ingestion order).
    results := make([]Result, 0, limit)
    total := 0
    truncated := false

    // Decide which textual fields are searched under current scope.
    // For each message we'll build target strings lazily.
    for _, s := range sessions {
        msgs := idx.Messages(s.ID, 0)
        for _, m := range msgs {
            // Apply field filters first (role/type/model/cwd/cwd_base)
            if !matchesFieldFilters(q, m, sessByID[m.SessionID]) {
                continue
            }
            // Evaluate text groups
            matched, field := matchesTextGroups(q, m)
            if !matched {
                continue
            }
            total++
            if total <= offset {
                continue
            }
            // Append result
            res := Result{
                SessionID: m.SessionID,
                MessageID: m.ID,
                Role:      m.Role,
                Type:      m.Type,
                Model:     m.Model,
                Source:    m.Source,
                LineNo:    m.LineNo,
                Ts:        m.Ts,
                Field:     field,
            }
            // Include a short text preview for Phase 1 (no mark-up)
            switch field {
            case "tool_cmd":
                res.Content = strings.TrimSpace(extractToolCmd(m))
            case "stdout":
                res.Content = strings.TrimSpace(extractToolOut(m, true))
            case "stderr":
                res.Content = strings.TrimSpace(extractToolOut(m, false))
            default:
                res.Content = strings.TrimSpace(m.Content)
            }
            if len(res.Content) > 240 {
                res.Content = res.Content[:240]
            }
            results = append(results, res)
            if len(results) >= limit {
                // still compute total within budget for better UX
                if time.Since(start) > budget {
                    truncated = true
                    break
                }
            }
            if time.Since(start) > budget {
                truncated = true
                break
            }
        }
        if truncated || len(results) >= limit && time.Since(start) > budget {
            break
        }
    }

    // Best-effort stable ordering: by Ts descending when available, else by Source/LineNo.
    sort.Slice(results, func(i, j int) bool {
        if !results[i].Ts.Equal(results[j].Ts) {
            return results[i].Ts.After(results[j].Ts)
        }
        if results[i].Source != results[j].Source {
            return results[i].Source < results[j].Source
        }
        return results[i].LineNo < results[j].LineNo
    })

    took := int(time.Since(start).Milliseconds())
    return Response{TookMS: took, Truncated: truncated, Total: total, Hits: results}
}

// matchesFieldFilters applies only Field clauses to a message and its session.
func matchesFieldFilters(q Query, m *indexer.Message, s indexer.Session) bool {
    if len(q.Groups) == 0 {
        return true
    }
    // All field filters across all groups must be satisfied for a candidate,
    // because OR applies only to textual predicates. This keeps behavior
    // intuitive for typical queries like role:assistant foo OR bar.
    // Collect allow/deny lists and evaluate.
    allow := make(map[string][]Clause)
    deny := make(map[string][]Clause)
    for _, g := range q.Groups {
        for _, c := range g {
            if c.Kind == KindField {
                if c.Negative {
                    deny[c.Field] = append(deny[c.Field], c)
                } else {
                    allow[c.Field] = append(allow[c.Field], c)
                }
            }
        }
    }
    // Helper to test one field
    fieldMatches := func(field, got string) bool {
        // if any allow exists for this field, require that one matches
        if arr, ok := allow[field]; ok && len(arr) > 0 {
            okMatch := false
            for _, c := range arr {
                if fieldValueMatches(field, got, c.Value) {
                    okMatch = true
                    break
                }
            }
            if !okMatch {
                return false
            }
        }
        // no denial for this field or none match denial
        if arr, ok := deny[field]; ok && len(arr) > 0 {
            for _, c := range arr {
                if fieldValueMatches(field, got, c.Value) {
                    return false
                }
            }
        }
        return true
    }

    // role, type, model from message; cwd/cwd_base from session
    if !fieldMatches("role", strings.ToLower(m.Role)) { return false }
    if !fieldMatches("type", strings.ToLower(m.Type)) { return false }
    if !fieldMatches("model", strings.ToLower(m.Model)) { return false }
    if !fieldMatches("cwd", strings.ToLower(s.CWD)) { return false }
    if !fieldMatches("cwd_base", strings.ToLower(s.CWDBase)) { return false }
    return true
}

func fieldValueMatches(field, got, want string) bool {
    got = strings.ToLower(strings.TrimSpace(got))
    want = strings.ToLower(strings.TrimSpace(want))
    if want == "" {
        return true
    }
    switch field {
    case "cwd":
        // substring to support subdirectories
        return strings.Contains(got, want)
    default:
        return got == want
    }
}

// matchesTextGroups evaluates the OR-of-AND groups for textual clauses only.
// Returns whether it matched and the field that matched (best-effort).
func matchesTextGroups(q Query, m *indexer.Message) (bool, string) {
    // Precompute target strings depending on scope.
    content := strings.ToLower(m.Content)
    toolCmd := strings.ToLower(extractToolCmd(m))
    outStd := strings.ToLower(extractToolOut(m, true))
    outErr := strings.ToLower(extractToolOut(m, false))

    // Helper to test a clause against a specific string
    testClause := func(c Clause, text string) bool {
        if c.Kind == KindField {
            // handled elsewhere
            return true
        }
        switch c.Kind {
        case KindRegex:
            if c.Regex == nil { return false }
            return c.Regex.MatchString(text)
        case KindPhrase:
            return strings.Contains(text, strings.ToLower(c.Value))
        case KindPrefix:
            // treat as substring with word boundary preference if possible
            pref := strings.ToLower(strings.TrimSuffix(c.Value, "*"))
            if pref == "" { return true }
            // fast path: substring
            if strings.Contains(text, pref) { return true }
            return false
        case KindTerm:
            v := strings.ToLower(c.Value)
            if v == "" { return true }
            return strings.Contains(text, v)
        default:
            return false
        }
    }

    anyGroup := false
    whichField := ""
    for _, group := range q.Groups {
        // Each group must satisfy all positive clauses and none of the negatives.
        // Evaluate across the selected scope and accept if any field within scope satisfies.
        groupOK := true
        fieldHit := ""

        // We evaluate positives and negatives across candidate fields.
        // For AND semantics, a positive clause must match in at least one field in-scope.
        // For negatives, if it matches in any in-scope field, the group fails.
        for _, c := range group {
            if c.Kind == KindField { continue } // handled in field filters

            matched := false
            // per-scope checks
            checkContent := func() bool { return testClause(c, content) }
            checkTools := func() (bool, string) {
                if testClause(c, toolCmd) { return true, "tool_cmd" }
                if testClause(c, outStd) { return true, "stdout" }
                if testClause(c, outErr) { return true, "stderr" }
                return false, ""
            }

            switch q.Scope {
            case ScopeContent:
                matched = checkContent()
                if matched && fieldHit == "" { fieldHit = "content" }
            case ScopeTools:
                if ok, f := checkTools(); ok { matched = true; if fieldHit == "" { fieldHit = f } }
            case ScopeAll:
                if checkContent() { matched = true; if fieldHit == "" { fieldHit = "content" } }
                if !matched {
                    if ok, f := checkTools(); ok { matched = true; if fieldHit == "" { fieldHit = f } }
                }
            }

            if c.Negative {
                if matched { groupOK = false; break }
            } else {
                if !matched { groupOK = false; break }
            }
        }

        if groupOK {
            anyGroup = true
            if whichField == "" {
                whichField = fieldHit
            }
            break
        }
    }
    if !anyGroup { return false, "" }
    if whichField == "" {
        // default
        switch q.Scope {
        case ScopeTools:
            whichField = "tool_cmd"
        default:
            whichField = "content"
        }
    }
    return true, whichField
}

// tokenize splits the raw query into tokens, respecting quotes and /regex/.
type token struct {
    raw      string
    negative bool
    isOR     bool
    isField  bool
    field    string
}

func tokenize(s string) []token {
    out := []token{}
    s = strings.TrimSpace(s)
    if s == "" { return out }
    i := 0
    for i < len(s) {
        // skip spaces
        if isSpace(s[i]) { i++; continue }
        neg := false
        if s[i] == '-' { neg = true; i++; for i < len(s) && isSpace(s[i]) { i++ } }
        if i >= len(s) { break }

        // phrase
        if s[i] == '"' {
            j := i + 1
            for j < len(s) && s[j] != '"' { j++ }
            val := s[i+1 : min(j, len(s))]
            out = append(out, token{raw: "\"" + val + "\"", negative: neg})
            i = min(j+1, len(s))
            continue
        }
        // regex /.../flags
        if s[i] == '/' {
            j := i + 1
            for j < len(s) && s[j] != '/' { j++ }
            val := s[i : min(j+1, len(s))]
            // flags
            k := j + 1
            for k < len(s) && ((s[k] >= 'a' && s[k] <= 'z') || (s[k] >= 'A' && s[k] <= 'Z')) { k++ }
            val = s[i:min(k, len(s))]
            out = append(out, token{raw: val, negative: neg})
            i = min(k, len(s))
            continue
        }
        // general token up to space
        j := i
        for j < len(s) && !isSpace(s[j]) { j++ }
        raw := s[i:j]
        // OR operator
        if raw == "OR" {
            out = append(out, token{isOR: true})
            i = j
            continue
        }
        // field:value
        if k := strings.IndexByte(raw, ':'); k > 0 {
            field := strings.ToLower(raw[:k])
            if isKnownField(field) {
                val := raw[k+1:]
                out = append(out, token{raw: val, negative: neg, isField: true, field: field})
                i = j
                continue
            }
        }
        out = append(out, token{raw: raw, negative: neg})
        i = j
    }
    return out
}

func isKnownField(f string) bool {
    switch f {
    case "role", "type", "model", "cwd", "cwd_base", "in":
        return true
    default:
        return false
    }
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// parseToDNF converts tokens into OR groups of AND clauses.
func parseToDNF(toks []token) [][]Clause {
    groups := [][]Clause{}
    cur := []Clause{}
    flush := func() {
        if len(cur) > 0 {
            groups = append(groups, cur)
            cur = []Clause{}
        }
    }
    for _, t := range toks {
        if t.isOR {
            flush()
            continue
        }
        if t.isField {
            // special-case in: scope; include as field clause to be handled by Parse caller
            if t.field == "in" {
                // Represent as field clause so fieldFilters can ignore it; scope already handled separately.
                cur = append(cur, Clause{Kind: KindField, Field: "in", Value: strings.ToLower(t.raw), Negative: t.negative})
                continue
            }
            cur = append(cur, Clause{Kind: KindField, Field: t.field, Value: stripQuotes(t.raw), Negative: t.negative})
            continue
        }
        raw := t.raw
        // regex
        if strings.HasPrefix(raw, "/") && len(raw) >= 2 {
            // find last '/'
            // raw may include flags like /pattern/i
            pattern := raw
            flags := ""
            if n := strings.LastIndex(raw, "/"); n > 0 {
                pattern = raw[1:n]
                flags = raw[n+1:]
            }
            // normalize typical curl-escaped backslashes from docs/examples:
            // users often write \\s which should be interpreted as \s in the pattern.
            pattern = strings.ReplaceAll(pattern, "\\\\", "\\")
            // translate common PCRE shorthands to Go RE2 equivalents
            pattern = normalizePCREtoRE2(pattern)
            // translate flags; only 'i' supported explicitly.
            if strings.Contains(flags, "i") {
                pattern = "(?i)" + pattern
            }
            re := safeCompile(pattern)
            cur = append(cur, Clause{Kind: KindRegex, Regex: re, Negative: t.negative})
            continue
        }
        // phrase
        if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") && len(raw) >= 2 {
            val := stripQuotes(raw)
            cur = append(cur, Clause{Kind: KindPhrase, Value: val, Negative: t.negative})
            continue
        }
        // wildcard
        if strings.Contains(raw, "*") {
            if strings.Count(raw, "*") == 1 && strings.HasSuffix(raw, "*") {
                cur = append(cur, Clause{Kind: KindPrefix, Value: strings.TrimSuffix(raw, "*"), Negative: t.negative})
            } else {
                // convert to regex: escape specials except '*', then replace '*' with '.*'
                esc := regexp.QuoteMeta(raw)
                esc = strings.ReplaceAll(esc, "\\*", ".*")
                re := safeCompile("(?i)" + esc)
                cur = append(cur, Clause{Kind: KindRegex, Regex: re, Negative: t.negative})
            }
            continue
        }
        // bare term
        cur = append(cur, Clause{Kind: KindTerm, Value: raw, Negative: t.negative})
    }
    flush()
    if len(groups) == 0 {
        groups = [][]Clause{{}}
    }
    return groups
}

func stripQuotes(s string) string {
    if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
        return s[1:len(s)-1]
    }
    return s
}

func safeCompile(pat string) *regexp.Regexp {
    re, err := regexp.Compile(pat)
    if err != nil { return nil }
    return re
}

// normalizePCREtoRE2 converts common PCRE-like shorthands into Go RE2 equivalents.
func normalizePCREtoRE2(p string) string {
    // Handle uppercase first to avoid double replacement
    p = strings.ReplaceAll(p, "\\S", "[^[:space:]]")
    p = strings.ReplaceAll(p, "\\D", "[^0-9]")
    p = strings.ReplaceAll(p, "\\W", "[^A-Za-z0-9_]")
    // Lowercase
    p = strings.ReplaceAll(p, "\\s", "[[:space:]]")
    p = strings.ReplaceAll(p, "\\d", "[0-9]")
    p = strings.ReplaceAll(p, "\\w", "[A-Za-z0-9_]")
    return p
}

// Tool helpers — mirror logic used in UI rendering to extract tool fields.
func extractToolCmd(m *indexer.Message) string {
    if m == nil || m.Raw == nil { return "" }
    if strings.ToLower(m.Type) != "function_call" { return "" }
    args := m.Raw["arguments"]
    switch v := args.(type) {
    case string:
        // try parse JSON
        var obj map[string]any
        if err := json.Unmarshal([]byte(v), &obj); err == nil {
            if cmd, ok := obj["command"].([]any); ok {
                parts := make([]string, 0, len(cmd))
                for _, el := range cmd {
                    if s, ok := el.(string); ok { parts = append(parts, s) }
                }
                if len(parts) > 0 { return strings.Join(parts, " ") }
            }
        }
        return v
    case map[string]any:
        if cmd, ok := v["command"].([]any); ok {
            parts := make([]string, 0, len(cmd))
            for _, el := range cmd {
                if s, ok := el.(string); ok { parts = append(parts, s) }
            }
            return strings.Join(parts, " ")
        }
    }
    return ""
}

func extractToolOut(m *indexer.Message, stdout bool) string {
    if m == nil || m.Raw == nil { return "" }
    if strings.ToLower(m.Type) != "function_call_output" { return "" }
    out := m.Raw["output"]
    if stdout {
        // prefer .output
        if s, ok := out.(string); ok {
            // maybe JSON
            var obj map[string]any
            if err := json.Unmarshal([]byte(s), &obj); err == nil {
                if v, ok := obj["output"].(string); ok { return v }
                if v, ok := obj["stdout"].(string); ok { return v }
            }
            return s
        }
        if m, ok := out.(map[string]any); ok {
            if v, ok := m["output"].(string); ok { return v }
            if v, ok := m["stdout"].(string); ok { return v }
        }
    } else {
        if s, ok := out.(string); ok {
            var obj map[string]any
            if err := json.Unmarshal([]byte(s), &obj); err == nil {
                if v, ok := obj["stderr"].(string); ok { return v }
            }
            // no stderr key in string JSON; nothing
        }
        if m, ok := out.(map[string]any); ok {
            if v, ok := m["stderr"].(string); ok { return v }
        }
    }
    return ""
}

func min(a, b int) int { if a < b { return a } ; return b }
