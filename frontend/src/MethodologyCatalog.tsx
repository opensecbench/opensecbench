import { useCallback, useEffect, useRef, useState } from 'react'
import { api, Methodology } from './api'
import { MethodologyBuilder } from './MethodologyBuilder'

// MethodologyCatalog is the global authoring surface for methodology packs (ADR-0009 / ADR-0055) — the
// testing checklists a project adopts from and tracks coverage against. Built-in and extension packs are
// read-only; teams author their own here by hand, by copying a built-in, by importing JSON, or by pasting a
// free-form checklist and letting the LLM structure it. Per-project adoption + coverage stay on the project's
// Methodology surface.
export function MethodologyCatalog({ online }: { online: boolean }) {
  const [packs, setPacks] = useState<Methodology[]>([])
  const [open, setOpen] = useState<string | null>(null)
  const [building, setBuilding] = useState(false)
  const [editing, setEditing] = useState<Methodology | null>(null)
  const [template, setTemplate] = useState<Methodology | null>(null) // draft source: copy / import / LLM convert
  const [converting, setConverting] = useState(false) // the "paste a checklist" panel is open
  const [checklist, setChecklist] = useState('')
  const [checklistTitle, setChecklistTitle] = useState('')
  const [drafting, setDrafting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)

  const load = useCallback(async () => {
    try {
      setPacks((await api.listMethodologies()) ?? [])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])
  useEffect(() => {
    if (online) void load()
  }, [online, load])

  function reset() {
    setBuilding(false)
    setEditing(null)
    setTemplate(null)
    setConverting(false)
  }

  // Open the builder pre-filled from `m` in create mode (a draft to review before saving). Used by copy,
  // import, and LLM-convert alike — a built-in becomes a starting template you can edit freely.
  function openDraft(m: Methodology) {
    setEditing(null)
    setConverting(false)
    setTemplate(m)
    setBuilding(true)
  }

  async function del(id: string) {
    try {
      await api.deleteMethodology(id)
      await load()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  // Convert pasted checklist text into a draft pack (LLM), then open it in the editor for review.
  async function convert() {
    setError(null)
    setDrafting(true)
    try {
      const draft = await api.draftMethodologyFromText(checklist, checklistTitle)
      setChecklist('')
      setChecklistTitle('')
      openDraft(draft)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setDrafting(false)
    }
  }

  // Import a pack from a .json file (exported from here or hand-written), opening it as a draft to review.
  async function onImportFile(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0]
    if (fileRef.current) fileRef.current.value = '' // allow re-importing the same file
    if (!f) return
    try {
      const parsed = JSON.parse(await f.text())
      if (!parsed || typeof parsed !== 'object' || !parsed.title || !Array.isArray(parsed.items)) {
        throw new Error('not a methodology pack (needs a title and an items array)')
      }
      setError(null)
      openDraft(parsed as Methodology)
    } catch (err) {
      setError('Import failed: ' + (err as Error).message)
    }
  }

  // Export a pack to a downloadable .json file (the transient `builtin` flag is stripped).
  function exportPack(p: Methodology) {
    const { builtin: _builtin, ...clean } = p
    const blob = new Blob([JSON.stringify(clean, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `methodology-${p.id}.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="lib-section">
      {error && <div className="banner error">⚠ {error}</div>}
      <input ref={fileRef} type="file" accept="application/json,.json" style={{ display: 'none' }} onChange={onImportFile} />
      <div className="lib-head">
        <h2>Methodology catalog</h2>
        <p>The testing standards available across the instance. Author your own packs — by hand, by copying a built-in, by importing JSON, or by pasting a free-form checklist for the assistant to structure — then a project adopts a pack from its Methodology surface and tracks per-item coverage there.</p>
        <div className="lib-actions">
          <button className="lib-new" disabled={!online} onClick={() => { reset(); setBuilding(true) }}>＋ New methodology</button>
          <button className="ghost-btn" disabled={!online} onClick={() => { reset(); setConverting(true) }}>✨ From checklist</button>
          <button className="ghost-btn" disabled={!online} onClick={() => fileRef.current?.click()}>⇪ Import JSON</button>
        </div>
      </div>

      {converting && (
        <div className="pbuild">
          <div className="pbuild-h">
            <b>Convert a checklist</b>
            <span className="grow" />
            <button className="ghost-btn" onClick={() => setConverting(false)}>Cancel</button>
          </div>
          <p className="mcat-convert-hint">Paste your text checklist below. The assistant turns each line into a structured item (objective, procedure, standards) and opens it here for review — nothing is saved until you save it.</p>
          <input className="pbuild-in" placeholder="Pack title (optional — the assistant will suggest one)" value={checklistTitle} onChange={(e) => setChecklistTitle(e.target.value)} />
          <textarea
            className="pbuild-instr"
            style={{ minHeight: 160 }}
            placeholder={'Paste your checklist, e.g.\n- Check for IDOR on all object references\n- Verify session tokens rotate on privilege change\n- Test redirect_uri validation on the OAuth flow'}
            value={checklist}
            onChange={(e) => setChecklist(e.target.value)}
          />
          <div className="pbuild-actions">
            <span className="grow" />
            <button className="pbuild-save" disabled={!online || drafting || !checklist.trim()} onClick={convert}>
              {drafting ? 'Converting…' : '✨ Convert'}
            </button>
          </div>
        </div>
      )}

      {(building || editing) && (
        <MethodologyBuilder
          key={editing?.id ?? (template ? `draft:${template.id}` : 'new')}
          online={online}
          edit={editing ?? undefined}
          template={template ?? undefined}
          onCancel={reset}
          onSaved={() => {
            reset()
            void load()
          }}
        />
      )}

      {packs.length === 0 && <div className="orch-empty">No methodology packs registered.</div>}
      {packs.map((p) => {
        const expanded = open === p.id
        return (
          <div key={p.id} className="mcat-pack">
            <div className="mcat-pack-h">
              <button className="mcat-pack-toggle" onClick={() => setOpen(expanded ? null : p.id)}>
                <span className="mcat-caret">{expanded ? '▾' : '▸'}</span>
                <b>{p.title}</b>
                <span className="mcat-meta">{p.tech} · v{p.version} · {p.items.length} item{p.items.length === 1 ? '' : 's'}</span>
              </button>
              <span className={p.builtin ? 'orch-builtin' : 'orch-saved'}>{p.builtin ? 'built-in' : 'saved'}</span>
              <button className="orch-copy" title="Export this pack as JSON" onClick={() => exportPack(p)}>⇩ Export</button>
              <button className="orch-copy" title="Save an editable copy of this pack" disabled={!online} onClick={() => openDraft({ ...p, title: `${p.title} (copy)` })}>⧉ Copy</button>
              {!p.builtin && (
                <>
                  <button className="orch-edit" title="Edit this pack" disabled={!online} onClick={() => { setConverting(false); setTemplate(null); setEditing(p); setBuilding(true) }}>✎ Edit</button>
                  <button className="orch-del" title="Delete this pack" disabled={!online} onClick={() => del(p.id)}>×</button>
                </>
              )}
            </div>
            {expanded && (
              <div className="mcat-items">
                {p.items.map((it) => (
                  <div key={it.id} className="mcat-item">
                    <div className="mcat-item-t">{it.title}</div>
                    {it.objective && <div className="mcat-item-d">{it.objective}</div>}
                    {(it.standards?.length ?? 0) > 0 && (
                      <div className="mcat-item-tags">{it.standards!.map((s) => <span key={s} className="mcat-tag">{s}</span>)}</div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
