#!/usr/bin/env python3
"""
Prune stale warmup-only Codex/Claude sessions.

Criteria:
- Session file older than the configured cutoff (24h by default).
- Only contains a single user "Warmup" message followed by a short assistant reply.
  These sessions are typically automatically generated warmup chats that are not useful.
"""

import argparse
import json
import os
import sys
import time
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Delete stale warmup-only Codex/Claude sessions.")
    parser.add_argument(
        "--codex-dir",
        default=os.environ.get("CODEX_DIR", os.path.expanduser("~/.codex")),
        help="Codex directory containing sessions/ (default: %(default)s)",
    )
    parser.add_argument(
        "--claude-dir",
        default=os.environ.get("CLAUDE_DIR", os.path.expanduser("~/.claude/projects")),
        help="Claude projects directory containing per-project JSONL files (default: %(default)s)",
    )
    parser.add_argument(
        "--max-age-hours",
        type=float,
        default=float(os.environ.get("WARMUP_MAX_AGE_HOURS", 24)),
        help="Only delete sessions older than this many hours (default: %(default)s)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Show what would be deleted without removing files",
    )
    parser.add_argument(
        "--quiet",
        action="store_true",
        help="Suppress informational output",
    )
    return parser.parse_args()


def extract_role_and_text(obj: dict):
    """Extract (role, text) from mixed Codex JSONL payloads."""
    payload = obj.get("payload")
    role = None
    content = None
    if isinstance(payload, dict):
        role = payload.get("role")
        content = payload.get("content")
        if role is None and isinstance(payload.get("message"), dict):
            message = payload["message"]
            role = message.get("role")
            content = message.get("content")
    else:
        role = obj.get("role")
        content = obj.get("content")
        if role is None and isinstance(obj.get("message"), dict):
            message = obj["message"]
            role = message.get("role")
            content = message.get("content")

    text = ""
    if isinstance(content, str):
        text = content
    elif isinstance(content, list):
        parts = []
        for item in content:
            if not isinstance(item, dict):
                continue
            if isinstance(item.get("text"), str):
                parts.append(item["text"])
            elif isinstance(item.get("content"), str):
                parts.append(item["content"])
        text = "\n".join(parts)
    elif isinstance(content, dict):
        parts = []
        for key in ("text", "content"):
            value = content.get(key)
            if isinstance(value, str):
                parts.append(value)
        text = "\n".join(parts)

    return role, text


def is_env_context(text: str) -> bool:
    stripped = (text or "").strip()
    if not stripped:
        return False
    if stripped.startswith("<environment_context>"):
        return True
    # Some builds tag warmup content explicitly.
    lowered = stripped.lower()
    if "warmup" in lowered and "environment" in lowered:
        return True
    normalized = "".join(ch for ch in lowered if ch.isalpha())
    if normalized in {"warmup", "warmups"}:
        return True
    return False


def collect_stats(path: Path) -> dict:
    stats = {
        "user_messages": 0,
        "assistant_messages": 0,
        "non_env_user_messages": 0,
    }
    try:
        with path.open("r", encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    continue
                role, text = extract_role_and_text(obj)
                if role == "user":
                    stats["user_messages"] += 1
                    if not is_env_context(text):
                        stats["non_env_user_messages"] += 1
                elif role == "assistant":
                    stats["assistant_messages"] += 1
                # Early exit once we know this is not a warmup.
                if stats["non_env_user_messages"] > 0 or stats["assistant_messages"] > 2:
                    break
    except OSError:
        return stats
    return stats


def is_warmup_session(path: Path) -> bool:
    stats = collect_stats(path)
    if stats["user_messages"] == 0:
        return False
    if stats["non_env_user_messages"] > 0:
        return False
    # Allow an optional short assistant acknowledgement, but not more.
    if stats["assistant_messages"] > 1:
        return False
    return True


def session_files(codex_dir: str, claude_dir: str):
    if codex_dir:
        sessions_dir = Path(codex_dir).expanduser() / "sessions"
        if sessions_dir.is_dir():
            for path in sessions_dir.rglob("*.jsonl"):
                yield "codex", path
    if claude_dir:
        claude_root = Path(claude_dir).expanduser()
        if claude_root.is_dir():
            for path in claude_root.rglob("*.jsonl"):
                yield "claude", path


def prune_sessions(args: argparse.Namespace) -> int:
    cutoff = time.time() - args.max_age_hours * 3600
    deleted = 0

    for provider, path in session_files(args.codex_dir, args.claude_dir):
        try:
            stat = path.stat()
        except OSError:
            continue
        if stat.st_mtime >= cutoff:
            continue

        if not is_warmup_session(path):
            continue

        if args.dry_run:
            if not args.quiet:
                print(f"[dry-run] Would delete {provider} warmup session: {path}")
            deleted += 1
            continue

        try:
            path.unlink()
            deleted += 1
            if not args.quiet:
                print(f"Deleted {provider} warmup session: {path}")
        except OSError as exc:
            if not args.quiet:
                print(f"Failed to delete {path}: {exc}", file=sys.stderr)

    return deleted


def main():
    args = parse_args()
    removed = prune_sessions(args)
    if not args.quiet:
        action = "would remove" if args.dry_run else "removed"
        print(f"{action.capitalize()} {removed} warmup session(s).")


if __name__ == "__main__":
    main()
