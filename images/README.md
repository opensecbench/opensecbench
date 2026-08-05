# OSB container images

Images that OpenSecBench **builds itself**. Everything else — capability and session sandboxes — pulls
**pinned public images** on demand (`alpine:3`, `anchore/grype:v0.80.2`, …); those need nothing here. This
directory is only for the rare case where no suitable public image exists and we must build one.

These are **published multi-arch (amd64 + arm64) to GHCR** (`ghcr.io/opensecbench/<name>`) by
[`.github/workflows/images.yml`](../.github/workflows/images.yml), so any host — a remote runner, a fresh
laptop — pulls them on demand exactly like the public images. `make images` is for local iteration only.

## Convention

- One directory per image: `images/<name>/Dockerfile` (+ a short `README.md` for that image).
- **Code references the published ref** `ghcr.io/opensecbench/<name>:<tag>`, overridable by an env var for
  local dev or a private mirror:
  | Image | Default reference | Override |
  |-------|-------------------|----------|
  | opengrep | `ghcr.io/opensecbench/opengrep:v1.25.0` | `OSB_OPENGREP_IMAGE` |
  | claude-cli | `ghcr.io/opensecbench/claude-cli:2.1.222` | `OSB_LLM_CLI_IMAGE` |
- **Multi-arch.** Fetch arch-specific assets by BuildKit's `TARGETARCH` (see opengrep's Dockerfile) so the
  manifest serves both amd64 and arm64 (Apple Silicon) natively — no emulation.
- **Pin the version in the Dockerfile.** Each image pins its tool version in a single ARG (`OPENGREP_VERSION`,
  `CLAUDE_VERSION`) — the one place to bump. The publish workflow derives the image tag from that ARG, so the
  published version tag always tracks the Dockerfile; keep the code reference above in step when you bump.
- Nothing sensitive is baked in. Credentials and data arrive at **runtime** via the runner's read-only
  mounts and secret-env, never in an image layer (ADR-0011).

## Publishing

**Automatic:** any push to `main` that touches `images/**` (or the workflow) rebuilds and publishes both
images — so bumping a pinned version in a Dockerfile is all it takes. Or run it **manually**: Actions ▸
publish images ▸ Run workflow (pick an image, or `all`). Either way it builds each `images/<name>` multi-arch
and pushes `ghcr.io/opensecbench/<name>:{version,latest}` using the repo's `GITHUB_TOKEN` — no extra secret.

> **First publish:** GHCR packages start **private**. Set each package's visibility to **Public**
> (org ▸ Packages ▸ the package ▸ settings) so unauthenticated runners can pull it; otherwise runners need
> a registry login.

## Building (local dev)

```sh
make images           # build every images/<name> as osb/<name>:latest
make image-opengrep   # build just one

# Then point the app at the local build instead of GHCR:
export OSB_OPENGREP_IMAGE=osb/opengrep:latest
```

The `image-%` target auto-discovers directories here, so a new image is just a new `images/<name>/`
directory plus its Dockerfile — no Makefile change needed.

## Images

| Image | Published reference | Purpose | ADR |
|-------|---------------------|---------|-----|
| [`opengrep`](opengrep/) | `ghcr.io/opensecbench/opengrep:v1.25.0` | SAST (open Semgrep fork) with dataflow reachability | [ADR-0036](../docs/adr/adr-0036-sast-engines.md) |
| [`claude-cli`](claude-cli/) | `ghcr.io/opensecbench/claude-cli:2.1.222` | Run the `claude` CLI as a sandboxed inference backend | [ADR-0018](../docs/adr/adr-0018-sandboxed-cli-provider.md) |
