import { useEffect, useState } from 'react'
import { api, CapabilityManifest, Methodology, MethodologyInput } from './api'

// caps is a validated list of capability ids (picked from the registry); standards is free text.
type Item = { id?: string; title: string; objective: string; procedure: string; standards: string; caps: string[] }

// splitList turns a comma-separated field into a trimmed, empty-free list (and back, with joinList).
const splitList = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean)
const joinList = (xs?: string[]) => (xs ?? []).join(', ')

// MethodologyBuilder authors OR edits a methodology pack (ADR-0055): a title, tech/version, applicability
// keywords, and a list of checklist items. It mirrors PlaybookBuilder — passing `edit` loads a saved pack and
// saves in place (keeping its id so adopted-pack and coverage references stay valid); passing `template`
// instead pre-fills from another pack (e.g. a read-only built-in) but stays in create mode — the "Copy" path.
// On copy, item ids are dropped so the backend re-scopes them under the new pack id.
export function MethodologyBuilder({
  online,
  edit,
  template,
  onSaved,
  onCancel,
}: {
  online: boolean
  edit?: Methodology
  template?: Methodology
  onSaved: () => void
  onCancel: () => void
}) {
  const seed = edit ?? template // fields to pre-fill from; only `edit` also saves in place
  const [title, setTitle] = useState(seed?.title ?? '')
  const [tech, setTech] = useState(seed?.tech ?? '')
  const [version, setVersion] = useState(seed?.version ?? '')
  const [keywords, setKeywords] = useState(joinList(seed?.keywords))
  const [items, setItems] = useState<Item[]>(
    seed?.items?.length
      ? seed.items.map((it) => ({
          id: edit ? it.id : undefined, // keep ids only when editing in place; copies get fresh ids
          title: it.title,
          objective: it.objective ?? '',
          procedure: it.procedure ?? '',
          standards: joinList(it.standards),
          caps: it.suggested_capabilities ?? [],
        }))
      : [{ title: '', objective: '', procedure: '', standards: '', caps: [] }],
  )
  const [capabilities, setCapabilities] = useState<CapabilityManifest[]>([])
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  // The suggested-capabilities picker is validated against the live capability registry (ADR-0055) rather
  // than free text, so items only ever reference capabilities that actually exist.
  useEffect(() => {
    if (!online) return
    void api.listCapabilities().then(setCapabilities).catch(() => setCapabilities([]))
  }, [online])
  const capTitle = (id: string) => capabilities.find((c) => c.id === id)?.title ?? id

  function update(i: number, patch: Partial<Item>) {
    setItems((s) => s.map((it, j) => (j === i ? { ...it, ...patch } : it)))
  }
  function addItem() {
    setItems((s) => [...s, { title: '', objective: '', procedure: '', standards: '', caps: [] }])
  }
  function removeItem(i: number) {
    setItems((s) => s.filter((_, j) => j !== i))
  }
  function addCap(i: number, id: string) {
    if (!id) return
    setItems((s) => s.map((it, j) => (j === i && !it.caps.includes(id) ? { ...it, caps: [...it.caps, id] } : it)))
  }
  function removeCap(i: number, id: string) {
    setItems((s) => s.map((it, j) => (j === i ? { ...it, caps: it.caps.filter((c) => c !== id) } : it)))
  }

  async function save() {
    setError(null)
    setSaving(true)
    try {
      const body: MethodologyInput = {
        title: title.trim(),
        tech: tech.trim(),
        version: version.trim(),
        keywords: splitList(keywords),
        items: items.map((it) => ({
          id: it.id,
          title: it.title.trim(),
          objective: it.objective.trim(),
          procedure: it.procedure.trim(),
          standards: splitList(it.standards),
          suggested_capabilities: it.caps,
        })),
      }
      if (edit) await api.updateMethodology(edit.id, body)
      else await api.createMethodology(body)
      onSaved()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="pbuild">
      <div className="pbuild-h">
        <b>{edit ? 'Edit checklist' : 'New checklist'}</b>
        <span className="grow" />
        <button className="ghost-btn" onClick={onCancel}>Cancel</button>
      </div>
      {error && <div className="banner error">⚠ {error}</div>}
      <input className="pbuild-in" placeholder="Title (e.g. GraphQL API)" value={title} onChange={(e) => setTitle(e.target.value)} />
      <div className="pbuild-row">
        <input className="pbuild-in" placeholder="Tech tag (e.g. api, web, mobile)" value={tech} onChange={(e) => setTech(e.target.value)} />
        <input className="pbuild-in" placeholder="Version (default 1.0.0)" value={version} onChange={(e) => setVersion(e.target.value)} />
      </div>
      <input
        className="pbuild-in"
        placeholder="Keywords, comma-separated — used to suggest this checklist when they appear in a target's knowledge base"
        value={keywords}
        onChange={(e) => setKeywords(e.target.value)}
      />

      {items.map((it, i) => (
        <div key={i} className="pbuild-step">
          <div className="pbuild-step-h">
            <input className="pbuild-in grow" placeholder="Item title (the check)" value={it.title} onChange={(e) => update(i, { title: e.target.value })} />
            {items.length > 1 && <button className="orch-del" title="Remove item" onClick={() => removeItem(i)}>×</button>}
          </div>
          <textarea className="pbuild-instr" placeholder="Objective — what this check confirms" value={it.objective} onChange={(e) => update(i, { objective: e.target.value })} />
          <textarea className="pbuild-instr" placeholder="Procedure — how to test it" value={it.procedure} onChange={(e) => update(i, { procedure: e.target.value })} />
          <input className="pbuild-in" placeholder="Standards, comma-separated (e.g. OWASP ASVS V4, CWE-639)" value={it.standards} onChange={(e) => update(i, { standards: e.target.value })} />
          <div className="mbuild-caps">
            <span className="mbuild-caps-lbl">Capabilities</span>
            {it.caps.map((id) => (
              <span key={id} className="mbuild-cap">
                {capTitle(id)}
                <button className="mbuild-cap-x" title="Remove" onClick={() => removeCap(i, id)}>×</button>
              </span>
            ))}
            <select
              className="mbuild-cap-add"
              value=""
              disabled={capabilities.length === 0}
              onChange={(e) => { addCap(i, e.target.value); e.target.value = '' }}
            >
              <option value="">＋ add capability…</option>
              {capabilities.filter((c) => !it.caps.includes(c.id)).map((c) => (
                <option key={c.id} value={c.id}>{c.title} ({c.id})</option>
              ))}
            </select>
          </div>
        </div>
      ))}

      <div className="pbuild-actions">
        <button className="ghost-btn" onClick={addItem}>＋ Add item</button>
        <span className="grow" />
        <button className="pbuild-save" disabled={!online || saving || !title.trim()} onClick={save}>
          {saving ? 'Saving…' : edit ? 'Save changes' : 'Save checklist'}
        </button>
      </div>
    </div>
  )
}
