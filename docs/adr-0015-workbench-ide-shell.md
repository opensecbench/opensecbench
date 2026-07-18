# ADR-0015 — Workbench IDE shell

Status: Accepted (Phase 1 — frame reshape: activity bar + document center + docked Analyst + status
bar; Phase 2 — contextual explorer + methodology-as-landing + coverage in status bar); the persistent
document-tab model (Phase 3) staged

## Context

The plan (and the confirmed layout mockups) describe the per-project surface as a **VSCode-shaped IDE
Workbench**: an activity bar, a contextual explorer, a tabbed document center that can hold many open
surfaces at once, a **docked Analyst thread**, and a status bar (runner · coverage · egress · approvals).
The landing surface is the **methodology coverage home** (ADR-0009); drilling into a test item opens its
tools *in that item's context* so evidence flows back onto the checklist.

What P0 actually stood up was a placeholder Workbench shell: a flat strip of ~16 sibling tabs above a
single content pane (`frontend/src/Workbench.tsx`). Every phase since added its feature as **another flat
tab** — the Analyst included (it was tab #15). The rich frame was described but never scheduled as its own
reshape. The result is functionally complete (P0–P12 delivered) but wears the placeholder frame: you can
only see one surface at a time, the Analyst is a place you navigate *away* to, and switching tabs tears
down in-progress state.

This ADR governs promoting the placeholder into the real frame. It is deliberately staged so each step is
production-quality and independently verifiable, per our no-shortcuts agreement.

## Decision

Rebuild the per-project shell as an IDE frame while **leaving every feature component's behavior
unchanged**. The frame is owned by `Workbench.tsx`; the existing `*Tab` components are dropped into it
verbatim as the content of the active surface. Three staged steps:

### Phase 1 — frame reshape (this step)

- **Full-window frame.** When a project is open, `App` renders the Workbench as the whole window (its own
  titlebar + body + status bar); Home/Extensions keep the app rail. Home is reached via the titlebar
  project control.
- **Activity bar** (left) replaces the flat tab strip: one grouped icon per existing surface (Assets ·
  Methodology · Knowledge · Context · Findings · Repeater · Proxy · Terminal · Scan · Playbooks · Tasks ·
  Graph · Scope, with Reports · Audit below a divider). Selecting an icon sets the active surface.
- **Document center** holds the active surface. A single document-tab header is shown now as the seam for
  the Phase 3 multi-document model.
- **Docked Analyst** (right) is always present, never an activity surface. Because it stays mounted across
  navigation, Analyst threads and streaming already **survive surface switches** — the first slice of the
  persistence contract. Its internals (threads, messages, approval cards, composer) are preserved; only its
  layout adapts to a narrow dock (thread selector on top instead of a side column).
- **Status bar** (bottom): control-plane connectivity, project scope-guard indicator, a live
  approvals-waiting count (`/v1/approvals`), and an audit indicator. Values shown are real; coverage lands
  with Phase 2.

### Phase 2 — contextual explorer + methodology-as-landing

The explorer panel (between activity bar and center) becomes contextual to the active activity (methodology
structure when Methodology is active; saved requests + proxy history when Repeater is active; …). Opening a
project lands on the methodology coverage home as the active document. Coverage joins the status bar.

### Phase 3 — persistent document-tab model

Open surfaces become real documents: multiple open at once, open/close, kept **alive** when not focused.
Open-document and background-session state (Repeater edits, running scans, proxy capture, terminal, Analyst
streams) is hoisted above the view so navigating never tears it down — the full persistence contract. Test
items open their tools in-context (Repeater bound to an item; "save as evidence" auto-attaches).

## Consequences

- **Incremental + verifiable.** Phase 1 is a pure frame change with no feature-logic edits, so it builds
  and behaves identically surface-by-surface; the desktop app can be exercised immediately.
- **Persistence is designed in, not bolted on.** The docked Analyst delivers persistence for the Analyst in
  Phase 1; Phase 3 generalizes the same hoisted-state approach to all documents rather than the current
  mount/unmount-per-tab. New surfaces must not hold unsaved/in-progress state in a component that unmounts on
  navigation.
- **CSS is namespaced** under `.wb-*` so the new frame does not collide with the existing `.tab`/`.tabs`,
  `.analyst`, `.rail` styles that other views still use.
- The flat-tab `Workbench` frame is superseded; the `Tab` union is repurposed as the activity-surface key.
```

Supersedes the placeholder Workbench shell noted in ADR-0001; coordinates with ADR-0009 (methodology home)
and ADR-0006/0007 (Analyst dock, Repeater).
