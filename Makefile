BIN_DIR := bin
BIN := $(BIN_DIR)/codex-watcher
PORT ?= 7077
CODEX_DIR ?= $(HOME)/.codex

.PHONY: all build test vet check run start stop restart status clean

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

clean:
	rm -rf $(BIN_DIR)
