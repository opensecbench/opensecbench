# ADR-0020 — Agent workspace & corpus investigation

Status: Accepted — delivered. Give agents the tools to actually **read the evidence corpus** (source code,
documents, correspondence) and **experiment** (a durable workspace + sandboxed execution), instead of only
running canned capabilities over it. This is the capability layer the rest of the agent architecture
(profiles, playbooks — ADR-0019) sits on top of; it is built **first**, because an agent that can't read
code or run a test isn't worth delegating to.

## Context

The platform already *stores* a heterogeneous corpus: asset types `source_repo`, `document`, and
`correspondence`; the `ContextItem` store ("a doc, email, chat log, or note", bytes in the CAS); captured
HTTP traffic; scan observations. But the Analyst's toolset (ADR-0017) can only run capabilities, read their
structured output, read traffic, and write findings. **It cannot open a single source file or read an
ingested document or email** — the `source-inventory` capability's `find . -type f` list is the extent of its
"code awareness." Agents reason about a corpus they have never read. Nothing lets an agent author and run a
test case or PoC either — the persona says "you never have a raw shell."

Two building blocks already exist: a source asset's `Location` is a real directory (mounted `/src` into
capability runners), and `pkg/runner` is a sandboxed exec surface with mounts, a network policy, limits, and
`Stdin` (ADR-0018). The gap is tools, not infrastructure.

## Decision

### 1. Corpus read tools (auto-approved, DLP-gated)

Read and navigate the corpus directly. Over a **source asset** (path-confined to `asset.Location`,
read-only): `read_file`, `list_dir`, `grep_code`, `find_files`. Over the **ingested corpus**:
`list_context` / `read_context` (documents, emails, chat, notes — `ContextItem` bytes from the CAS),
`get_kb_entry` (full knowledge-base read-back, which `search`/`draft` don't give). These are reads, so they
are auto-approved — but see §3.

Every read is **project-scoped** (the asset/context item must belong to the thread's project; cross-project
reads are refused) and **path-confined** (a requested path is resolved against the asset root and must stay
inside it — no `..` traversal, no symlink escape).

### 2. Workspace + conventions (durable, path-confined)

A per-project **durable workspace** plus ephemeral per-run scratch, reached by `write_file` / `read_file` /
`list_files` — path-confined to a workspace root, never the host FS. A **standard layout** with a
machine-readable `index.json` manifest lets many agents coordinate through the filesystem without guessing:

```
workspace/<project>/{inventory, recon, analysis, findings/<id>, reports, scratch/<run>, index.json}
```

Worth-keeping outputs can be promoted to durable artifacts/evidence (CAS).

### 3. DLP is the reason reads are governed, not free

Reading a **private** asset's source — or a private document — and returning it to an **external** model is a
data-egress event, exactly the class the existing guard blocks for `run_capability` on a private asset
(ADR-0011). So the corpus read tools join that guard: under a strict egress policy with an external provider,
reading a private asset/document is blocked (use a local provider, or relax the policy). This is why the read
tools **force** the DLP guard to generalize from "capability output" to "any private evidence leaving to an
external model" — reads are not exempt.

### 4. `run_code` — sandboxed experimentation (deferred one step)

Authoring and running a test case or PoC: `run_code(command)` runs in a runner sandbox with the project
workspace mounted read-write at `/work`, resource/time-limited. It refines "no raw shell" into "no shell *on
the host*, but a sandboxed, gated execution surface." Kept deliberately minimal: **gated** (arbitrary
execution needs approval) and **no network** — a PoC that must reach a target uses `send_request`, which is
already scope-guarded, rather than duplicating egress policy here. The agent stages files via `workspace_write`
and runs over them; the image defaults to `alpine:3` and is overridable per call. Built after the read tools,
as a deliberate second increment.

## Consequences

- **Agents can finally do the work** — read the actual code, the design docs, the dev-team emails, and reason
  across them, not just read scanner verdicts. This is what turns the Analyst from a tool-runner into an
  investigator.
- **Reuses substrate** — source reads use `asset.Location`; corpus reads use the `ContextItem`/CAS store;
  `run_code` uses the runner. Governance (project scope, path confinement, the DLP guard, audit) is the load
  the read tools add.
- **DLP lands where it bites** — private source/docs reaching an external model is now guarded, closing a gap
  the read tools would otherwise open wide.
- **Build order (this ADR):**
  1. **Source read tools** (`read_file`, `list_dir`, `grep_code`, `find_files`) + the DLP guard extension.
  2. Corpus read tools (`list_context`, `read_context`, `get_kb_entry`).
  3. Workspace (`write_file`/`read_file`/`list_files` + conventions + manifest).
  4. `run_code` (sandboxed execution) — the deliberate second step.
- **Out of scope now:** symbol/xref (LSP) navigation; ingestion connectors (mailbox/chat import); promoting
  workspace files to evidence. Not precluded.

Extends ADR-0017 (the governed toolset) and composes with ADR-0004 (runner sandbox), ADR-0011 (DLP), and
ADR-0018 (runner stdin / sandbox). Feeds ADR-0019 (profiles & orchestration), which assumes these tools exist.
