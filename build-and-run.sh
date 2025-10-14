#!/bin/bash
# Codex Watcher - Build and Run Script
# This script builds and runs codex-watcher using local Go installation

set -e  # Exit on error

# Configuration
GO_BIN="/usr/local/go/bin/go"
BIN_DIR="bin"
BIN_NAME="codex-watcher"
BIN_PATH="${BIN_DIR}/${BIN_NAME}"

# Workspace-local caches
GOCACHE="$(pwd)/.gocache"
GOMODCACHE="$(pwd)/.gomodcache"

# Server configuration
PORT="${PORT:-7077}"
CODEX_DIR="${CODEX_DIR:-$HOME/.codex}"
CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude/projects}"
HOST="${HOST:-0.0.0.0}"

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}==== Codex Watcher Build Script ====${NC}"

# Check if Go is installed
if [ ! -f "$GO_BIN" ]; then
    echo -e "${YELLOW}Error: Go not found at $GO_BIN${NC}"
    exit 1
fi

echo -e "${GREEN}✓ Go found: $($GO_BIN version)${NC}"

# Create directories
mkdir -p "$BIN_DIR" "$GOCACHE" "$GOMODCACHE"

# Build
echo -e "\n${BLUE}Building...${NC}"
GOCACHE="$GOCACHE" GOMODCACHE="$GOMODCACHE" "$GO_BIN" build -o "$BIN_PATH" ./cmd/codex-watcher

if [ $? -eq 0 ]; then
    BIN_SIZE=$(ls -lh "$BIN_PATH" | awk '{print $5}')
    echo -e "${GREEN}✓ Build successful! Binary size: $BIN_SIZE${NC}"
else
    echo -e "${YELLOW}✗ Build failed${NC}"
    exit 1
fi

# Run tests (optional)
if [ "$1" == "--test" ]; then
    echo -e "\n${BLUE}Running tests...${NC}"
    GOCACHE="$GOCACHE" GOMODCACHE="$GOMODCACHE" "$GO_BIN" test ./... -v
    exit 0
fi

# Check if service is already running and stop it automatically
EXISTING_PID=$(ps aux | grep "$BIN_NAME serve" | grep -v grep | awk '{print $2}')
if [ ! -z "$EXISTING_PID" ]; then
    echo -e "\n${YELLOW}Service already running (PID: $EXISTING_PID)${NC}"
    echo -e "${BLUE}Stopping existing service...${NC}"
    kill $EXISTING_PID
    sleep 2
    echo -e "${GREEN}✓ Service stopped${NC}"
fi

# Run server
echo -e "\n${BLUE}Starting server...${NC}"
echo -e "${GREEN}Configuration:${NC}"
echo -e "  Port:       $PORT"
echo -e "  Host:       $HOST"
echo -e "  Codex Dir:  $CODEX_DIR"
echo -e "  Claude Dir: $CLAUDE_DIR"
echo -e "\n${GREEN}Server URL: http://localhost:$PORT${NC}"
echo -e "${YELLOW}Press Ctrl+C to stop${NC}\n"

# Run in foreground (to see logs)
"$BIN_PATH" serve --port "$PORT" --codex "$CODEX_DIR" --claude "$CLAUDE_DIR" --host "$HOST"
