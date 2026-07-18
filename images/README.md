# OSB container images

Images that OpenSecBench **builds itself**. Everything else — capability and session sandboxes — pulls
**pinned public images** on demand (`alpine:3`, `anchore/grype:v0.80.2`, …); those need nothing here. This
directory is only for the rare case where no suitable public image exists and we must build one.

## Convention

- One directory per image: `images/<name>/Dockerfile` (+ a short `README.md` for that image).
- It builds to the tag **`osb/<name>:latest`**. Code that references an OSB-built image uses that name
  (e.g. the sandboxed CLI provider defaults `OSB_LLM_CLI_IMAGE` to `osb/claude-cli:latest`).
- **Pin what you install.** The rest of OSB pins every image by tag/digest; do the same inside these
  Dockerfiles (base image tag + explicit versions), or override at build time with `--build-arg` and tag the
  result accordingly. `latest` defaults are a convenience for local dev, not for a real deployment.
- Nothing sensitive is baked in. Credentials and data arrive at **runtime** via the runner's read-only
  mounts and secret-env, never in an image layer (ADR-0011).

## Building

```sh
make images          # build every images/<name> as osb/<name>:latest
make image-claude-cli # build just one
```

The `image-%` target auto-discovers directories here, so a new image is just a new `images/<name>/`
directory plus its Dockerfile — no Makefile change needed.

## Images

| Image | Tag | Purpose | ADR |
|-------|-----|---------|-----|
| [`claude-cli`](claude-cli/) | `osb/claude-cli:latest` | Run the `claude` CLI as a sandboxed inference backend | [ADR-0018](../docs/adr-0018-sandboxed-cli-provider.md) |
