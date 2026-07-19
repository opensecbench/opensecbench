# Extensions

First-party extension packages (ADR-0013). Each subdirectory is a package: an `extension.json`
manifest plus an optional `extension.sig` (detached ed25519 signature). Built-ins use the **same**
format third-party packages do — this directory dogfoods the loader.

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
  section is namespaced with `ext.<id>.` and rendered by the generic Settings UI — no client code. See
  `trufflehog/extension.json` for a working example.

Playbook/visualization pack types are staged.
