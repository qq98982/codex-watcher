package api

import (
    "encoding/json"
    "html/template"
    "net/http"
    "strconv"

    "codex-watcher/internal/indexer"
    "codex-watcher/internal/search"
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
    mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query()
        raw := q.Get("q")
        limit := 50
        offset := 0
        if s := q.Get("limit"); s != "" {
            if n, err := strconv.Atoi(s); err == nil { limit = n }
        }
        if s := q.Get("offset"); s != "" {
            if n, err := strconv.Atoi(s); err == nil { offset = n }
        }
        scope := q.Get("in")
        parsed := search.Parse(raw, scope)
        res := search.Exec(idx, parsed, limit, offset)
        writeJSON(w, 200, res)
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
    .pill.role-user { background: #e0f2fe; text-transform: uppercase; font-weight: bold; }
    .pill.role-assistant { background: #e9d5ff; text-transform: uppercase; font-weight: bold; }
    .pill.role-tool { background: #ffe4e6; }
    .stats { color: #333; font-size: 14px; }
    .btn { padding: 6px 10px; border: 1px solid #ccc; border-radius: 6px; background: #fff; cursor: pointer; }
    .back-link { color: #333; text-decoration: none; }
    .back-link:hover { text-decoration: underline; }
    .searchbar { display:flex; gap:8px; align-items:center; padding:8px 10px; }
    .searchbar input[type="text"] { flex:1; padding:6px 8px; border:1px solid #ddd; border-radius:6px; }
    .searchbar select { padding:6px 8px; border:1px solid #ddd; border-radius:6px; }
    .result-item { padding: 8px 10px; border-bottom: 1px solid #f3f3f3; cursor: pointer; }
    .result-item:hover { background: #fafafa; }
    .group .result-item { padding-left: 18px; }
    .msg.focus { animation: focusFlash 2s ease 1; }
    @keyframes focusFlash { 0% { background:#fff7c2; } 100% { background:transparent; } }
  </style>
  <link rel="stylesheet" href="https://unpkg.com/@highlightjs/cdn-assets@11.9.0/styles/github.min.css">
  <script src="https://unpkg.com/htmx.org@1.9.12"></script>
  <script src="https://unpkg.com/marked@12.0.2/marked.min.js"></script>
  <script src="https://unpkg.com/dompurify@3.1.7/dist/purify.min.js"></script>
  <script src="https://unpkg.com/@highlightjs/cdn-assets@11.9.0/highlight.min.js"></script>
  <script>
    // Helpers: shell quoting and output toggles
    function shQuote(arg){
      if (arg == null) return '';
      arg = String(arg);
      if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(arg)) return arg; // safe unquoted
      // single-quote, escape single quotes by closing/opening
      return "'" + arg.replace(/'/g, "'\\''") + "'";
    }
    function shJoin(arr){ try{ return (arr||[]).map(shQuote).join(' ');}catch(e){ return ''} }
    function toggleOutput(id){
      var t = document.getElementById(id+':trunc');
      var f = document.getElementById(id+':full');
      var b = document.getElementById(id+':btn');
      if (!t || !f) return;
      var isTruncShown = t.style.display !== 'none';
      t.style.display = isTruncShown ? 'none' : '';
      f.style.display = isTruncShown ? '' : 'none';
      if (b) b.textContent = isTruncShown ? 'Show less' : 'Show full';
      try { hljs.highlightAll(); } catch(e) {}
    }
    function toggleTool(id){
      var c = document.getElementById(id+':collapsed');
      var e = document.getElementById(id+':expanded');
      var a = document.getElementById(id+':arrow');
      if (!c || !e) return;
      var isCollapsedShown = c.style.display !== 'none';
      c.style.display = isCollapsedShown ? 'none' : '';
      e.style.display = isCollapsedShown ? '' : 'none';
      if (a) a.textContent = isCollapsedShown ? '▾' : '▸';
      try { hljs.highlightAll(); } catch(e) {}
    }
    let currentSessionId = null;
    async function selectSession(id) {
      currentSessionId = id;
      const res = await fetch('/api/messages?session_id=' + encodeURIComponent(id) + '&limit=500');
      const data = await res.json();
      const el = document.getElementById('messages');
      el.innerHTML = data.map(function(m){
        var role = (m.role || (m.raw && m.raw.role) || '').toLowerCase();
        var isReasoning = (m.type === 'reasoning') || (m.raw && m.raw.type === 'reasoning');
        var isFuncCall = (m.type === 'function_call') || (m.raw && m.raw.type === 'function_call');
        var isFuncOut = (m.type === 'function_call_output') || (m.raw && m.raw.type === 'function_call_output');
        var rolePillClass = isReasoning ? 'role-assistant' : (role === 'user' ? 'role-user' : (role === 'assistant' ? 'role-assistant' : 'role-tool'));
        var tsHTML = '';
        var model = (m.model ? '<span class="pill">' + m.model + '</span>' : '');
        var pillLabel = isReasoning ? 'Assistant Thinking' : (isFuncCall ? ('Tool: ' + ((m.raw && m.raw.name) || 'tool')) : (isFuncOut ? ('Tool Output' + ((m.raw && m.raw.name) ? (': ' + m.raw.name) : '')) : (role || 'message')));
        var id2 = null;
        var html = renderContent(m);
        if (isFuncCall || isFuncOut) {
          id2 = 'tool-' + (m.id || Math.random().toString(36).slice(2));
          var summary = '';
          if (isFuncCall) {
            var name = (m.raw && m.raw.name) || '';
            var args = (m.raw && m.raw.arguments);
            var obj = null; if (args && typeof args === 'string') { try{ obj = JSON.parse(args)}catch(e){} } else if (args && typeof args === 'object') { obj = args }
            var cmdLine = (obj && Array.isArray(obj.command)) ? shJoin(obj.command) : '';
            summary = cmdLine ? ('$ ' + cmdLine) : (name ? (name + ' arguments') : 'tool arguments');
          } else if (isFuncOut) {
            var out = (m.raw && m.raw.output); var textOut=''; var stderrOut='';
            if (typeof out === 'string') { try{ var p=JSON.parse(out); if(p){ if(typeof p.output==='string') textOut=p.output; if(typeof p.stderr==='string') stderrOut=p.stderr; } }catch(e){} if(!textOut) textOut=out; }
            else if (out && typeof out === 'object') { if (typeof out.output==='string') textOut=out.output; if(typeof out.stderr==='string') stderrOut=out.stderr; }
            var parts=[]; if (textOut) parts.push('stdout'); if (stderrOut) parts.push('stderr'); summary = parts.length? ('output: ' + parts.join(', ')) : 'output';
          }
          var collapsedDiv = '<div id="'+id2+':collapsed" class="meta" style="font-family:ui-monospace, SFMono-Regular, Menlo, monospace;' + (collapseTools? '' : 'display:none;') + '"><p style="margin:6px 0 0">' + escapeHTML(summary) + '</p></div>';
          var expandedDiv = '<div id="'+id2+':expanded" ' + (collapseTools? 'style=\"display:none;\"' : '') + '>' + html + '</div>';
          html = collapsedDiv + expandedDiv;
        }
        if (!html || !html.trim()) return '';
        var arrow = '';
        if (id2) {
          var sym = collapseTools ? '▸' : '▾';
          arrow = ' <span id="'+id2+':arrow" class="pill" style="cursor:pointer" onclick="toggleTool(\''+id2+'\')">' + sym + '</span>';
        }
        var anchorId = (m.id && String(m.id).trim() !== '') ? ('msg-' + m.id) : ('msg-L' + (m.line_no || 0));
        return '<div class="msg" id="' + anchorId + '">'
          + '<div class="meta"><span class="pill ' + rolePillClass + '">' + pillLabel + '</span>' + arrow + ' ' + model + '</div>'
          + '<div class="content">' + html + '</div>'
          + '</div>';
      }).filter(Boolean).join('');
      if (!el.innerHTML || !el.innerHTML.trim()) {
        el.innerHTML = '<div class="meta" style="padding:12px;color:#666;">此会话没有可显示的文本</div>';
      }
      try { hljs.highlightAll(); } catch(e) {}
      // scroll to a pending focus target if requested
      try {
        if (window.pendingFocus && window.pendingFocus.sessionId === id) {
          var tmp = window.pendingFocus;
          var anchor = tmp.messageId ? ('msg-' + tmp.messageId) : ('msg-L' + (tmp.lineNo||0));
          var node = document.getElementById(anchor);
          if (node) {
            try { node.scrollIntoView({behavior:'smooth', block:'center'}); } catch(e) { node.scrollIntoView(); }
            node.classList.add('focus');
            setTimeout(function(){ try{ node.classList.remove('focus'); }catch(e){} }, 2200);
          }
          window.pendingFocus = null;
        }
      } catch(e) {}
    }

    function tryString(v){ if(typeof v==='string') return v; try{ return JSON.stringify(v, null, 2)}catch(e){return ''}}

    function renderContent(m){
      var md = '';
      var htmlBuilt = '';
      if (m && typeof m.content === 'string' && m.content.trim() !== '') {
        md = m.content;
      } else if (m && m.raw && m.raw.content && Array.isArray(m.raw.content)) {
        md = m.raw.content.map(function(part){
          if (typeof part === 'string') return part;
          if (part && typeof part === 'object') {
            if ((part.type === 'text' || part.type === 'input_text' || part.type === 'output_text') && part.text) return part.text;
            if ((part.type === 'text' || part.type === 'input_text' || part.type === 'output_text') && typeof part.content === 'string') return part.content;
          }
          return '';
        }).filter(Boolean).join('\n\n');
      } else if (m && m.raw && (m.raw.type === 'function_call' || m.type === 'function_call')) {
        // Render function call arguments; prefer commands for shell
        var name = (m.raw && m.raw.name) || '';
        var args = (m.raw && m.raw.arguments);
        var obj = null;
        if (args && typeof args === 'string') { try { obj = JSON.parse(args); } catch(e) { obj = null; } }
        else if (args && typeof args === 'object') { obj = args; }
        var cmdLine = '';
        if (obj && Array.isArray(obj.command)) {
          try { cmdLine = shJoin(obj.command); } catch(e) {}
        }
        if (cmdLine) {
          md = '**' + (name || 'tool') + ' command**\n\n~~~bash\n$ ' + cmdLine + '\n~~~';
        } else {
          md = '**' + (name || 'tool') + ' arguments**\n\n~~~json\n' + tryString(obj || args || m.raw) + '\n~~~';
        }
      } else if (m && m.raw && (m.raw.type === 'function_call_output' || m.type === 'function_call_output')) {
        // Render function output; try to unwrap nested JSON with { output: "..." }
        var out = (m.raw && m.raw.output);
        var textOut = '';
        var stderrOut = '';
        if (typeof out === 'string') {
          try { var parsed = JSON.parse(out); if (parsed) { if (typeof parsed.output === 'string') textOut = parsed.output; if (typeof parsed.stderr === 'string') stderrOut = parsed.stderr; } } catch(e) { /* keep raw */ }
          if (!textOut) textOut = out;
        } else if (out && typeof out === 'object') {
          if (typeof out.output === 'string') textOut = out.output;
          if (typeof out.stderr === 'string') stderrOut = out.stderr;
          if (!textOut && !stderrOut) textOut = tryString(out);
        }
        var MAX = 5000;
        var id = 'out-' + (m.id || Math.random().toString(36).slice(2));
        function section(label, body){
          if (!body) return '';
          var full = body;
          var trunc = body.length>MAX ? body.slice(0,MAX) + '\n... (truncated)' : body;
          if (full.length>MAX) {
            return '<div><div class="meta"><strong>' + label + '</strong> · <button id="'+id+':btn" class="btn" onclick="toggleOutput(\''+id+'\')">Show full</button></div>'
              + '<pre id="'+id+':trunc" style="margin-top:6px; white-space:pre; overflow:auto;">' + escapeHTML(trunc) + '</pre>'
              + '<pre id="'+id+':full" style="display:none; margin-top:6px; white-space:pre; overflow:auto;">' + escapeHTML(full) + '</pre>'
              + '</div>';
          }
          return '<div><div class="meta"><strong>' + label + '</strong></div>'
            + '<pre style="margin-top:6px; white-space:pre; overflow:auto;">' + escapeHTML(full) + '</pre>'
            + '</div>';
        }
        htmlBuilt = section('stdout', textOut) + (stderrOut? section('stderr', stderrOut) : '');
      } else if (m && m.raw && m.raw.summary) {
        var s = m.raw.summary;
        if (Array.isArray(s)) {
          md = s.map(function(part){
            if (typeof part === 'string') return part;
            if (part && typeof part === 'object') {
              if (part.type === 'summary_text' && typeof part.text === 'string') return part.text;
              if (part.type === 'summary_text' && typeof part.content === 'string') return part.content;
            }
            return '';
          }).filter(Boolean).join('\n\n');
        } else if (typeof s === 'string') {
          md = s;
        }
      } else if (m && m.raw && typeof m.raw.text === 'string') {
        md = m.raw.text;
      } else {
        return '';
      }
      if (htmlBuilt) { return DOMPurify.sanitize(htmlBuilt); }
      try { return DOMPurify.sanitize(marked.parse(md)); } catch(e) { return escapeHTML(md); }
    }

    function escapeHTML(s){ return (s||'').toString().replace(/[&<>"']/g, function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;','\'':'&#39;'}[c]||c;}) }
    let viewMode = 'time-cwd'; // 'cwd-time' | 'time-cwd' | 'flat'
    let collapseTools = true;
    let sessionsCache = [];
    window.pendingFocus = null; // { sessionId, messageId, lineNo }
    function setViewMode(v){ viewMode = v; try{ localStorage.setItem('viewMode', viewMode); }catch(e){} renderSessions(sessionsCache); if (currentSessionId) selectSession(currentSessionId); }
    function toggleCollapseTools(v){ collapseTools = !!v; try{ localStorage.setItem('collapseTools', collapseTools?'1':'0'); }catch(e){} if (currentSessionId) selectSession(currentSessionId); }

    function getCollapsed(key){ try{ return (localStorage.getItem('collapsed:'+key)||'0')==='1'; }catch(e){ return false; } }
    function setCollapsed(key, val){ try{ localStorage.setItem('collapsed:'+key, val?'1':'0'); }catch(e){} }
    function isBucketKey(key){ return key && key.indexOf('bucket:')===0 && key.indexOf(':cwd:')===-1 }
    let lastSearch = {res:null, q:''};
    function toggleGroup(key){
      if (isBucketKey(key)) {
        // Accordion behavior for buckets: open this one, close others
        var all = ['Today','Yesterday','Last 7 days','Last 30 days','All'];
        for (var i=0;i<all.length;i++){
          var k = 'bucket:'+all[i];
          setCollapsed(k, k!==key); // collapse all except current
        }
      } else {
        setCollapsed(key, !getCollapsed(key));
      }
      if (String(key||'').indexOf('search:')===0) {
        if (lastSearch && lastSearch.res) { renderSearchResults(lastSearch.res, lastSearch.q||''); }
      } else {
        renderSessions(sessionsCache);
      }
    }

    // Search
    async function runSearch(){
      var q = (document.getElementById('searchInput')||{}).value || '';
      var scope = (document.getElementById('searchScope')||{}).value || 'content';
      q = (q||'').trim();
      if (!q) { return clearSearch(); }
      var url = '/api/search?q=' + encodeURIComponent(q) + '&in=' + encodeURIComponent(scope) + '&limit=200';
      const res = await fetch(url);
      const data = await res.json();
      lastSearch = {res: data, q: q};
      renderSearchResults(data, q);
    }
    function clearSearch(){
      try{ document.getElementById('searchInput').value=''; }catch(e){}
      showSessionsList();
      var el = document.getElementById('search-results'); if (el) el.innerHTML='';
    }
    function showSearchView(){
      var sr = document.getElementById('search-results');
      var sc = document.getElementById('sidebar-controls');
      var sl = document.getElementById('sessions');
      if (sr) sr.style.display = '';
      if (sc) sc.style.display = 'none';
      if (sl) sl.style.display = 'none';
    }
    function showSessionsList(){
      var sr = document.getElementById('search-results');
      var sc = document.getElementById('sidebar-controls');
      var sl = document.getElementById('sessions');
      if (sr) sr.style.display = 'none';
      if (sc) sc.style.display = '';
      if (sl) sl.style.display = '';
    }
    function tokensFromQuery(q){
      if(!q) return [];
      var arr = q.split(/\s+/).filter(Boolean);
      var out = [];
      for (var i=0;i<arr.length;i++){
        var t = arr[i];
        if (!t) continue;
        if (t === 'OR') continue;
        if (t[0] === '-') t = t.slice(1);
        if (t.indexOf(':')>0) continue; // field
        if (t[0] === '"' && t[t.length-1]==='"') t = t.slice(1,-1);
        if (t[0] === '/' && t.lastIndexOf('/')>0) continue; // regex: skip naive highlight
        t = t.replace(/\*/g,'');
        if (t) out.push(t);
      }
      return out.slice(0,5);
    }
    function hiSnippet(s, q){ if(!s) return ''; var toks = tokensFromQuery(q); var out=escapeHTML(s); try{ for(var i=0;i<toks.length;i++){ var t=toks[i]; var rx=new RegExp(t.replace(/[.*+?^${}()|[\]\\]/g,'\\$&'),'ig'); out=out.replace(rx, function(m){return '<mark>'+m+'</mark>';}); } }catch(e){} return out; }
    function renderSearchResults(res, q){
      showSearchView();
      var el = document.getElementById('search-results'); if(!el) return;
      if (!res || !Array.isArray(res.hits) || res.hits.length===0) { el.innerHTML = '<div class="meta" style="padding:8px 10px;"><a href="#" class="back-link" onclick="showSessionsList(); return false;">← Back</a></div><div class="meta" style="padding:8px 10px;">No results</div>'; return; }
      var bySession = {};
      for (var i=0;i<res.hits.length;i++){ var h=res.hits[i]; var sid=h.session_id; if(!bySession[sid]) bySession[sid]=[]; bySession[sid].push(h); }
      var groups = Object.keys(bySession).map(function(sid){ var hits=bySession[sid]; hits.sort(function(a,b){ var ta=(a.ts?Date.parse(a.ts):0), tb=(b.ts?Date.parse(b.ts):0); if(ta!==tb) return tb-ta; return (a.line_no||0)-(b.line_no||0); }); return {sid:sid, hits:hits}; });
      groups.sort(function(a,b){ var ta=(a.hits[0]&&a.hits[0].ts?Date.parse(a.hits[0].ts):0), tb=(b.hits[0]&&b.hits[0].ts?Date.parse(b.hits[0].ts):0); return tb-ta; });
      var sessMap = {}; try{ (sessionsCache||[]).forEach(function(s){ sessMap[s.id]=s; }); }catch(e){}
      function nameForSession(id){ var s=sessMap[id]; if(!s) return id; var base = (s.cwd_base||''); if (base) return base; return (s.title||id); }
      function startTimeForSession(id){ var s=sessMap[id]; if(!s) return ''; return s.first_at ? new Date(s.first_at).toLocaleString() : ''; }
      var html = '<div class="meta" style="padding:8px 10px;"><a href="#" class="back-link" onclick="showSessionsList(); return false;">← Back</a></div>';
      html += '<div class="meta" style="padding:8px 10px;">Found ' + (res.total||0) + ' in ' + (res.took_ms||0) + ' ms' + (res.truncated? ' (truncated)':'' ) + '</div>';
      for (var g=0; g<groups.length; g++){
        var group = groups[g]; var key = 'search:session:'+group.sid; var collapsed = getCollapsed(key); var caret = collapsed ? '▸' : '▾';
        var startAt = startTimeForSession(group.sid);
        html += '<div class="group">' + '<div class="item" onclick="toggleGroup(\'' + key.replace(/'/g,"\'") + '\')"><strong>' + escapeHTML(nameForSession(group.sid)) + '</strong> <span class="meta">(' + group.hits.length + ')</span> ' + caret + (startAt ? ('<br /><span class="meta">' + startAt + '</span>') : '') + '</div>';
        if (!collapsed){
          for (var j=0;j<group.hits.length;j++){
            var h = group.hits[j]; var pill = (h.type && h.type!=='') ? ('<span class="pill">'+h.type+'</span>') : (h.role? ('<span class="pill">'+h.role+'</span>') : '<span class="pill">message</span>');
            var field = h.field || 'content'; var snippet = hiSnippet(h.content||'', q); var when = (h.ts ? new Date(h.ts).toLocaleString() : '');
            var anchor = (h.message_id && String(h.message_id).trim() !== '') ? String(h.message_id) : ('L'+(h.line_no||0));
            html += '<div class="result-item" onclick="openHit(\''+group.sid+'\', \''+anchor.replace(/'/g,"\\'")+'\', '+(h.line_no||0)+')">' + '<div class="meta">' + pill + ' <span class="pill">' + field + '</span> <span class="meta">' + when + '</span></div>' + '<div>' + (snippet? snippet : '<span class="meta">(no preview)</span>') + '</div>' + '</div>';
          }
        }
        html += '</div>';
      }
      el.innerHTML = html;
    }
    function openHit(sessionId, anchor, lineNo){
      window.pendingFocus = { sessionId: sessionId, messageId: (anchor && anchor[0] !== 'L') ? anchor : '', lineNo: lineNo };
      selectSession(sessionId);
    }

    function formatPath(p){ if(!p) return '(Unknown)';
      // shorten /Users/<name> to ~
      if (p.indexOf('/Users/')===0){ var ix=p.indexOf('/',7); if(ix>0){ return '~'+p.slice(ix); } }
      return p; }
    function groupByCWD(list){
      var m = {};
      for (var i=0;i<list.length;i++){
        var it=list[i]; var key = it.cwd || '(Unknown)';
        if(!m[key]) m[key]=[];
        m[key].push(it);
      }
      var groups=[];
      for (var k in m){
        var arr=m[k].slice();
        arr.sort(function(a,b){ var da = new Date(a.last_at||0).getTime(); var db = new Date(b.last_at||0).getTime(); return db-da; });
        var last = arr.length? arr[0].last_at : '';
        groups.push({cwd:k, items:arr, lastAt:last});
      }
      groups.sort(function(a,b){ var da = new Date(a.lastAt||0).getTime(); var db = new Date(b.lastAt||0).getTime(); return db-da; });
      return groups;
    }
    function baseName(p){ if(!p) return '(Unknown)'; p = (p||'').replace(/\/+$/,''); var i=p.lastIndexOf('/'); return i>=0? p.slice(i+1):p; }
    function sortByLastAtDesc(a,b){ var da=new Date(a.last_at||0).getTime(); var db=new Date(b.last_at||0).getTime(); return db-da }
    function bucketLabel(dt){ var d=new Date(dt); if(isNaN(d)) return 'Older'; var now=new Date(); var oneDay=24*3600*1000; var startToday=new Date(now.getFullYear(),now.getMonth(),now.getDate()); var startYesterday=new Date(startToday.getTime()-oneDay); var start7=new Date(startToday.getTime()-7*oneDay); var start30=new Date(startToday.getTime()-30*oneDay); if(d>=startToday) return 'Today'; if(d>=startYesterday) return 'Yesterday'; if(d>=start7) return 'Last 7 days'; if(d>=start30) return 'Last 30 days'; return 'Older'; }
    function bucketizeByTime(list){
      var m={}; list.forEach(function(it){ var lbl=bucketLabel(it.last_at); (m[lbl]||(m[lbl]=[])).push(it); });
      var order=['Today','Yesterday','Last 7 days','Last 30 days'];
      var buckets=[];
      order.forEach(function(lbl){ if(m[lbl]&&m[lbl].length){ m[lbl].sort(sortByLastAtDesc); buckets.push({label:lbl, items:m[lbl]}); } });
      // Add an "All" bucket with everything, sorted
      var all = list.slice().sort(sortByLastAtDesc);
      buckets.push({label:'All', items: all});
      return buckets;
    }
    async function refreshSessions(){ const r=await fetch('/api/sessions'); const data = await r.json(); renderSessions(data) }
    // Auto-refresh sessions list periodically and on tab focus
    setInterval(()=>{ refreshSessions().catch(()=>{}) }, 10000);
    document.addEventListener('visibilitychange', ()=>{ if(!document.hidden) refreshSessions() });
    function renderSessions(list){
      sessionsCache = Array.isArray(list) ? list : [];
      const all = sessionsCache;
      const filtered = all;
      const s = document.getElementById('sessions');
      function parseDateSafe(v){ var d=new Date(v); return isNaN(d)? null : d; }
      function endAtOf(it){ var a=parseDateSafe(it.last_at), b=parseDateSafe(it.file_mod_at); if(a&&b) return a>b?a:b; return a||b; }
      function fmtStartCountDur(it){
        var start = parseDateSafe(it.first_at);
        var end = endAtOf(it) || start;
        var count = (it.message_count||0);
        var startStr = start? start.toLocaleString() : '';
        var durMs = (start && end) ? (end - start) : 0;
        function human(ms){ if(ms<=0) return '0s'; var s=Math.floor(ms/1000); var d=Math.floor(s/86400); s%=86400; var h=Math.floor(s/3600); s%=3600; var m=Math.floor(s/60); s%=60; var out=[]; if(d) out.push(d+'d'); if(h) out.push(h+'h'); if(m) out.push(m+'m'); if(s && out.length<2) out.push(s+'s'); return out.join(' ')||'0s'; }
        return startStr + ' · ' + count + ' msgs · ' + human(durMs);
      }
      function hasSession(list, id){ if(!id) return false; for(var i=0;i<list.length;i++){ if(list[i].id===id) return true } return false }
      if(viewMode === 'flat'){
        s.innerHTML = filtered.map(function(it){
          var pills = Object.keys(it.models||{}).map(function(m){ return '<span class="pill">'+m+'</span>'; }).join('');
          var meta = fmtStartCountDur(it);
          return '<div class="item" data-id="' + it.id + '" onclick="selectSession(\'' + it.id + '\')">'
            + '<div class="meta">' + meta + '</div>'
            + '<div class="meta">' + pills + '</div>'
            + '</div>';
        }).join('');
        if (!currentSessionId || !hasSession(filtered, currentSessionId)) {
          var first = s.querySelector('.item');
          if (first && first.dataset && first.dataset.id) { selectSession(first.dataset.id); }
        }
      } else if (viewMode === 'cwd-time') {
        var groups = groupByCWD(filtered);
        s.innerHTML = groups.map(function(g){
          var key = 'cwd:'+ (g.cwd||'');
          var collapsed = getCollapsed(key);
          var caret = collapsed ? '▸' : '▾';
          var title = formatPath(g.cwd);
          var titleBase = baseName(g.cwd);
          var sessionsHTML = '';
          if(!collapsed){
            sessionsHTML = g.items.map(function(it){
              var pills = Object.keys(it.models||{}).map(function(m){ return '<span class="pill">'+m+'</span>'; }).join('');
              var meta = fmtStartCountDur(it);
              return '<div class="item" data-id="' + it.id + '" onclick="selectSession(\'' + it.id + '\')">'
                + '<div class="meta">' + meta + '</div>'
                + '<div class="meta">' + pills + '</div>'
                + '</div>';
            }).join('');
          }
          var lastAtG = (g.lastAt ? new Date(g.lastAt).toLocaleString() : '');
          return '<div class="group">'
            + '<div class="item" onclick="toggleGroup(\'' + (key.replace(/'/g,"\'")) + '\')" title="' + (g.cwd||'') + '">' + caret + ' <strong style="font-weight:600">' + titleBase + '</strong><br /> <span class="meta">' + title + '</span><br /> <span class="meta">' + g.items.length + ' sessions • ' + lastAtG + '</span></div>'
            + (collapsed ? '' : sessionsHTML)
            + '</div>';
        }).join('');
        if (!currentSessionId || !hasSession(filtered, currentSessionId)) {
          var first2 = s.querySelector('.group .item[data-id]');
          if (first2 && first2.dataset && first2.dataset.id) { selectSession(first2.dataset.id); }
        }
      } else if (viewMode === 'time-cwd') {
        var buckets = bucketizeByTime(filtered);
        s.innerHTML = buckets.map(function(b){
          var bkey = 'bucket:'+b.label;
          var bCollapsed = getCollapsed(bkey);
          var bCaret = bCollapsed ? '▸' : '▾';
          var inner = '';
          if(!bCollapsed){
            var groups = groupByCWD(b.items);
            inner = groups.map(function(g){
              var key = bkey+':cwd:'+(g.cwd||'');
              var collapsed = getCollapsed(key);
              var caret = collapsed ? '▸' : '▾';
              var title = formatPath(g.cwd);
              var titleBase = baseName(g.cwd);
              var sessionsHTML = '';
              if(!collapsed){
                sessionsHTML = g.items.map(function(it){
                  var pills = Object.keys(it.models||{}).map(function(m){ return '<span class="pill">'+m+'</span>'; }).join('');
                  var meta = fmtStartCountDur(it);
                  return '<div class="item" data-id="' + it.id + '" onclick="selectSession(\'' + it.id + '\')">'
                    + '<div class="meta">' + meta + '</div>'
                    + '<div class="meta">' + pills + '</div>'
                    + '</div>';
                }).join('');
              }
              var lastAtG = (g.lastAt ? new Date(g.lastAt).toLocaleString() : '');
              return '<div class="group">'
                + '<div class="item" onclick="toggleGroup(\'' + key.replace(/'/g,"\'") + '\')" title="' + (g.cwd||'') + '">' + caret + ' <strong style="font-weight:600">' + titleBase + '</strong><br /> <span class="meta">' + title + '</span><br /> <span class="meta">' + g.items.length + ' sessions • ' + lastAtG + '</span></div>'
                + (collapsed ? '' : sessionsHTML)
                + '</div>';
            }).join('');
          }
          return '<div class="group">'
            + '<div class="item" onclick="toggleGroup(\'' + bkey + '\')"><strong>' + b.label + '</strong> <span class="meta">(' + b.items.length + ' sessions)</span> ' + bCaret + '</div>'
            + (bCollapsed ? '' : inner)
            + '</div>';
        }).join('');
        if (!currentSessionId || !hasSession(filtered, currentSessionId)) {
          var first3 = s.querySelector('.group .item[data-id]');
          if (first3 && first3.dataset && first3.dataset.id) { selectSession(first3.dataset.id); }
        }
      }
    }
    window.addEventListener('load', ()=>{
      try{ viewMode = localStorage.getItem('viewMode') || 'time-cwd'; }catch(e){ viewMode='time-cwd'; }
      try{ collapseTools = (localStorage.getItem('collapseTools')||'1')==='1'; }catch(e){ collapseTools=true; }
      var sel = document.getElementById('viewModeSelect');
      if (sel) sel.value = viewMode;
      var ct = document.getElementById('collapseToolsToggle');
      if (ct) ct.checked = collapseTools;
      const init = JSON.parse(document.getElementById('init-sessions').textContent);
      sessionsCache = Array.isArray(init) ? init : [];
      renderSessions(sessionsCache);
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
    <label class="meta" style="margin-right:8px; display:flex; align-items:center; gap:6px;">
      <input type="checkbox" id="collapseToolsToggle" checked onchange="toggleCollapseTools(this.checked)">
      Collapse Tools
    </label>
    <div class="searchbar" style="max-width:680px;">
      <input id="searchInput" type="text" placeholder="Search across sessions… (quotes, -exclude, OR, fields, /re/flags)" onkeydown="if(event.key==='Enter'){runSearch()}" />
      <select id="searchScope" title="Scope">
        <option value="content">Content</option>
        <option value="tools">Tools</option>
        <option value="all">All</option>
      </select>
      <button class="btn" onclick="runSearch()">Search</button>
      <button class="btn" onclick="clearSearch()">Clear</button>
    </div>
  </header>
  <div class="container">
    <div class="sidebar">
      <div id="search-results" style="display:none"></div>
      <div id="sidebar-controls" class="meta" style="padding:8px 10px; border-bottom:1px solid #eee; display:flex; align-items:center; gap:8px;">
        <span>View</span>
        <select id="viewModeSelect" onchange="setViewMode(this.value)" class="btn" style="padding:4px 6px;">
          <option value="time-cwd">Time → Dir</option>
          <option value="cwd-time">Dir → Time</option>
          <option value="flat">All by Time</option>
        </select>
      </div>
      <div id="sessions"></div>
    </div>
    <div class="content" id="messages"></div>
  </div>
</body>
</html>
`
