# ADR-0050 — In-app source viewer & click-to-file from findings

Status: Accepted — building. A finding or secret that references `path:line` becomes a click that opens the
file in an in-app code viewer, scrolled to and highlighting that line. The source is served from the scanned
`source_repo` asset's on-disk tree through a path-confined HTTP endpoint; code opens as a document in the
Workbench editor area (a multi-document surface, per ADR-0015), with syntax highlighting.

## Context

SAST (semgrep/opengrep), secret (TruffleHog), and vuln (govulncheck) interpreters record an observation's
`location` as a single `"path:line"` string relative to the scanned repo (ADR-0005/0032/0036). Until now the
UI could only display that string — there was no way to see the code it points at. The actual source lives on
disk at the `source_repo` asset's `Location` (the operator places it there; the platform reads it in place at
scan time and does not clone or copy it — ADR-0002). The one existing content endpoint,
`/v1/artifacts/{id}/content`, serves CAS artifacts (task output), not repo files. The analyst agent already
reads repo files safely via `pkg/analyst/codetools.go` (`read_file`, path-confined), but only as an LLM tool,
never over HTTP.

So three things were missing: an HTTP way to read a repo file, a way to know *which* repo a finding's path is
relative to, and a viewer.

## Decision

**1. Resolve finding → file through the stored task→asset link.** An observation carries `task_id`; a task
carries `asset_id` (persisted column `tasks.asset_id`, set whenever a scan runs against a `source_repo`
asset — mandatory for playbook runs). So `observation → task.asset_id → asset.Location` is a reliable chain
to the on-disk root. `GET /v1/projects/{id}/observations` now returns each observation enriched with that
resolved `asset_id` (`store.LocatedObservation`); it is empty only for tasks launched from a bare
`target_dir` with no asset, where the UI simply omits the click affordance.

**2. Serve files path-confined.** New endpoints:
- `GET /v1/assets/{id}/source?path=<rel>` — one file's contents (capped 1 MiB), with line count + truncation.
- `GET /v1/assets/{id}/tree?path=<rel>` — one directory's children (dirs first, noise dirs elided).

The security-critical confinement (reject `..`, absolute re-rooting, symlink escape) is factored into
`pkg/srcfile` and shared verbatim by both these endpoints and the analyst tools — one boundary, one place to
audit. Project confinement is inherent: `s.pdb(r)` is the per-project database (ADR-0049), so an asset id from
another project is simply not found. Only `source_repo` assets are readable.

**3. Code is a document.** A file opens in the Workbench editor area as a document keyed
`code:<assetId>:<path>`. `code` is a multi-document surface (like Replay), so it uses the slim per-surface tab
row introduced alongside this work; every other surface stays tab-less. The viewer shows line numbers,
auto-scrolls to the target line, highlights it, and syntax-highlights via a bundled highlighter themed to the
app's light/dark tokens (the app is a desktop bundle, so there is no CSP/offline concern).

**4. Entry points.** The Findings and Investigations surfaces render each source `location` as a clickable
chip → open at line. Taint findings also expose `attributes.dataflow_source` (the input location) as a second
jump. The Explorer gains a source file-tree and richer per-surface content.

## Non-goals

- **No multi-step dataflow trace.** Interpretation keeps only the source and sink endpoints, not the
  intermediate threadFlow steps (ADR-0032); a step-through path is out of scope until those are persisted.
- **No editing.** The viewer is read-only.
- **No repo management.** The platform still does not clone, cache, or retain source; it reads the operator's
  on-disk `Location`. A moved/deleted directory yields a 404, surfaced as "source unavailable".
- **`location` is overloaded.** nmap uses it for `host:port/proto`; the UI linkifies only true source paths.
