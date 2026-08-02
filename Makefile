# OpenSecBench developer tasks.
#
# The desktop app (main.go) is behind the `desktop` build tag so plain `go build ./...` and CI
# don't need the webkit/gtk toolchain. Wails must be told that tag explicitly — these targets do
# it for you. The Analyst's AI provider is configured in the app's settings — no env vars needed.

.PHONY: dev dev-attach build gui tui cli daemon run-daemon test lint fmt frontend images claude-image adr-index

# Wails build tags. The `webkit2_41` tag selects webkit2gtk-4.1 and is LINUX-ONLY — macOS (native
# WebKit) and Windows (WebView2) must not get it, so we only add it on Linux. On modern distros
# (Ubuntu/Pop!_OS 24.04+) webkit is 4.1; on older distros that ship webkit2gtk-4.0, override with
# `WAILS_TAGS=desktop`. You can always override WAILS_TAGS explicitly on the command line.
ifeq ($(OS),Windows_NT)
  WAILS_TAGS ?= desktop
else ifeq ($(shell uname),Linux)
  WAILS_TAGS ?= desktop webkit2_41
else
  WAILS_TAGS ?= desktop
endif

# Live-reload desktop app (embeds its own control plane).
dev:
	wails dev -tags "$(WAILS_TAGS)"

# Live-reload desktop app attached to a separately-run `make run-daemon` (OSB_API), so the backend can
# be restarted independently of the window. Reads the daemon's token from the default data dir.
dev-attach:
	OSB_API=http://127.0.0.1:7373 wails dev -tags "$(WAILS_TAGS)"

# Build everything: the desktop GUI, the osb CLI/TUI, and the headless daemon.
build: gui tui daemon

# Desktop GUI (Wails) → ./build/bin. Needs the webkit/gtk toolchain and a built frontend.
gui:
	wails build -tags "$(WAILS_TAGS)"

# osb CLI + TUI → ./bin/osb. One binary: `osb <command>` is the CLI; bare `osb` (or `osb tui`) opens
# the terminal UI. `tui` and `cli` build the same binary.
tui cli:
	go build -o bin/osb ./cmd/osb

# Headless control-plane binary → ./bin/daemon (no desktop toolchain needed).
daemon:
	go build -o bin/daemon ./cmd/daemon

# Run the headless control plane in the foreground (pair with `make dev-attach`).
run-daemon:
	go run ./cmd/daemon

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
