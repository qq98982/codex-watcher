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
```

API

- `GET /api/sessions` — list discovered sessions with basic stats.
- `GET /api/messages?session_id=...` — messages for a session (latest 200 by default).
- `GET /api/stats` — aggregate counters (messages, sessions, roles, models if present).
- `POST /api/reindex` — trigger full rescan (lightweight for initial setup).

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

