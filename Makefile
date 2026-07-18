# OpenSecBench developer tasks.
#
# The desktop app (main.go) is behind the `desktop` build tag so plain `go build ./...` and CI
# don't need the webkit/gtk toolchain. Wails must be told that tag explicitly — these targets do
# it for you. Set a provider to enable the Analyst, e.g. `OSB_LLM_PROVIDER=claude-cli make dev`.

.PHONY: dev build daemon cli test lint fmt frontend

# Live-reload desktop app.
dev:
	wails dev -tags desktop

# Package a desktop binary into ./build/bin.
build:
	wails build -tags desktop

# Headless control plane (no desktop toolchain needed).
daemon:
	go run ./cmd/daemon

# Build the CLI.
cli:
	go build -o bin/osb ./cmd/osb

test:
	go test ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

# Build the frontend once (creates frontend/dist).
frontend:
	cd frontend && npm install && npm run build
