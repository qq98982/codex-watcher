#!/usr/bin/env bash
set -euo pipefail

# Uninstall codex-watcher from macOS

APP_DIR="${APP_DIR:-/opt/codex-watcher}"

BIN_CANDIDATES=(
  "/opt/homebrew/bin/codex-watcher"
  "/usr/local/bin/codex-watcher"
)

echo "==> Stopping service if running (best-effort)"
if command -v codex-watcher >/dev/null 2>&1; then
  codex-watcher stop || true
fi

echo "==> Removing wrapper from PATH (if present)"
for p in "${BIN_CANDIDATES[@]}"; do
  if [[ -f "$p" ]]; then
    # Only remove if this looks like our wrapper
    if grep -q "/opt/codex-watcher" "$p" 2>/dev/null; then
      echo "Removing $p"
      sudo rm -f "$p"
    else
      echo "Skipping $p (does not look like our wrapper)"
    fi
  fi
done

echo "==> Removing app directory $APP_DIR"
if [[ -d "$APP_DIR" ]]; then
  sudo rm -rf "$APP_DIR"
fi

echo "==> Uninstall complete"

