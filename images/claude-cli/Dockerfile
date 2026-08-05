# OSB image — the `claude` CLI as a sandboxed inference backend (ADR-0018).
#
# Published multi-arch to ghcr.io/opensecbench/claude-cli by .github/workflows/images.yml and referenced via
# OSB_LLM_CLI_IMAGE (default: ghcr.io/opensecbench/claude-cli:<CLAUDE_VERSION>). Nothing sensitive is baked in — at
# runtime the sandbox mounts ONLY ~/.claude/.credentials.json (read-only) at /root/.claude and grants the
# container egress so the CLI can reach the Anthropic API.
#
#   Build:      make claude-image
#   Or:         docker build -t osb/claude-cli:latest images/claude-cli
#   Pin the CLI: docker build --build-arg CLAUDE_VERSION=<x.y.z> -t osb/claude-cli:<x.y.z> images/claude-cli
#
# The runner invokes `claude -p …` directly (no ENTRYPOINT); the CLI is on PATH via the global npm install.
FROM node:25-slim

# ca-certificates for TLS to the API; git because the CLI expects it available on PATH.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git \
 && rm -rf /var/lib/apt/lists/*

# The Claude Code CLI (npm). Pinned for reproducibility (the rest of OSB pins every image). Bump this to
# update — the publish workflow derives the image tag from it and rebuilds on any images/** change.
ARG CLAUDE_VERSION=2.1.222
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_VERSION} \
 && claude --version

LABEL org.opensecbench.image="claude-cli" \
      org.opensecbench.adr="ADR-0018"

# HOME must match the sandbox HomeDir (default /root); the credential is mounted at $HOME/.claude.
ENV HOME=/root
CMD ["claude", "--version"]
