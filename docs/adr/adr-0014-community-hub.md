# ADR-0014 — Community extension hub

Status: Accepted (static signed index, publish/browse/install, explicit trust-on-install);
submission scanning, reputation, and a hosted upload service staged

## Context

ADR-0013 made packages loadable, signed, and digest-pinned. The hub is their **distribution
channel**. The plan's threat model is explicit: *assume malicious actors will try to upload bad
packages* — so the hub is a distribution layer over an already-safe format (signed, pinned,
sandboxed), never a new trust root. It must work offline-friendly and start simple ("a directory or
git repo now; a hosted registry later").

## Decision

### A hub is a signed static index + package archives

A hub is anything that serves:

- `index.json` — a list of `PackageEntry{ id, name, version, publisher, description, tags, digest,
  archive (relative URL), publisher_key (base64 ed25519) }`.
- `packages/<id>-<version>.tgz` — the gzipped tar of the package directory (`extension.json` [+
  `extension.sig`]).

Because it is static files, a hub can be a directory served by any web server, a git repo's raw
URLs, or (later) a hosted service — no bespoke server required to start. `osb hub publish` builds the
archive, computes the digest, and updates a local hub directory's `index.json`; serve that directory
and you are a hub.

### Install verifies twice and trusts explicitly

`osb hub install` (server-side, into `<data>/extensions`):

1. Fetch the index; find the entry.
2. Download the archive; **verify its bytes hash to the entry `digest`** (integrity in transit).
3. Extract; the **extension loader re-verifies** the manifest digest + ed25519 signature against the
   **trust store** (ADR-0013) before it is registered.

Trust is **never** taken from the index (that would be trust-on-first-use — the exact hub failure
mode). The publisher key in the index is *shown*; the operator must **explicitly** trust it
(`--trust`, an audited action that writes the key to the trust store) or install is refused (unless a
local-dev unsigned override). Installing hot-registers the package's capabilities/methodologies into
the live registries — no restart.

### Reputation & scanning are surfaced, not trusted

Install-time surfacing (publisher, whether trusted, tags) is shown so the operator decides. Automated
submission scanning, reputation/labels, and moderation/takedown belong to a hosted hub and are
staged; the format carries `tags` and stable digests so they attach later without a format change.

## Consequences

- Distribution reuses the ADR-0013 safety properties wholesale: a hub can only offer packages that are
  still signed, pinned, and sandboxed on install — the hub compromise surface is "serve a different
  signed package", which the trust store + digest catch.
- Zero-infrastructure start: publish to a directory, serve it, share your `.pub`. A hosted registry
  is an optimization, not a prerequisite.
- Explicit trust-on-install keeps the human in the loop for the one irreversible decision (trusting a
  publisher), consistent with the platform's human-in-control stance.

## Alternatives considered

- **Auto-trust publishers listed in the index**: rejected outright — trust-on-first-use is the
  attack. Keys are shown; trusting is a separate explicit, audited step.
- **A bespoke hub server with an upload API now**: deferred — a static index covers browse/install
  and runs anywhere; the hosted service (accounts, uploads, scanning, moderation) layers on later.
- **Client-side install (CLI writes into the data dir)**: rejected — the daemon owns its data dir and
  the trust store; install is a server action so verification + hot-registration happen in one place.
