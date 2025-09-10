BIN_DIR := bin
BIN := $(BIN_DIR)/codex-watcher
PORT ?= 7077
CODEX_DIR ?= $(HOME)/.codex

.PHONY: all build test vet check run start stop restart reload status browse open health clean

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/codex-watcher

test:
	go test ./...

vet:
	go vet ./...

check: vet test build

run: build
	$(BIN) --port $(PORT) --codex $(CODEX_DIR)

start: build
	$(BIN) start --port $(PORT) --codex $(CODEX_DIR)

stop:
	@if [ -x "$(BIN)" ]; then \
		$(BIN) stop --codex $(CODEX_DIR); \
	else \
		go run ./cmd/codex-watcher stop --codex "$(CODEX_DIR)"; \
	fi

restart: build
	$(BIN) restart --port $(PORT) --codex $(CODEX_DIR)

status:
	@if [ -x "$(BIN)" ]; then \
		$(BIN) status --codex $(CODEX_DIR); \
	else \
		go run ./cmd/codex-watcher status --codex "$(CODEX_DIR)"; \
	fi

# One-shot: stop -> build -> start
reload: stop build start

# Ensure server is running, then open browser
browse: build
	$(BIN) browse --port $(PORT) --codex $(CODEX_DIR)

# Back-compat alias
open: browse

# Quick health check
health:
	@echo "GET /api/stats" && curl -sS http://localhost:$(PORT)/api/stats | sed -n '1,200p'

clean:
	rm -rf $(BIN_DIR)
