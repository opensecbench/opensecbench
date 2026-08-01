# ADR-0013 — Extension loader

Status: Accepted (directory packages, container capabilities + methodology packs, ed25519 signing +
trust store, digest pinning); report/playbook/viz pack types and a community hub staged

## Context

ADR-0003 set the central thesis — adding a tool or methodology is *authoring a package*, not patching
core — and the trust model (signed, digest-pinned, permission-declaring packages). Until now the
loader didn't exist: capabilities, methodology packs, and report templates were all code-defined
built-ins. This ADR pins down the concrete loader so third parties (and, later, a community hub) can
ship packages, and built-ins can be dogfooded through the same path.

## Decision

### Package = a directory with a manifest

An extension is a directory containing `extension.json`:

```json
{
  "id": "opensecbench.trufflehog",
  "name": "TruffleHog secrets scanner",
  "version": "1.0.0",
  "publisher": "opensecbench",
  "capabilities":  [ <container-capability manifest>, ... ],
  "methodologies": [ <methodology pack>, ... ]
}
```

**v1 loads two provider types** — both pure data, no third-party Go:

- **Container capabilities** — a manifest declaring `image` (pinned digest ref), `cmd` (with
  `{{param}}` / `{{target}}` substitution), `network`, source-mount, output name/media-type,
  `ok_exit_codes`, and optional `target_param`. It satisfies the same `capability.Capability`
  interface as built-ins and runs in the identical sandbox (ADR-0004) — so a package adds a tool
  (TruffleHog, nmap, …) with **no core code and no new trust surface** beyond a container image.
- **Methodology packs** — a `methodology.Methodology` value, registered into the catalog (ADR-0009).

Report/playbook/visualization pack types and a `permissions` schema beyond the sandbox defaults are
staged; the manifest is versioned so they add without breaking v1.

### Signing, trust, digest pinning

- **Digest**: sha256 over the canonical JSON of the manifest (signature excluded). Recorded at
  install → the package is immutable; a changed digest is a different package.
- **Signature**: detached **ed25519** over the digest, in `extension.sig` (base64), attributed to a
  `publisher`. Verified against a **trust store** — trusted public keys under
  `<data>/extensions/trusted_keys/<publisher>.pub`.
- **Untrusted/unsigned** packages are **refused** unless an explicit, audited override
  (`allow_unsigned`) is given — never the default. Built-in first-party packs are signed by the
  project key shipped in the trust store.

`osb ext keygen` / `sign` produce a key pair and a package signature; `ext trust` adds a publisher
key; the loader enforces the rest.

### Loading

At startup the control plane loads every package under `<data>/extensions/*/`, verifies each, and
registers its capabilities into the capability registry and its methodologies into the catalog — the
same registries the built-ins populate. A failed/untrusted package is skipped with an audited event,
never fatal.

## Consequences

- The "everything is an extension" thesis becomes real: the tool catalog and methodology packs grow
  by dropping in a signed directory, and built-ins can migrate to the same format incrementally.
- Container capabilities inherit sandboxing, scope, secrets, and audit for free — the package adds an
  image + argv, not privileged code — which is what keeps community packages tractable to trust.
- Signing + digest pinning + a trust store are enforced from v1, so the community hub (P12) is a
  distribution channel over an already-safe format, not a new trust problem.

## Alternatives considered

- **Go plugins (`plugin` pkg) for third-party capabilities**: rejected — fragile ABI, no
  sandboxing, and a huge trust surface. Container capabilities are language-agnostic and sandboxed.
- **A single signed archive (tar) instead of a directory**: fine later; a directory is simpler to
  author, diff, and load now, and the digest is over the manifest regardless.
- **Trust-on-first-use**: rejected — silent trust is exactly the hub threat model's failure. Explicit
  key trust with an audited unsigned-override is the safe default.
