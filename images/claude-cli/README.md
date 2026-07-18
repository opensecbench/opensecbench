# `osb/claude-cli`

The `claude` CLI packaged as a sandboxed inference backend for the Analyst (ADR-0018). Lets you drive the
Analyst from a **Claude subscription** without handing the sandbox the rest of `~/.claude` or your host
environment — only the credential file is exposed, read-only, at runtime.

## Build

```sh
make claude-image
# or, pinning the CLI version to match your local `claude --version`:
docker build --build-arg CLAUDE_VERSION=<x.y.z> -t osb/claude-cli:latest images/claude-cli
```

## Use

```sh
export OSB_LLM_PROVIDER=claude-cli
export OSB_LLM_CLI_SANDBOX=1
# OSB_LLM_CLI_IMAGE defaults to osb/claude-cli:latest
# OSB_LLM_CLI_CREDENTIAL defaults to ~/.claude/.credentials.json
# OSB_LLM_CLI_NETWORK   defaults to "bridge" (egress so the CLI can reach the API)
```

At runtime the sandbox mounts **only** `~/.claude/.credentials.json` (read-only) into the container at
`/root/.claude/.credentials.json` and runs `claude -p --output-format json …`. Any other state the CLI writes
(`~/.claude/settings.json`, session files) lands in the container's ephemeral layer and is discarded — the
credential is the one thing that persists, and it's read-only.

## Notes

- Requires Docker and, obviously, a working `claude` login on the host that produced the credential file.
- **Egress is broad** (the container reaches the network to hit the API); narrowing it to just the API host
  is future work (runners-as-egress-endpoints). Set `OSB_LLM_CLI_NETWORK` to a locked-down docker network if
  you have one.
- API keys remain the recommended default for anyone not deliberately using a subscription this way.
