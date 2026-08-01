# OpenSecBench developer tasks.
#
# The desktop app (main.go) is behind the `desktop` build tag so plain `go build ./...` and CI
# don't need the webkit/gtk toolchain. Wails must be told that tag explicitly — these targets do
# it for you. Set a provider to enable the Analyst, e.g. `OSB_LLM_PROVIDER=claude-cli make dev`.

.PHONY: dev build daemon cli test lint fmt frontend images claude-image adr-index

# Wails build tags. On modern distros (Ubuntu/Pop!_OS 24.04+) webkit is 4.1, which needs the
# webkit2_41 tag. On older distros that ship webkit2gtk-4.0, override: WAILS_TAGS=desktop.
WAILS_TAGS ?= desktop webkit2_41

# Live-reload desktop app.
dev:
	wails dev -tags "$(WAILS_TAGS)"

# Package a desktop binary into ./build/bin.
build:
	wails build -tags "$(WAILS_TAGS)"

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

# OSB-built container images (see images/README.md). Each images/<name>/ builds to osb/<name>:latest.
# The image-% pattern auto-discovers dirs, so a new image needs no Makefile change.
IMAGES := $(patsubst images/%/,%,$(wildcard images/*/))

images: $(addprefix image-,$(IMAGES))

image-%:
	docker build -t osb/$*:latest images/$*

# Convenience alias for the one image most people build.
claude-image: image-claude-cli

# Regenerate the ADR index table in docs/adr/README.md from each ADR's title + Status line.
# CI runs this and fails if the committed index is stale (see .github/workflows/ci.yml).
adr-index:
	go run scripts/gen_adr_index.go
