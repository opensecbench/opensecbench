# ADR-0011 — Secrets vault, DLP & redaction

Status: Accepted (encrypted vault, references-not-values, exec-time injection, output redaction, DLP
egress scan + canaries); full policy_profile entity and remote KMS staged

## Context

Engagements need credentials — target test accounts, cloud/AWS keys, integration API tokens, LLM
provider keys. The plan is emphatic: **no secrets in code or config**; values live only in an
encrypted vault, code uses **references**, values are injected into a runner **only at exec time**,
and are **redacted everywhere** (audit, thread, egress). A DLP monitor is the active counterpart —
it watches every egress point (LLM requests, reports, export bundles) for vault secrets, planted
**canary** tokens, and secret/PII patterns, and blocks/redacts/alerts. This ADR covers the vault,
injection, redaction, and DLP; it builds on the sensitivity-based egress policy already in place
(ADR-0006).

## Decision

### Vault — encrypted at rest, references not values

`pkg/secret` provides a `Vault` that seals/opens values with **AES-256-GCM** (random 12-byte nonce
per value, authenticated). Sealed blobs live in a `secrets` table keyed by a unique **name**; a
secret is referenced elsewhere by that name, never by value.

**Master key** resolution, strongest first:
1. `OSB_VAULT_KEY` — base64 32-byte key in the environment (key never touches disk).
2. else a generated `vault.key` file (0600) beside the database.

The env-provided key is preferred (ciphertext and key aren't co-located); the key file is the
zero-config local default. Passphrase-derived keys (scrypt) and a remote KMS are additive later.

**The API never returns a plaintext secret.** Create accepts a value and seals it; list returns only
names/metadata; there is no "get value" endpoint. Values leave the vault only into a runner at exec
time or into a governed integration call.

### Injection at exec time

A task may request secret references — `{ENV_VAR: secret_name}`. The engine resolves them through
the vault immediately before running and passes them as environment variables into the sandboxed
runner (never written to the RunSpec that gets persisted, never logged). Nothing about the plaintext
is stored on the task.

### Redaction

Whenever secret values are known (the resolved set for a run), they are **redacted** from captured
stdout/stderr before the artifact is stored, and from any audit/thread text — replaced with
`«redacted:NAME»`. Redaction is defense-in-depth alongside "don't log it in the first place."

### DLP monitor + canaries

`pkg/dlp` inspects outbound content at egress points (LLM provider requests first; reports/exports
next) for: (a) any **current vault secret value**, (b) planted **canary** tokens, (c) high-signal
**patterns** (AWS keys, private-key headers, JWTs). A hit is recorded as a `dlp_event`
(blocked | redacted | alerted) in the append-only trail. Default action for vault-secret/canary hits
on an *external* egress is **block**; pattern hits **redact + alert**. Canaries are exfil tripwires:
a decoy token that should never leave — if it appears at an egress or is reported hit externally, a
rogue extension/agent/tool is caught.

## Consequences

- Secrets never live in code, task rows, artifacts, or logs; the only plaintext paths are exec-time
  injection and explicit integration calls, both governed.
- DLP gives an *active* backstop to the vault's *passive* redaction, and canaries turn silent
  exfiltration into a detectable event — the trust story the community hub (P12) will lean on.
- Co-locating the key file with the DB is a real weakness; `OSB_VAULT_KEY` is the documented stronger
  path, and KMS/passphrase are staged. This is called out rather than hidden.
- Redaction is best-effort string replacement — it cannot catch a transformed/encoded secret; the
  primary control remains not sending secret-bearing data to a disallowed egress (egress policy).

## Alternatives considered

- **OS keychain integration** (Keychain/DPAPI/libsecret): better key custody, but platform-specific
  and unavailable headless; the file/env key ships now and keychain is additive.
- **Return secrets through the API for clients to inject**: rejected — plaintext would cross the API
  and risk logging; injection stays server-side at exec time.
- **DLP as a separate proxy process**: rejected for now — in-process egress hooks are simpler and
  cover the current egress points (LLM, reports); a proxy-level DLP can layer on with pkg/proxy.
