BIN_DIR := bin
BIN := $(BIN_DIR)/codex-watcher

.PHONY: all build test vet run clean

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/codex-watcher

test:
	go test ./...

vet:
	go vet ./...

run: build
	PORT?=7077 CODEX_DIR?=$(HOME)/.codex $(BIN) --port $${PORT} --codex $${CODEX_DIR}

clean:
	rm -rf $(BIN_DIR)

