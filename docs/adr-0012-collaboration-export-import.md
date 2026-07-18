# ADR-0012 — Collaboration: portable export/import

Status: Accepted (encrypted project bundle export/import with ID remapping); DefectDojo+Teams
mediated sharing and publisher-key signing staged

## Context

Teammates need to share an engagement without a server. The plan sequences collaboration
existing-platform-first, and the universal primitive is a **portable, encrypted, content-addressed
bundle** of a project (also serving as backup). The hosted team service is deferred until proven
necessary; DefectDojo+Teams mediated sharing reuses the integration connectors (P10). This ADR
covers the bundle.

## Decision

### One encrypted file, self-contained

`pkg/bundle` exports a project's assessment graph — project, targets, applications, assets, scope,
findings, their supporting observations, evidence **artifacts + their CAS blobs**, and the KB for the
project's targets — into a single **encrypted** file. Engagement-internal, non-shareable data (tasks,
audit, sessions, proxy exchanges, secrets) is deliberately excluded; reports regenerate from the
imported findings.

Layout: a magic header + KDF salt + AEAD nonce + ciphertext. Plaintext is JSON of the bundle `Data`
(blobs base64-embedded, keyed by their sha256 — content addressing survives the trip). Encryption is
**AES-256-GCM** with a key derived from a **passphrase via scrypt** (random per-bundle salt). GCM
authenticates the whole payload, so tampering or a wrong passphrase fails closed. The passphrase is
the shared secret between sender and receiver.

### Import remaps IDs

Import decrypts, then recreates every entity through the normal store constructors, building an
old→new ID map and rewriting foreign keys in dependency order (targets → project → applications →
assets → blobs → artifacts → observations → findings → KB → scope). This means:

- **Content addressing is preserved**: blobs re-enter the CAS under the *same* sha256, so an
  artifact's evidence hash — its provenance anchor — is identical after import.
- **Re-import is safe**: every import creates fresh rows, so importing the same bundle twice yields
  two independent projects rather than a collision. Importing into the *origin* instance is fine too.

Imported agent-drafted/unreviewed items keep their review state, so the receiver re-curates rather
than inheriting unearned trust.

## Consequences

- Sharing and backup are the same offline, peer-to-peer primitive — no server, no account.
- Fidelity where it matters (findings, evidence, KB) with exact evidence hashes; engagement-internal
  noise is dropped, keeping bundles focused and safe to share.
- ID remapping (vs preserving IDs) trades exact-id identity for robust, repeatable imports — the
  right call for a sharing/backup tool.
- Passphrase-based encryption is only as strong as the passphrase; publisher-key **signing** (tying a
  bundle to an identity, reusing the extension-hub trust model) is the next step and is staged.

## Alternatives considered

- **Preserve entity IDs on import**: rejected — collides on re-import and origin-import; remapping is
  more robust and evidence provenance is anchored on the (preserved) content hash anyway.
- **Plain tar/zip of the CAS + a DB dump**: rejected — leaks engagement-internal data (secrets,
  audit) and isn't a curated, encryptable share; the typed bundle exports exactly the shareable graph.
- **Export the whole project including tasks/audit**: rejected for the share path — those are
  engagement-internal; a full local backup can be a later bundle "mode".
