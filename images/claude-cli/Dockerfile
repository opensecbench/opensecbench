# OSB image — the `claude` CLI as a sandboxed inference backend (ADR-0018).
#
# This is the only image OpenSecBench builds itself; everything else pulls pinned public images. It is
# referenced via OSB_LLM_CLI_IMAGE (default: osb/claude-cli:latest). Nothing sensitive is baked in — at
# runtime the sandbox mounts ONLY ~/.claude/.credentials.json (read-only) at /root/.claude and grants the
# container egress so the CLI can reach the Anthropic API.
#
#   Build:      make claude-image
#   Or:         docker build -t osb/claude-cli:latest images/claude-cli
#   Pin the CLI: docker build --build-arg CLAUDE_VERSION=<x.y.z> -t osb/claude-cli:<x.y.z> images/claude-cli
#
# The runner invokes `claude -p …` directly (no ENTRYPOINT); the CLI is on PATH via the global npm install.
FROM node:22-slim

# ca-certificates for TLS to the API; git because the CLI expects it available on PATH.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git \
 && rm -rf /var/lib/apt/lists/*

# The Claude Code CLI (npm). Defaults to 'latest'; pin to your local `claude --version` for reproducibility
# (the rest of OSB pins every image — do the same here in a real deployment).
ARG CLAUDE_VERSION=latest
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_VERSION} \
 && claude --version

LABEL org.opensecbench.image="claude-cli" \
      org.opensecbench.adr="ADR-0018"

# HOME must match the sandbox HomeDir (default /root); the credential is mounted at $HOME/.claude.
ENV HOME=/root
CMD ["claude", "--version"]
