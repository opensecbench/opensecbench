# ADR-0018 — Sandboxed claude-cli inference provider

Status: Accepted — delivered. The `claude` CLI can run inside a runner container that mounts **only** the
credential file, so a Claude subscription is usable as an inference backend without handing the sandbox the
rest of `~/.claude` or the host environment. Opt-in; the direct-exec path stays the default.

## Context

`CLIProvider` (ADR-0006, ADR-0017) drives `claude -p --output-format json` as a plain completion backend —
the cheapest way to test the Analyst, and how James personally runs it against a Claude subscription rather
than an API key. Today it execs the `claude` binary directly on the host. That inherits the caller's whole
ambient environment and their entire `~/.claude` directory (settings, project history, MCP config, OAuth
state — not just the credential), and its network egress is unconstrained. For a governed setup we want the
opposite: the CLI should see **only** what it needs to authenticate and talk to the API.

The building block already exists: `pkg/runner` runs a command in an ephemeral Docker container with
explicit mounts, a network policy, secret-env injection, and resource limits (ADR-0004, ADR-0011). It was
built for capabilities, but a one-shot `claude -p` is just another sandboxed command — with two gaps: the
runner had no **stdin** (the CLI takes the conversation on stdin), and nothing wired the CLI provider to it.

## Decision

Add an **opt-in sandbox mode** to `CLIProvider`. When configured, `Complete` builds the same args as the
host path (`--output-format json`, `--disallowed-tools …`, `--append-system-prompt …`) but runs the CLI via
a `runner.Runner` instead of `os/exec`, and parses the container's stdout identically. The direct-exec path
is unchanged and remains the default.

1. **Credential-only mount.** The sandbox mounts exactly one host path — `~/.claude/.credentials.json` —
   read-only, at `$HOME/.claude/.credentials.json` inside the container. Nothing else from `~/.claude` or the
   host is exposed. The mount source (and container HOME) are configurable; the source defaults to the
   caller's `~/.claude/.credentials.json`.
2. **Egress, not isolation.** Capabilities default to `--network none`; this provider is the deliberate
   exception — it *must* reach the Anthropic API, so the sandbox uses an egress-capable network (default
   `bridge`), configurable. True per-destination egress policy (allow only the API host) is a refinement
   that rides on the future runners-as-egress-endpoints work (TODO), not this ADR.
3. **Stdin for the runner.** `RunSpec` gains `Stdin []byte`; `LocalRunner` attaches it (`docker run -i`) and
   feeds it to the container. This is a general runner capability, not CLI-specific.
4. **Config-driven, opt-in.** `Config.CLISandbox` (env `OSB_LLM_CLI_SANDBOX=1`) turns it on, with
   `CLIImage` / `CLICredential` / `CLINetwork` (and matching `OSB_LLM_CLI_*` env) to point at an image that
   has the `claude` CLI installed and the credential path. Off → the existing host exec. The image is the
   operator's to provide/build; we don't ship one.

## Consequences

- **Least privilege for subscription auth.** The CLI authenticates from the one file it needs; the host
  profile, other credentials, and the ambient environment are no longer in reach of the sandboxed process.
- **Reuses the audited sandbox.** Mounts, network policy, resource limits, and secret handling come from the
  existing runner — no second execution path to secure. `Stdin` is a clean, general addition.
- **Requires Docker + a CLI image.** Sandbox mode needs the Docker runtime and an image containing `claude`;
  without them the operator stays on the (default) host path. API keys remain the recommended default for
  everyone who isn't deliberately using a subscription this way.
- **Egress is broad for now.** The sandbox can reach the network to hit the API; narrowing that to just the
  API host is deferred to the egress-proxy work. Documented, not hidden.

Extends ADR-0006 (CLI as a completion backend) and composes with ADR-0004 (sandbox) and ADR-0011 (secret
handling). Coordinates with ADR-0017 (this is one provider behind the tool-aware layer; the CLI path is
always the prompted fallback).
