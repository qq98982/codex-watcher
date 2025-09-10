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
        if (!html || !html.trim()) return '';
        return '<div class="msg">'
          + '<div class="meta"><span class="pill ' + rolePillClass + '">' + (role || 'message') + '</span> <span>' + ts + '</span> ' + model + '</div>'
          + '<div class="content">' + html + '</div>'
          + '</div>';
      }).filter(Boolean).join('');
      if (!el.innerHTML || !el.innerHTML.trim()) {
        el.innerHTML = '<div class="meta" style="padding:12px;color:#666;">此会话没有可显示的文本</div>';
      }
      try { hljs.highlightAll(); } catch(e) {}
    }

    function tryString(v){ if(typeof v==='string') return v; try{ return JSON.stringify(v, null, 2)}catch(e){return ''}}

    function renderContent(m){
      var md = '';
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
      } else if (m && m.raw && typeof m.raw.text === 'string') {
        md = m.raw.text;
      } else {
        return '';
      }
      try { return DOMPurify.sanitize(marked.parse(md)); } catch(e) { return escapeHTML(md); }
    }

    function escapeHTML(s){ return (s||'').toString().replace(/[&<>"']/g, function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;','\'':'&#39;'}[c]||c;}) }
    let onlyText = false;
    let viewMode = 'time-cwd'; // 'cwd-time' | 'time-cwd' | 'flat'
    let sessionsCache = [];
    function toggleOnlyText(v){ onlyText = !!v; renderSessions(sessionsCache); }
    function setViewMode(v){ viewMode = v; try{ localStorage.setItem('viewMode', viewMode); }catch(e){} renderSessions(sessionsCache); }

    function getCollapsed(key){ try{ return (localStorage.getItem('collapsed:'+key)||'0')==='1'; }catch(e){ return false; } }
    function setCollapsed(key, val){ try{ localStorage.setItem('collapsed:'+key, val?'1':'0'); }catch(e){} }
    function isBucketKey(key){ return key && key.indexOf('bucket:')===0 && key.indexOf(':cwd:')===-1 }
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
      renderSessions(sessionsCache);
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
    function renderSessions(list){
      sessionsCache = Array.isArray(list) ? list : [];
      const all = sessionsCache;
      const filtered = onlyText ? all.filter(function(it){ return (it.text_count||0) > 0; }) : all;
      const hidden = all.length - filtered.length;
      const hint = document.getElementById('hiddenHint');
      if (hint) hint.textContent = (onlyText && hidden>0) ? ('隐藏 ' + hidden + ' 个无文本会话') : '';
      const s = document.getElementById('sessions');
      function parseDateSafe(v){ var d=new Date(v); return isNaN(d)? null : d; }
      function endAtOf(it){ var a=parseDateSafe(it.last_at), b=parseDateSafe(it.file_mod_at); if(a&&b) return a>b?a:b; return a||b; }
      function fmtStartCountDur(it){
        var start = parseDateSafe(it.first_at);
        var end = endAtOf(it) || start;
        var count = (onlyText ? (it.text_count||0) : (it.message_count||0));
        var startStr = start? start.toLocaleString() : '';
        var durMs = (start && end) ? (end - start) : 0;
        function human(ms){ if(ms<=0) return '0s'; var s=Math.floor(ms/1000); var d=Math.floor(s/86400); s%=86400; var h=Math.floor(s/3600); s%=3600; var m=Math.floor(s/60); s%=60; var out=[]; if(d) out.push(d+'d'); if(h) out.push(h+'h'); if(m) out.push(m+'m'); if(s && out.length<2) out.push(s+'s'); return out.join(' ')||'0s'; }
        return startStr + ' · ' + count + ' msgs · ' + human(durMs);
      }
      if(viewMode === 'flat'){
        s.innerHTML = filtered.map(function(it){
          var pills = Object.keys(it.models||{}).map(function(m){ return '<span class="pill">'+m+'</span>'; }).join('');
          var meta = fmtStartCountDur(it);
          return '<div class="item" data-id="' + it.id + '" onclick="selectSession(\'' + it.id + '\')">'
            + '<div class="meta">' + meta + '</div>'
            + '<div class="meta">' + pills + '</div>'
            + '</div>';
        }).join('');
        var first = s.querySelector('.item');
        if (first && first.dataset && first.dataset.id) { selectSession(first.dataset.id); }
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
        var first2 = s.querySelector('.group .item[data-id]');
        if (first2 && first2.dataset && first2.dataset.id) { selectSession(first2.dataset.id); }
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
        var first3 = s.querySelector('.group .item[data-id]');
        if (first3 && first3.dataset && first3.dataset.id) { selectSession(first3.dataset.id); }
      }
    }
    window.addEventListener('load', ()=>{
      onlyText = false;
      try{ viewMode = localStorage.getItem('viewMode') || 'time-cwd'; }catch(e){ viewMode='time-cwd'; }
      var tgl = document.getElementById('onlyTextToggle');
      if (tgl) tgl.checked = onlyText;
      var sel = document.getElementById('viewModeSelect');
      if (sel) sel.value = viewMode;
      const init = JSON.parse(document.getElementById('init-sessions').textContent);
      renderSessions(init);
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
      视图
      <select id="viewModeSelect" onchange="setViewMode(this.value)" class="btn" style="padding:4px 6px;">
        <option value="time-cwd">时间 → 目录</option>
        <option value="cwd-time">目录 → 时间</option>
        <option value="flat">扁平</option>
      </select>
    </label>
    <label class="meta" style="margin-right:8px; display:flex; align-items:center; gap:6px;">
      <input type="checkbox" id="onlyTextToggle" checked onchange="toggleOnlyText(this.checked)">
      仅文本
    </label>
    <div id="hiddenHint" class="meta" style="margin-right:12px;"></div>
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
