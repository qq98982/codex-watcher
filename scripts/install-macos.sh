#!/usr/bin/env bash
set -euo pipefail

# Install codex-watcher on macOS (Apple Silicon friendly)
# - Builds the binary for darwin/arm64
# - Stages it under /opt/codex-watcher with the static/ assets
# - Drops a small wrapper into a bin dir on PATH (prefers /opt/homebrew/bin)

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="${APP_DIR:-/opt/codex-watcher}"

# Choose install bin directory: prefer Homebrew path on Apple Silicon, fallback to /usr/local/bin
if [[ -d "/opt/homebrew/bin" ]]; then
  BIN_DIR_DEFAULT="/opt/homebrew/bin"
else
  BIN_DIR_DEFAULT="/usr/local/bin"
fi
BIN_DIR="${BIN_DIR:-$BIN_DIR_DEFAULT}"
WRAPPER_PATH="$BIN_DIR/codex-watcher"

echo "==> Building codex-watcher for macOS (arm64)"
mkdir -p "$REPO_ROOT/bin"
GOOS=darwin GOARCH=arm64 go build -o "$REPO_ROOT/bin/codex-watcher" ./cmd/codex-watcher

echo "==> Staging app into $APP_DIR"
sudo mkdir -p "$APP_DIR"
sudo cp "$REPO_ROOT/bin/codex-watcher" "$APP_DIR/"
sudo rsync -a --delete "$REPO_ROOT/static/" "$APP_DIR/static/"

echo "==> Installing wrapper into $WRAPPER_PATH"
sudo mkdir -p "$BIN_DIR"
sudo tee "$WRAPPER_PATH" >/dev/null <<'SH'
#!/usr/bin/env sh
set -e
APP_DIR="/opt/codex-watcher"
cd "$APP_DIR"
exec ./codex-watcher "$@"
SH
sudo chmod +x "$WRAPPER_PATH"

echo "==> Install complete"
echo "Binary:   $APP_DIR/codex-watcher"
echo "Assets:   $APP_DIR/static/"
echo "Wrapper:  $WRAPPER_PATH"
echo
echo "Run:      codex-watcher start --host 127.0.0.1 --port 7077 --codex \"$HOME/.codex\""
echo "Open:     codex-watcher browse"

