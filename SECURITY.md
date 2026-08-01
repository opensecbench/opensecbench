# Security Policy

OpenSecBench is a security-assessment tool that handles sensitive data — source code, findings,
credentials in its vault, and traffic captures. We take the security of the tool itself seriously
and appreciate reports made responsibly.

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues, pull requests, or
discussions.**

Instead, report it privately through GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability):
go to the repository's **Security** tab → **Report a vulnerability**.

Please include, as far as you can:

- A description of the issue and its impact.
- Steps to reproduce, or a proof of concept.
- Affected version / commit and your environment (OS, LLM provider, runner setup).
- Any suggested remediation.

## What to expect

This is an early-access project maintained on a best-effort basis. We aim to:

- Acknowledge your report within **5 business days**.
- Give an initial assessment and a rough timeline within **10 business days**.
- Keep you updated as we work on a fix, and credit you when the fix ships (unless you prefer to
  stay anonymous).

Please give us a reasonable window to address the issue before any public disclosure.

## Scope

In scope — vulnerabilities **in OpenSecBench itself**, for example:

- Escapes from the OCI-sandboxed capability/agent runners, or bypasses of the scope guard.
- Leakage of secrets from the encrypted vault, or bypasses of DLP redaction / egress controls.
- Tampering with the append-only, hash-chained audit trail.
- Signature or digest-pinning bypasses in the extension loader / community hub.
- Auth, path-traversal, or injection issues in the local control-plane API.

The security model for these areas is documented in the ADRs under [`docs/`](docs/) — notably
`adr-0011` (secrets/DLP/redaction), `adr-0003`/`adr-0013` (extension trust), `adr-0004` (runner
protocol), and `adr-0002` (provenance/audit). Grounding a report in the relevant ADR helps us
triage quickly.

Out of scope:

- Findings that OpenSecBench *reports about a target you scan* — those are the tool working as
  intended, not vulnerabilities in OpenSecBench.
- Issues in third-party scanners, models, or extensions — report those upstream.
- Anything requiring a malicious extension the user has explicitly chosen to trust and install
  (trust is an explicit, signed decision by design), unless it demonstrates a bypass of the stated
  trust controls.

## Supported versions

OpenSecBench is pre-1.0 and moving fast. Security fixes land on the latest `main`; there is no
back-porting to older commits during early access. Always run the latest version.
