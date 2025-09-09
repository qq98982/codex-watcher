package api

import (
    "encoding/json"
    "html/template"
    "net/http"
    "strconv"

    "codex-watcher/internal/indexer"
)

var funcMap = template.FuncMap{
    "toJSON": func(v any) template.JS {
        b, _ := json.Marshal(v)
        return template.JS(b)
    },
}

func AttachRoutes(mux *http.ServeMux, idx *indexer.Indexer) {
    // UI
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        tmpl := template.Must(template.New("index").Funcs(funcMap).Parse(indexHTML))
        data := struct {
            Sessions []indexer.Session
            Stats    indexer.Stats
        }{Sessions: idx.Sessions(), Stats: idx.Stats()}
        _ = tmpl.Execute(w, data)
    })

    // API
    mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, 200, idx.Sessions())
    })
    mux.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query()
        sessionID := q.Get("session_id")
        limitStr := q.Get("limit")
        limit := 200
        if limitStr != "" {
            if n, err := strconv.Atoi(limitStr); err == nil {
                limit = n
            }
        }
        writeJSON(w, 200, idx.Messages(sessionID, limit))
    })
    mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, 200, idx.Stats())
    })
    mux.HandleFunc("/api/fields", func(w http.ResponseWriter, r *http.Request) {
        st := idx.Stats()
        writeJSON(w, 200, st.Fields)
    })
    mux.HandleFunc("/api/reindex", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            w.WriteHeader(405)
            return
        }
        if err := idx.Reindex(); err != nil {
            writeJSON(w, 500, map[string]any{"error": err.Error()})
            return
        }
        writeJSON(w, 200, map[string]any{"ok": true})
    })
}

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    enc := json.NewEncoder(w)
    enc.SetEscapeHTML(false)
    _ = enc.Encode(v)
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Codex Watcher</title>
  <style>
    body { font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, Ubuntu, Cantarell, Noto Sans, Helvetica Neue, Arial, "Apple Color Emoji", "Segoe UI Emoji"; margin: 0; }
    header { padding: 10px 16px; border-bottom: 1px solid #eee; display: flex; gap: 16px; align-items: center; }
    .container { display: grid; grid-template-columns: 340px 1fr; height: calc(100vh - 52px); }
    .sidebar { border-right: 1px solid #eee; overflow: auto; }
    .content { overflow: auto; }
    .item { padding: 10px 12px; border-bottom: 1px solid #f3f3f3; cursor: pointer; }
    .item:hover { background: #fafafa; }
    .meta { color: #666; font-size: 12px; }
    .msg { padding: 10px 12px; border-bottom: 1px solid #f3f3f3; }
    .role { font-weight: 600; margin-right: 8px; }
    code, pre { background: #f7f7f7; }
    pre { padding: 8px; overflow: auto; }
    .row { display: flex; gap: 16px; align-items: baseline; }
    .pill { font-size: 12px; background: #efefef; border-radius: 9999px; padding: 2px 8px; margin-right: 6px; }
    .pill.role-user { background: #e0f2fe; }
    .pill.role-assistant { background: #e9d5ff; }
    .pill.role-tool { background: #ffe4e6; }
    .stats { color: #333; font-size: 14px; }
    .btn { padding: 6px 10px; border: 1px solid #ccc; border-radius: 6px; background: #fff; cursor: pointer; }
  </style>
  <link rel="stylesheet" href="https://unpkg.com/@highlightjs/cdn-assets@11.9.0/styles/github.min.css">
  <script src="https://unpkg.com/htmx.org@1.9.12"></script>
  <script src="https://unpkg.com/marked@12.0.2/marked.min.js"></script>
  <script src="https://unpkg.com/dompurify@3.1.7/dist/purify.min.js"></script>
  <script src="https://unpkg.com/@highlightjs/cdn-assets@11.9.0/highlight.min.js"></script>
  <script>
    async function selectSession(id) {
      const res = await fetch('/api/messages?session_id=' + encodeURIComponent(id) + '&limit=500');
      const data = await res.json();
      const el = document.getElementById('messages');
      el.innerHTML = data.map(function(m){
        var role = (m.role || (m.raw && m.raw.role) || '').toLowerCase();
        var rolePillClass = role === 'user' ? 'role-user' : (role === 'assistant' ? 'role-assistant' : 'role-tool');
        var ts = (m.ts ? new Date(m.ts).toLocaleString() : '');
        var model = (m.model ? '<span class="pill">' + m.model + '</span>' : '');
        var html = renderContent(m);
        return '<div class="msg">'
          + '<div class="meta"><span class="pill ' + rolePillClass + '">' + (role || 'message') + '</span> <span>' + ts + '</span> ' + model + '</div>'
          + '<div class="content">' + html + '</div>'
          + '</div>';
      }).join('');
      try { hljs.highlightAll(); } catch(e) {}
    }

    function tryString(v){ if(typeof v==='string') return v; try{ return JSON.stringify(v, null, 2)}catch(e){return ''}}

    function renderContent(m){
      var md = '';
      if (m && typeof m.content === 'string' && m.content.trim() !== '') {
        md = m.content;
      } else if (m && m.raw && m.raw.content) {
        // handle OpenAI-style content arrays or objects
        var c = m.raw.content;
        if (Array.isArray(c)) {
          md = c.map(function(part){
            if (typeof part === 'string') return part;
            if (part && typeof part === 'object') {
              if (part.type === 'text' && part.text) return part.text;
              if (part.type === 'input_text' && part.text) return part.text;
              if (part.type === 'output_text' && part.text) return part.text;
              if (part.type === 'tool_result' && part.content) return 'Tool result:\n\n~~~\n' + tryString(part.content) + '\n~~~';
            }
            return tryString(part);
          }).join('\n\n');
        } else if (typeof c === 'string') {
          md = c;
        } else if (typeof c === 'object') {
          md = '~~~json\n' + tryString(c) + '\n~~~';
        }
      } else if (m && (m.tool_name || m.type === 'tool_call')) {
        md = '**' + (m.tool_name || 'tool') + '** call\n\n~~~json\n' + tryString(m.raw && (m.raw.arguments || m.raw.args || m.raw)) + '\n~~~';
      } else {
        md = tryString(m && m.raw);
      }
      try { return DOMPurify.sanitize(marked.parse(md)); } catch(e) { return escapeHTML(md); }
    }

    function escapeHTML(s){ return (s||'').toString().replace(/[&<>"']/g, function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;','\'':'&#39;'}[c]||c;}) }
    async function refreshSessions(){ const r=await fetch('/api/sessions'); const data = await r.json(); renderSessions(data) }
    function renderSessions(list){
      const s = document.getElementById('sessions');
      s.innerHTML = list.map(function(it){
        var pills = Object.keys(it.models||{}).map(function(m){ return '<span class="pill">'+m+'</span>'; }).join('');
        var title = (it.title || it.id);
        var firstAt = (it.first_at ? new Date(it.first_at).toLocaleString() : '');
        var lastAt = (it.last_at ? new Date(it.last_at).toLocaleString() : '');
        return '<div class="item" onclick="selectSession(\'' + it.id + '\')">'
          + '<div><strong>' + title + '</strong></div>'
          + '<div class="meta">' + it.message_count + ' msgs • ' + firstAt + ' → ' + lastAt + '</div>'
          + '<div class="meta">' + pills + '</div>'
          + '</div>';
      }).join('');
    }
    window.addEventListener('load', ()=>{
      renderSessions(JSON.parse(document.getElementById('init-sessions').textContent));
    });
  </script>
  <script type="application/json" id="init-sessions">{{ toJSON .Sessions }}</script>
</head>
<body>
  <header>
    <div style="font-weight:700">Codex Watcher</div>
    <div class="row stats">
      <div title="Sessions">🗂 {{ .Stats.TotalSessions }}</div>
      <div title="Messages">💬 {{ .Stats.TotalMessages }}</div>
    </div>
    <div style="flex:1"></div>
    <button class="btn" onclick="refreshSessions()">Refresh</button>
    <form method="post" action="/api/reindex" onsubmit="event.preventDefault(); fetch('/api/reindex',{method:'POST'}).then(()=>refreshSessions())">
      <button class="btn" type="submit">Reindex</button>
    </form>
  </header>
  <div class="container">
    <div class="sidebar" id="sessions"></div>
    <div class="content" id="messages"></div>
  </div>
</body>
</html>
`
