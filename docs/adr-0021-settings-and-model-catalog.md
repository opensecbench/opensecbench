# ADR-0021 — Settings, model catalog & appearance

Status: Proposed. A first-class **Settings** surface (sectioned/tabbed) built on a **settings registry**
that core and **extensions** both populate — so settings never feel bolted on. Plus structured **model
configuration** (a curated catalog instead of a free-text field), **model tags** for task-based routing
(frugality — right-size the model per job), and **appearance/theming** (light/dark + accent).

## Context

Settings today are scattered: ad-hoc `settings`-table keys (`active_policy_profile`, the active provider,
`analyst_approval_rules`) and Analyst config crammed into the provider panel. There is no unified settings
surface, the model is a free-text field, the UI is dark-only, and there's no way for an extension to
contribute a setting. As the platform grows (and extensions ship features), settings need a proper,
extensible home.

## Decision

### 1. A settings-section registry (the "not bolted on" core)

The Settings surface is a shell with a sidebar of **sections** (tabs). A section is one of:

- a **custom section** — a core React component with bespoke logic (Providers, Custom Agents, Approvals);
- a **declarative section** — a **field schema** rendered by a generic renderer. **This is what extensions
  contribute**: they declare fields (in their manifest), not code.

The control plane keeps a **section registry** (core declarative sections + sections declared by loaded
extension packs) and exposes it. The frontend renders declarative sections from the schema and slots the
custom core components in by section id. New settings — core or extension — are additive: register a
section, done.

### 2. Typed settings + storage

A setting is a namespaced key `<section>.<field>` persisted as JSON in the existing `settings` table.
Field types: `string`, `text`, `bool`, `select` (with options), `number`, `color`, and `model` (a
catalog-backed picker). API: `GET /v1/settings` returns the registered sections (schema) + current values;
`PUT /v1/settings` sets values (validated against the schema). Existing ad-hoc keys are reachable through
the same store; the approval policy keeps its richer editor as a custom section.

### 3. Model catalog

We can't pull a provider's live model list, so a curated **`models.json`** is embedded in the repo
(`pkg/llm/catalog`, via `go:embed`, like migrations). Each entry:
`{ provider, id, name, family, context_window, input_per_mtok, output_per_mtok, default_tags }`. API:
`GET /v1/models/catalog`. The provider add/configure UI picks a model from the catalog (a grouped dropdown
with context/price shown) instead of a free-text field — while still allowing a custom id for anything not
listed. The catalog is data, updatable without code, and could later be extended by packs.

### 4. Model tags & tag-based routing (frugality)

Each model carries **tags**. A small **fixed vocabulary drives routing** (`default`, `cheap`, `fast`,
`reasoning`, `long-context`) so the mapping is unambiguous; **user-defined custom tags** are also allowed as
labels/organization (routing keys on the built-in ones). Settings hold a **default** and a **tag → model**
map. When the runtime needs a model for a task, it resolves by tag: a profile or playbook step may declare a
preferred tag; the runtime picks that tag's model, falling back to the default. So a Triage step runs on
`cheap`, a Vuln Validator on `reasoning` — you don't pay for Opus on a job Haiku handles.

**Routing is cross-provider from v1** (decided): a tag maps to a **(provider, model)** pair, so `cheap` can be
a local Ollama model while `reasoning` is Anthropic — real cost savings. This means the runtime resolves a
**provider by tag** (building it from the provider registry, keys from the vault) rather than always using
the single active provider; the agent loop/session take the resolved provider + model per task. The active
provider remains the fallback when a task names no tag.

### 5. Appearance & theming

A declarative **Appearance** section: `theme` = `system` | `dark` | `light` (dark is the default), plus an
`accent` color override. Theming is **token-based** — the palette lives in CSS custom properties on `:root`,
the app stamps a `data-theme` attribute, and there are full **light and dark token sets** (light is a
first-class set, tuned for legibility and screenshots — not a naive inversion). The accent override sets a
token. The setting loads on boot and applies before first paint to avoid a flash.

## Consequences

- **One coherent Settings surface.** The provider panel's contents (providers, approvals, custom agents)
  become sections alongside Appearance, Models, and future ones.
- **Extensible by design.** Extensions add settings by declaring a field-schema section — declarative,
  validated, no code — so it composes instead of bolting on.
- **Frugal by default.** Structured models + tags let the system right-size the model per task.
- **Themeable.** Light/dark + accent, with light tuned for screenshots.
- **Build order:**
  1. Settings shell + section registry + `GET/PUT /v1/settings`; move Providers/Approvals/Custom-agents in
     as sections.
  2. **Appearance** section + light/dark/system theming + accent (concrete, visible).
  3. **Model catalog** (`models.json` + API) + the provider-config dropdown.
  4. **Model tags** + default + tag routing within the active provider; profiles/steps carry a tag.
  5. Extension-declared sections; cross-provider tag routing.
- **Out of scope now:** per-user settings profiles; cloud sync of settings.

Composes with ADR-0013 (extension packs — the section-schema is a new manifest capability) and ADR-0017/0019
(the model a profile/step runs on becomes tag-resolved).
