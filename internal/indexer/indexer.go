package indexer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Message represents a single JSONL event/message extracted from Codex logs.
type Message struct {
	ID        string         `json:"id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	Ts        time.Time      `json:"ts,omitempty"`
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	Model     string         `json:"model,omitempty"`
	Type      string         `json:"type,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
	Source    string         `json:"source"`   // relative file path
	Provider  string         `json:"provider"` // codex|claude
	LineNo    int            `json:"line_no"`
}

// Session aggregates messages by session id or file.
type Session struct {
	ID           string         `json:"id"`
	Title        string         `json:"title,omitempty"`
	FirstAt      time.Time      `json:"first_at,omitempty"`
	LastAt       time.Time      `json:"last_at,omitempty"`
	FileModAt    time.Time      `json:"file_mod_at,omitempty"`
	MessageCount int            `json:"message_count"`
	TextCount    int            `json:"text_count"`
	CWD          string         `json:"cwd,omitempty"`
	CWDBase      string         `json:"cwd_base,omitempty"`
	Models       map[string]int `json:"models,omitempty"`
	Roles        map[string]int `json:"roles,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Sources      []string       `json:"sources,omitempty"`
	Provider     string         `json:"provider,omitempty"` // codex|claude
	Project      string         `json:"project,omitempty"`  // for claude
}

// Indexer tails JSONL files under ~/.codex and builds an in-memory index.
type Indexer struct {
	codexDir  string
	claudeDir string

	mu        sync.RWMutex
	sessions  map[string]*Session
	messages  map[string][]*Message // by session id
	stats     Stats
	positions map[string]int64 // file path -> byte offset (tail)
	lineNos   map[string]int   // file path -> last line number processed

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
	BadLines     int `json:"bad_lines,omitempty"`
	FilesScanned int `json:"files_scanned,omitempty"`
	LastScanMs   int `json:"last_scan_ms,omitempty"`
}

func New(codexDir, claudeDir string) *Indexer {
	return &Indexer{
		codexDir:     codexDir,
		claudeDir:    claudeDir,
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
	// Codex: sessions/*.jsonl
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
			_ = x.tailFile("codex", "", id, path)
			files++
		}
		return nil
	})
	// Claude: <project>/*.jsonl under claudeDir
	if strings.TrimSpace(x.claudeDir) != "" {
		entries, _ := os.ReadDir(x.claudeDir)
		for _, ent := range entries {
			if !ent.IsDir() {
				continue
			}
			project := ent.Name()
			projDir := filepath.Join(x.claudeDir, project)
			_ = filepath.WalkDir(projDir, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d == nil || d.IsDir() {
					return nil
				}
				if strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
					sid := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
					// namespace with provider to avoid collisions
					namespaced := "claude:" + project + ":" + sid
					_ = x.tailFile("claude", project, namespaced, path)
					files++
				}
				return nil
			})
		}
	}
	// update observability metrics
	x.mu.Lock()
	x.stats.FilesScanned = files
	x.stats.LastScanMs = int(time.Since(start).Milliseconds())
	x.mu.Unlock()
	return nil
}

func (x *Indexer) tailFile(provider, project, sessionID, path string) error {
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
			x.ingestLine(provider, project, sessionID, path, string(line))
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
			s = &Session{ID: sessionID, Models: map[string]int{}, Roles: map[string]int{}, Provider: provider, Project: project}
			x.sessions[sessionID] = s
		}
		if modTime.After(s.FileModAt) {
			s.FileModAt = modTime
		}
		x.mu.Unlock()
		// Load custom metadata (title, etc.) after session is created
		x.loadSessionMetadata(sessionID, provider, project)
	}
	return nil
}

func (x *Indexer) ingestLine(provider, project, sessionID, path, line string) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &raw); err != nil {
		// ignore bad line but record count
		x.mu.Lock()
		x.stats.BadLines++
		x.mu.Unlock()
		return
	}

	if shouldSkipEventMessage(raw) {
		return
	}

	// attempt to map common fields
	msg := &Message{
		ID:        stringOr(raw["id"]),
		SessionID: sessionID,
		Role:      stringOr(raw["role"]),
		Content:   extractText(raw),
		Model:     stringOr(raw["model"]),
		Type:      stringOr(raw["type"]),
		ToolName:  stringOr(raw["tool_name"]),
		Raw:       raw,
		Source:    chooseRelSource(path, provider, x.codexDir, x.claudeDir),
		Provider:  provider,
	}

	if ts, ok := parseTime(raw["timestamp"], raw["ts"], raw["created_at"]); ok {
		msg.Ts = ts
	}

	// Claude-specific extraction: nested message fields
	if provider == "claude" {
		if mobj, ok := raw["message"].(map[string]any); ok && mobj != nil {
			if msg.Role == "" {
				msg.Role = stringOr(mobj["role"])
			}
			if msg.Model == "" {
				msg.Model = stringOr(mobj["model"])
			}
			// Extract content text ("text" parts) and thinking ("thinking" parts)
			textOut, thinkOut := extractClaudeSegments(mobj)
			if strings.TrimSpace(textOut) != "" {
				msg.Content = textOut
			}
			if strings.TrimSpace(thinkOut) != "" {
				msg.Thinking = thinkOut
			}
		}
		// For Claude: Use filename as session ID (ignore internal sessionId)
		// This ensures resumed sessions in the same file are treated as one session
		// msg.SessionID is already set to sessionID (file-based) at the top

		// For summaries, always update title (summaries are more accurate than first message)
		// Only custom titles from .meta.json will override this later
		if strings.ToLower(msg.Type) == "summary" {
			if s := stringOr(raw["summary"]); s != "" {
				x.mu.Lock()
				if sess := x.sessions[sessionID]; sess != nil {
					sess.Title = trimTitle(s)
				}
				x.mu.Unlock()
			}
		}
	} else {
		// Codex: if raw provides a session_id, prefer it
		if sid := firstNonEmpty(stringOr(raw["session_id"]), ""); sid != "" {
			msg.SessionID = sid
		}
	}

	x.mu.Lock()

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
	isNewSession := (s == nil)
	if s == nil {
		s = &Session{ID: sID, Models: map[string]int{}, Roles: map[string]int{}, Provider: provider, Project: project}
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
	// Priority: custom title (from .meta.json) > Claude summary > explicit title > first message
	// Note: custom titles are loaded via loadSessionMetadata and have highest priority
	if s.Title == "" {
		if t := normalizeTitleCandidate(stringOr(raw["title"]), s); t != "" {
			s.Title = t
		} else if t := normalizeTitleCandidate(msg.Content, s); t != "" {
			s.Title = t
		} else if fallback := fallbackTitleFromSession(s); fallback != "" {
			s.Title = trimTitle(fallback)
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

	x.mu.Unlock()

	// Load custom metadata for newly created sessions after releasing the lock
	if isNewSession {
		x.loadSessionMetadata(sID, provider, project)
	}
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
	if raw == nil {
		return
	}
	b, _ := json.Marshal(raw)
	// mimic a file path for line numbers and source
	path := "/tmp/.codex/sessions/" + sessionID + ".jsonl"
	x.ingestLine("codex", "", sessionID, path, string(b))
}

// DeleteSession removes a session and all its messages from memory and deletes the source file.
func (x *Indexer) DeleteSession(sessionID string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	sess, exists := x.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Determine file path based on provider
	var filePath string
	if sess.Provider == "claude" {
		// Parse "claude:<project>:<sid>"
		parts := strings.SplitN(sessionID, ":", 3)
		if len(parts) >= 3 {
			project := parts[1]
			sid := parts[2]
			filePath = filepath.Join(x.claudeDir, project, sid+".jsonl")
		} else {
			return fmt.Errorf("invalid claude session ID format: %s", sessionID)
		}
	} else {
		// Codex: sessions/<sessionID>.jsonl
		filePath = filepath.Join(x.codexDir, "sessions", sessionID+".jsonl")
	}

	// Delete the file
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file %s: %w", filePath, err)
	}

	// Remove from memory
	delete(x.sessions, sessionID)
	delete(x.messages, sessionID)
	delete(x.positions, filePath)
	delete(x.lineNos, filePath)

	// Update stats
	x.stats.TotalSessions = len(x.sessions)
	return nil
}

// DeleteMessage removes a single message from a session in memory and rewrites the JSONL file.
func (x *Indexer) DeleteMessage(sessionID, messageID string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	sess, exists := x.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	msgs := x.messages[sessionID]
	if len(msgs) == 0 {
		return fmt.Errorf("no messages in session: %s", sessionID)
	}

	// Find the message to delete
	msgIndex := -1
	for i, msg := range msgs {
		if msg.ID == messageID {
			msgIndex = i
			break
		}
	}
	if msgIndex == -1 {
		return fmt.Errorf("message not found: %s", messageID)
	}

	// Determine file path
	var filePath string
	if sess.Provider == "claude" {
		parts := strings.SplitN(sessionID, ":", 3)
		if len(parts) >= 3 {
			project := parts[1]
			sid := parts[2]
			filePath = filepath.Join(x.claudeDir, project, sid+".jsonl")
		} else {
			return fmt.Errorf("invalid claude session ID format: %s", sessionID)
		}
	} else {
		filePath = filepath.Join(x.codexDir, "sessions", sessionID+".jsonl")
	}

	// Read all lines from the file
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	targetLineNo := msgs[msgIndex].LineNo
	for scanner.Scan() {
		lineNum++
		if lineNum != targetLineNo {
			lines = append(lines, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	f.Close()

	// Write back the filtered lines
	tmpPath := filePath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	writer := bufio.NewWriter(tmpFile)
	for _, line := range lines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("failed to write temp file: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to flush temp file: %w", err)
	}
	tmpFile.Close()

	// Replace original file with temp file
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to replace file: %w", err)
	}

	// Remove from memory
	x.messages[sessionID] = append(msgs[:msgIndex], msgs[msgIndex+1:]...)

	// Update session stats
	sess.MessageCount = len(x.messages[sessionID])
	if msgs[msgIndex].Content != "" {
		sess.TextCount--
	}

	// Reset file position to force re-reading
	x.positions[filePath] = 0
	x.lineNos[filePath] = 0

	return nil
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

// chooseRelSource picks the correct root for relative path computation.
func chooseRelSource(path, provider, codexRoot, claudeRoot string) string {
	switch provider {
	case "claude":
		if strings.TrimSpace(claudeRoot) != "" {
			if r, err := filepath.Rel(claudeRoot, path); err == nil {
				return r
			}
		}
	default:
		if strings.TrimSpace(codexRoot) != "" {
			if r, err := filepath.Rel(codexRoot, path); err == nil {
				return r
			}
		}
	}
	return path
}

// extractClaudeSegments returns (text, thinking) extracted from message.content (string or array).
func extractClaudeSegments(messageObj map[string]any) (string, string) {
	var textParts []string
	var thinkParts []string
	if messageObj == nil {
		return "", ""
	}

	// Handle content as string (user messages)
	if str, ok := messageObj["content"].(string); ok && strings.TrimSpace(str) != "" {
		return str, ""
	}

	// Handle content as array (assistant messages with structured content)
	arr, _ := messageObj["content"].([]any)
	for _, el := range arr {
		m, ok := el.(map[string]any)
		if !ok || m == nil {
			continue
		}
		t, _ := m["type"].(string)
		switch strings.ToLower(t) {
		case "text", "input_text", "output_text":
			if s, _ := m["text"].(string); strings.TrimSpace(s) != "" {
				textParts = append(textParts, s)
			}
			if s, _ := m["content"].(string); strings.TrimSpace(s) != "" {
				textParts = append(textParts, s)
			}
		case "thinking":
			if s, _ := m["thinking"].(string); strings.TrimSpace(s) != "" {
				thinkParts = append(thinkParts, s)
			}
		}
	}
	return strings.Join(textParts, "\n\n"), strings.Join(thinkParts, "\n\n")
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func trimTitle(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	// Use runes to avoid splitting multi-byte UTF-8 characters
	runes := []rune(s)
	if len(runes) <= 80 {
		return s
	}
	return string(runes[:80]) + "…"
}

func normalizeTitleCandidate(candidate string, sess *Session) string {
	cand := strings.TrimSpace(candidate)
	if cand == "" {
		return ""
	}
	if sess != nil && strings.EqualFold(strings.TrimSpace(sess.ID), cand) {
		return ""
	}
	if looksLikeEnvironmentContext(cand) || looksLikeGeneratedIdentifier(cand) {
		return ""
	}
	return trimTitle(cand)
}

// looksLikeEnvironmentContext detects the XML-ish metadata blob emitted by Codex.
func looksLikeEnvironmentContext(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "<environment_context") {
		return true
	}
	markers := []string{
		"<cwd>",
		"</cwd>",
		"<approval_pol",
		"<sandbox_mode",
		"<network_access",
		"<shell>",
	}
	hits := 0
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			hits++
		}
	}
	return hits >= 2
}

func fallbackTitleFromSession(s *Session) string {
	if s == nil {
		return ""
	}
	if base := strings.TrimSpace(s.CWDBase); base != "" {
		return base
	}
	if cwd := strings.TrimSpace(s.CWD); cwd != "" {
		return cwd
	}
	id := strings.TrimSpace(s.ID)
	if id != "" && !looksLikeGeneratedIdentifier(id) {
		return id
	}
	return ""
}

var autoGeneratedTitleRe = regexp.MustCompile(`^[a-z]+-\d{4}-\d{2}-\d{2}t\d{2}-\d{2}-\d{2}(?:-[0-9a-z]+)+$`)

func looksLikeGeneratedIdentifier(s string) bool {
	if s == "" {
		return false
	}
	str := strings.ToLower(strings.TrimSpace(s))
	if str == "" {
		return false
	}
	if autoGeneratedTitleRe.MatchString(str) {
		return true
	}
	if strings.HasPrefix(str, "rollout-") {
		return true
	}
	return false
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
	if msg := extractMessageLikeText(raw["message"]); msg != "" {
		return msg
	}
	// Codex stores most chat text under payload.*; mirror message parsing there.
	if msg := extractMessageLikeText(raw["payload"]); msg != "" {
		return msg
	}
	if txt := extractContentText(raw["content"], false); txt != "" {
		return txt
	}
	if s := strings.TrimSpace(stringOr(raw["text"])); s != "" {
		return s
	}
	if s := strings.TrimSpace(stringOr(raw["message"])); s != "" {
		return s
	}
	return ""
}

func extractMessageLikeText(v any) string {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return ""
	}
	if txt := extractContentText(m["content"], true); txt != "" {
		return txt
	}
	if s := strings.TrimSpace(stringOr(m["text"])); s != "" {
		return s
	}
	if s := strings.TrimSpace(stringOr(m["message"])); s != "" {
		return s
	}
	return ""
}

func extractContentText(v any, includeThinking bool) string {
	switch c := v.(type) {
	case string:
		if strings.TrimSpace(c) != "" {
			return c
		}
	case []any:
		var parts []string
		appendPart := func(s string) {
			if strings.TrimSpace(s) == "" {
				return
			}
			parts = append(parts, s)
		}
		for _, el := range c {
			if s, ok := el.(string); ok {
				appendPart(s)
				continue
			}
			m, ok := el.(map[string]any)
			if !ok || m == nil {
				continue
			}
			t := strings.ToLower(stringOr(m["type"]))
			switch t {
			case "text", "input_text", "output_text":
				if tx := stringOr(m["text"]); strings.TrimSpace(tx) != "" {
					appendPart(tx)
					continue
				}
				if cx := stringOr(m["content"]); strings.TrimSpace(cx) != "" {
					appendPart(cx)
					continue
				}
			case "thinking":
				if includeThinking {
					if th := stringOr(m["thinking"]); strings.TrimSpace(th) != "" {
						appendPart(th)
					}
				}
			default:
				if includeThinking {
					if tx := stringOr(m["text"]); strings.TrimSpace(tx) != "" {
						appendPart(tx)
					}
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n")
		}
	}
	return ""
}

func shouldSkipEventMessage(raw map[string]any) bool {
	if raw == nil {
		return false
	}
	if strings.ToLower(stringOr(raw["type"])) != "event_msg" {
		return false
	}
	payload, ok := raw["payload"].(map[string]any)
	if !ok || payload == nil {
		return false
	}
	switch strings.ToLower(stringOr(payload["type"])) {
	case "user_message", "agent_message":
		return true
	default:
		return false
	}
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
			if cwd := findCWDInText(c); cwd != "" {
				return cwd
			}
		case []any:
			for _, el := range c {
				if s, ok := el.(string); ok {
					if cwd := findCWDInText(s); cwd != "" {
						return cwd
					}
					continue
				}
				if m, ok := el.(map[string]any); ok {
					if t, _ := m["type"].(string); t == "text" || t == "input_text" || t == "output_text" {
						if tx, _ := m["text"].(string); strings.TrimSpace(tx) != "" {
							if cwd := findCWDInText(tx); cwd != "" {
								return cwd
							}
						}
						if cx, _ := m["content"].(string); strings.TrimSpace(cx) != "" {
							if cwd := findCWDInText(cx); cwd != "" {
								return cwd
							}
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
	if s == "" {
		return ""
	}
	if cwd := between(s, "<cwd>", "</cwd>"); cwd != "" {
		return cwd
	}
	return ""
}

// UpdateSessionTitle updates the custom title for a session and persists it to a metadata file.
func (x *Indexer) UpdateSessionTitle(sessionID, newTitle string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	sess, exists := x.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Update the in-memory title
	sess.Title = trimTitle(newTitle)

	// Determine metadata file path based on provider
	var metaPath string
	if sess.Provider == "claude" {
		parts := strings.SplitN(sessionID, ":", 3)
		if len(parts) >= 3 {
			project := parts[1]
			sid := parts[2]
			metaPath = filepath.Join(x.claudeDir, project, sid+".meta.json")
		} else {
			return fmt.Errorf("invalid claude session ID format: %s", sessionID)
		}
	} else {
		metaPath = filepath.Join(x.codexDir, "sessions", sessionID+".meta.json")
	}

	// Save metadata to file
	metadata := map[string]string{
		"custom_title": sess.Title,
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file %s: %w", metaPath, err)
	}

	return nil
}

// loadSessionMetadata loads custom metadata from .meta.json file if it exists.
func (x *Indexer) loadSessionMetadata(sessionID, provider, project string) {
	var metaPath string
	if provider == "claude" {
		parts := strings.SplitN(sessionID, ":", 3)
		if len(parts) >= 3 {
			proj := parts[1]
			sid := parts[2]
			metaPath = filepath.Join(x.claudeDir, proj, sid+".meta.json")
		} else {
			return
		}
	} else {
		metaPath = filepath.Join(x.codexDir, "sessions", sessionID+".meta.json")
	}

	data, err := os.ReadFile(metaPath)
	if err != nil {
		return // File doesn't exist or can't be read, that's OK
	}

	var metadata map[string]string
	if err := json.Unmarshal(data, &metadata); err != nil {
		return // Invalid JSON, ignore
	}

	// Apply custom title if present
	if customTitle, ok := metadata["custom_title"]; ok && strings.TrimSpace(customTitle) != "" {
		x.mu.Lock()
		if sess := x.sessions[sessionID]; sess != nil {
			sess.Title = customTitle
		}
		x.mu.Unlock()
	}
}
