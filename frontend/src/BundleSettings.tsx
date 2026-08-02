import { useRef, useState } from 'react'
import { api, Project } from './api'

// Bundle export / import surface (ADR-0012 shareable deliverable + ADR-0060 full-fidelity mode).
// Export seals the project into an encrypted, passphrase-protected .osb file; import recreates it as a new
// project. Two modes:
//   - Shareable: the client deliverable (findings + supporting evidence + KB), lean and safe to hand over.
//   - Full: also carries working state (Analyst conversation, captured traffic, reports, methodology
//     coverage, engagement record) so a loaded project renders every surface as if live — for demos,
//     backup, and intra-team migration. NOT a client deliverable (it holds the Analyst's raw reasoning).
export function BundleSettings({
  project,
  online,
  onError,
  onSaved,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
  onSaved: () => void
}) {
  const [full, setFull] = useState(false)
  const [exPass, setExPass] = useState('')
  const [exBusy, setExBusy] = useState(false)
  const [exDone, setExDone] = useState('')

  const [imPass, setImPass] = useState('')
  const [imBusy, setImBusy] = useState(false)
  const [imDone, setImDone] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const slug = (project.name || 'project').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')

  async function exportBundle() {
    if (!exPass) return
    setExBusy(true)
    setExDone('')
    try {
      const blob = await api.exportBundle(project.id, exPass, full)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${slug || 'project'}-${full ? 'full' : 'shareable'}.osb`
      a.click()
      URL.revokeObjectURL(url)
      setExDone(`Exported ${(blob.size / 1024).toFixed(0)} KB`)
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setExBusy(false)
    }
  }

  async function importFile(file: File) {
    if (!imPass) {
      onError('Enter the bundle passphrase before choosing a file.')
      return
    }
    setImBusy(true)
    setImDone('')
    try {
      const { project_id } = await api.importBundle(file, imPass)
      setImDone(`Imported as new project ${project_id.slice(0, 8)}…`)
      setImPass('')
      onSaved()
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setImBusy(false)
      if (fileRef.current) fileRef.current.value = ''
    }
  }

  return (
    <div className="content">
      <div className="hero">
        <h1>Export &amp; import</h1>
        <p>Seal this project into an encrypted, passphrase-protected bundle — or import one as a new project.</p>
      </div>

      <section className="panel es">
        <div className="panel-head">Export bundle</div>
        <div className="em-field">
          <label>Mode</label>
          <div className="em-chiprow">
            <button className={`em-chip ${!full ? 'on' : ''}`} onClick={() => setFull(false)}>Shareable deliverable</button>
            <button className={`em-chip ${full ? 'on' : ''}`} onClick={() => setFull(true)}>Full (demo / backup)</button>
          </div>
          <p className="hint">
            {full
              ? 'Full fidelity: findings and evidence plus working state — the Analyst conversation, captured traffic, generated reports, methodology coverage, and the engagement record. A loaded full bundle renders every surface as if live. It carries the Analyst’s raw reasoning and full traffic, so it is for demos, backup, and intra-team migration — not a client deliverable.'
              : 'The client deliverable: project, scope, findings and their supporting observations, evidence artifacts, and the knowledge base. Lean and safe to hand to a client. Working state (Analyst threads, traffic, reports) is excluded.'}
          </p>
        </div>
        {full && <div className="banner warn">⚠ Full bundles contain internal working state (Analyst reasoning, captured traffic). Do not send to a client.</div>}
        <div className="em-field">
          <label>Passphrase <span className="em-opt">encrypts the bundle; required to import it later</span></label>
          <input
            className="em-in"
            type="password"
            value={exPass}
            onChange={(e) => { setExPass(e.target.value); setExDone('') }}
            placeholder="Choose a strong passphrase"
            autoComplete="new-password"
            disabled={!online || exBusy}
          />
        </div>
        <div className="es-actions">
          {exDone && <span className="es-saved">✓ {exDone}</span>}
          <button className="pbuild-save" disabled={!online || exBusy || !exPass} onClick={exportBundle}>
            {exBusy ? 'Exporting…' : `Export ${full ? 'full' : 'shareable'} bundle`}
          </button>
        </div>
      </section>

      <section className="panel es">
        <div className="panel-head">Import bundle</div>
        <p className="hint">Imports an <code>.osb</code> bundle as a <strong>new</strong> project (it never overwrites this one). A full bundle restores every surface; a shareable one restores findings and evidence.</p>
        <div className="em-field">
          <label>Passphrase <span className="em-opt">the one the bundle was sealed with</span></label>
          <input
            className="em-in"
            type="password"
            value={imPass}
            onChange={(e) => { setImPass(e.target.value); setImDone('') }}
            placeholder="Bundle passphrase"
            autoComplete="off"
            disabled={!online || imBusy}
          />
        </div>
        <input
          ref={fileRef}
          type="file"
          accept=".osb"
          hidden
          onChange={(e) => { const f = e.target.files?.[0]; if (f) void importFile(f) }}
        />
        <div className="es-actions">
          {imDone && <span className="es-saved">✓ {imDone}</span>}
          <button className="pbuild-save" disabled={!online || imBusy || !imPass} onClick={() => fileRef.current?.click()}>
            {imBusy ? 'Importing…' : 'Choose .osb file…'}
          </button>
        </div>
      </section>
    </div>
  )
}
