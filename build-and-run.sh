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

# Arguments
RUN_TESTS=0
PRUNE_DRY_RUN=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --test)
            RUN_TESTS=1
            shift
            ;;
        --dry-run)
            PRUNE_DRY_RUN=1
            shift
            ;;
        --skip-prune)
            SKIP_WARMUP_PRUNE=1
            shift
            ;;
        --)
            shift
            break
            ;;
        *)
            echo "Unknown option: $1"
            echo "Supported: --test, --dry-run, --skip-prune"
            exit 1
            ;;
    esac
done

# Server configuration
PORT="${PORT:-7077}"
CODEX_DIR="${CODEX_DIR:-$HOME/.codex}"
CLAUDE_DIR="${CLAUDE_DIR:-$HOME/.claude/projects}"
HOST="${HOST:-0.0.0.0}"
PRUNE_SCRIPT="$(pwd)/scripts/prune-warmup-sessions.py"

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

# Optional warmup session pruning
if [ "${SKIP_WARMUP_PRUNE:-0}" != "1" ] && [ -x "$PRUNE_SCRIPT" ]; then
    PRUNE_MODE_MSG="Pruning stale warmup sessions (>24h)"
    if [ "$PRUNE_DRY_RUN" -eq 1 ]; then
        PRUNE_MODE_MSG="$PRUNE_MODE_MSG [dry-run]"
    fi
    echo -e "\n${BLUE}${PRUNE_MODE_MSG}...${NC}"
    PRUNE_ARGS=(--codex-dir "$CODEX_DIR" --claude-dir "$CLAUDE_DIR" --max-age-hours 24)
    if [ "$PRUNE_DRY_RUN" -eq 1 ]; then
        PRUNE_ARGS+=("--dry-run")
    fi
    if ! "$PRUNE_SCRIPT" "${PRUNE_ARGS[@]}"; then
        echo -e "${YELLOW}Warning: failed to prune warmup sessions${NC}"
    fi
fi

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
if [ "$RUN_TESTS" -eq 1 ]; then
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
