# ADR-0004 — Runner protocol & sandboxing

Status: Accepted

## Context

Capabilities execute potentially dangerous security tooling against sensitive targets. Execution
must be isolated from the host, produce artifacts with full provenance (ADR-0002), and be
portable to remote runners later without reworking the model. In P2 the only runner is the local
machine, but the task envelope and runner interface are designed so a remote outbound-connect
runner is an additive implementation.

## Decision

**A task is a single capability invocation.** It records what ran (capability id + version),
where (runner), against what (asset/application), by whom (`actor`: human or a thread), and its
outcome (status, exit code, timing). Outputs are captured as immutable artifacts in the CAS and
linked to the task, giving the provenance chain `artifact → task → capability+version, runner`.

**The runner interface is small and transport-agnostic:**

```
RunSpec{ Image, Cmd, Env, Mounts[], Network, Workdir, Timeout, MemoryMB, CPUs }
Result{ ExitCode, Stdout, Stderr, Duration }
Runner{ Run(ctx, RunSpec) (Result, error); Name() string }
```

**`LocalRunner` runs each capability in a container** via the Docker CLI with sandboxing defaults:

- `--rm` (ephemeral), `--network none` by default (opt-in network per capability permissions),
- target sources mounted **read-only**, a writable scratch workdir only where required,
- memory and CPU caps, and a wall-clock timeout (context cancellation kills the container),
- immutable, digest-pinned images (a capability's manifest declares its image).

The runner captures stdout/stderr and the exit code; the capability decides which stream/file
becomes an output artifact (e.g. Semgrep's SARIF on stdout).

## Consequences

- Sandboxing is the runner's responsibility and is enforced uniformly for both human- and
  agent-initiated tasks. The agent gets no path around it (ADR-0001).
- A capability's declared permissions (network/fs/secrets) become concrete `RunSpec` fields; the
  default-deny posture (no network, read-only mounts) is the safe baseline.
- Remote execution is a future `Runner` implementation (outbound gRPC stream) behind the same
  interface; the task/artifact model does not change.
- P2 shells out to the `docker` CLI for simplicity and auditability; swapping to the Docker SDK
  or another engine is internal to `LocalRunner`.
- Scope enforcement and secret injection (later phases) hook into `RunSpec` construction; they are
  out of scope for P2 but the shape anticipates them.
