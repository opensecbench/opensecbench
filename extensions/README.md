# Extensions

The extension loader (ADR-0013) for **third-party** packages. Each package is a directory with an
`extension.json` manifest plus an `extension.sig` (detached ed25519 signature). Built-ins implement the
**same** `capability.Capability` contract, so the extension API is exercised by every first-party tool —
but first-party tools ship in-tree as built-ins (`pkg/capability/builtins.go`), **not** as bundled packs.

> Bundling a first-party tool as an unsigned in-tree pack (we briefly did this for trufflehog) only makes
> it fail to load by default — the extension format's payoff is add/update *without* recompiling, which is
> moot for a tool that ships in the binary. So this directory carries no bundled packs; a runnable example
> extension lives in its own separate repository.

## Installing a package

The control plane loads packages from `<data-dir>/extensions/*/` at startup (the data dir is next to
the SQLite database). To install:

1. Copy the package directory into `<data-dir>/extensions/`.
2. Trust its publisher's key: place the publisher's base64 ed25519 public key at
   `<data-dir>/extensions/trusted_keys/<publisher>.pub`.
3. Restart the control plane. `osb ext list` shows what loaded.

Unsigned or untrusted packages are refused unless `OSB_ALLOW_UNSIGNED_EXTENSIONS=1` (local dev only).

## Authoring & signing

```
osb ext keygen --out mypublisher        # writes mypublisher.pub / mypublisher.key
osb ext sign --dir ./mypackage --key mypublisher.key   # writes mypackage/extension.sig
```

Then publish the package directory and share your `.pub` so users can trust it.

## Package types (v1)

- **container capabilities** — an image + templated `cmd`; runs in the standard sandbox (ADR-0004).
- **methodology packs** — reusable checklists (ADR-0009).
- **report templates** — MD/HTML template strings registered as report types.
- **settings sections** — a `settings` array of declarative field-schema sections (ADR-0021 §6). Each
  section is namespaced with `ext.<id>.` and rendered by the generic Settings UI — no client code.

Playbook/visualization pack types are staged.
