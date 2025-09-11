Codex Watcher (Local Background Service)

Overview

- Watches `~/.codex` for changes to `history.jsonl` and `sessions/*.jsonl`.
- Parses JSONL chat events and exposes a local HTTP API + a minimal UI.
- Default port: `localhost:7077` (configurable via `PORT`).
- Default Codex dir: `$HOME/.codex` (override with `CODEX_DIR`).

Notes

- This initial version uses polling (no external deps) to detect file appends.
- It incrementally tails JSONL files and indexes messages in-memory.
- Unknown/extra JSON fields are preserved in a `raw` blob for later analysis.

Run

```bash
go run ./cmd/codex-watcher
# or
PORT=8081 CODEX_DIR=/path/to/.codex go run ./cmd/codex-watcher
# Bind host (default 127.0.0.1)
HOST=0.0.0.0 go run ./cmd/codex-watcher --host 0.0.0.0

# Search tunables
go run ./cmd/codex-watcher --search_budget_ms 500 --search_max 300
```

Usage

```text
codex-watcher [flags]
codex-watcher serve [flags]           # same as default
codex-watcher browse [flags]          # ensure running, then open browser
codex-watcher start|stop|restart [flags]

Flags (with env var equivalents)
  --host <host>               Bind address (default 0.0.0.0)
    env: HOST
  --port <port>               HTTP port (default 7077)
    env: PORT
  --codex <dir>               Path to ~/.codex (default $HOME/.codex)
    env: CODEX_DIR
  --search_budget_ms <ms>     Soft time budget for /api/search (default 350)
  --search_max <n>            Maximum hits returned (default 200)

Examples
  # foreground
  codex-watcher --host 0.0.0.0 --port 7077 --codex "$HOME/.codex"

  # background service (simple)
  codex-watcher start --host 0.0.0.0 --port 7077 --codex "$HOME/.codex"
  codex-watcher browse   # open UI
  codex-watcher stop
  codex-watcher status
  
Notes
- The binary serves static files from a local `static/` folder; run from the repo root or keep `static/` adjacent to the binary when deploying (e.g., `/opt/codex-watcher/{codex-watcher,static/}`).
- The default host is `0.0.0.0` (listens on all interfaces). If you prefer local-only, run with `--host 127.0.0.1`.
```

API

- `GET /api/sessions` — list discovered sessions with basic stats.
- `GET /api/messages?session_id=...` — messages for a session (latest 200 by default).
- `GET /api/stats` — aggregate counters (messages, sessions, roles, models if present).
- `POST /api/reindex` — trigger full rescan (lightweight for initial setup).

Export parameters (selected)

- `GET /api/export/session?session_id=...&format=jsonl|json|md|txt&exclude_shell=0|1&exclude_tool_outputs=0|1`
- `GET /api/export/by_dir?cwd=...&after=RFC3339&before=RFC3339&exclude_shell=0|1&exclude_tool_outputs=0|1` (markdown)
  - Defaults: exclude_shell=1, exclude_tool_outputs=1

UI

- `GET /` — Minimal HTMX-based view listing sessions and messages.
- Designed to work without Node tooling or bundlers.

Data Model (flexible)

- Session (id, title, first_at, last_at, message_count, models, tags)
- Message (id, session_id, ts, role, content, model, type, tool_name, raw)
- The parser attempts to map common fields; anything else is kept in `raw`.

Assumptions About ~/.codex Structure

- `~/.codex/history.jsonl` — global linear log of events/messages.
- `~/.codex/sessions/*.jsonl` — per-session logs. Each line is a JSON object.
- If the real schema differs, the parser is resilient and stores the full `raw` JSON.

Next Steps

- If you can share 10–20 sample lines from both `history.jsonl` and a `sessions/*.jsonl`,
  I can refine field mapping (e.g., tokens, cost, tools, errors, attachments, model, etc.).
