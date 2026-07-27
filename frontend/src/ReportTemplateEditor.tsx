import { useEffect, useState } from 'react'
import { api, Project, ReportTemplate, ReportTemplateDetail } from './api'

// ReportTemplatesPanel is the reusable report-template editor: a list of built-in (read-only) and saved
// templates, a source editor (raw MD/HTML), and a live preview rendered against a real project's data via
// /report-templates/preview (which renders without saving). Selecting a built-in shows its source with a
// "Fork" action; selecting a saved template edits it in place — mirroring MethodologyBuilder's fork/edit
// split (built-ins immutable, editing one saves a copy under a new id).
//
// It is used two ways: inside an overlay on the Reports page (previewProjectId fixed to the open project)
// and inline in the global Library (allowProjectPick lets the user choose which project to preview against,
// since the Library has no active project).
export function ReportTemplatesPanel({
  online,
  onChanged,
  previewProjectId,
  allowProjectPick,
}: {
  online: boolean
  onChanged?: () => void // notify the host (e.g. the Reports generate dropdown) to refresh
  previewProjectId?: string
  allowProjectPick?: boolean
}) {
  const [list, setList] = useState<ReportTemplate[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [detail, setDetail] = useState<ReportTemplateDetail | null>(null)
  // Draft form state. `savedId` is the id we update in place (null while forking/creating a new one).
  const [savedId, setSavedId] = useState<string | null>(null)
  const [base, setBase] = useState('')
  const [title, setTitle] = useState('')
  const [md, setMd] = useState('')
  const [html, setHtml] = useState('')
  const [preview, setPreview] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [dirty, setDirty] = useState(false)
  // Preview target: fixed to previewProjectId when provided, otherwise picked from a selector (Library).
  const [projects, setProjects] = useState<Project[]>([])
  const [pickedProject, setPickedProject] = useState('')
  const previewProject = previewProjectId ?? pickedProject

  const isBuiltin = detail?.builtin ?? false
  const canSave = !!title.trim() && !!md.trim() && !!html.trim() && !isBuiltin

  async function reloadList(select?: string) {
    try {
      const rows = (await api.listReportTemplates()) ?? []
      setList(rows)
      if (select) void selectTemplate(select)
      else if (!selectedId && rows.length) void selectTemplate(rows[0].id)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function selectTemplate(id: string) {
    try {
      const d = await api.getReportTemplate(id)
      setSelectedId(id)
      setDetail(d)
      setSavedId(d.builtin ? null : d.id)
      setBase(d.base || (d.builtin ? d.id : ''))
      setTitle(d.title)
      setMd(d.md)
      setHtml(d.html)
      setPreview('')
      setDirty(false)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  useEffect(() => {
    if (!online) return
    void reloadList()
    if (allowProjectPick) {
      void (async () => {
        try {
          const ps = (await api.listProjects()) ?? []
          setProjects(ps)
          if (ps.length) setPickedProject((p) => p || ps[0].id)
        } catch {
          /* preview simply stays unavailable */
        }
      })()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  function edit(setter: (v: string) => void) {
    return (v: string) => {
      setter(v)
      setDirty(true)
    }
  }

  // fork turns the currently-viewed built-in into a new editable draft (a copy under a fresh id).
  function fork() {
    if (!detail) return
    setSavedId(null) // create path
    setSelectedId(null)
    setBase(detail.builtin ? detail.id : detail.base)
    setTitle(detail.title + ' (copy)')
    setDetail({ ...detail, builtin: false })
    setDirty(true)
  }

  function startNew() {
    setSavedId(null)
    setSelectedId(null)
    setDetail({ id: '', title: '', kind: 'custom', base: '', md: '', html: '', builtin: false })
    setBase('')
    setTitle('')
    setMd(BLANK_MD)
    setHtml(BLANK_HTML)
    setPreview('')
    setDirty(true)
    setError('')
  }

  async function runPreview() {
    if (!previewProject) return
    setBusy(true)
    setError('')
    try {
      setPreview(await api.previewReportTemplate({ project_id: previewProject, format: 'html', md, html }))
    } catch (e) {
      setError('Preview: ' + (e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function save() {
    setBusy(true)
    setError('')
    try {
      const body = { title: title.trim(), base, md, html }
      const saved = savedId
        ? await api.updateReportTemplate(savedId, body)
        : await api.createReportTemplate(body)
      setDirty(false)
      onChanged?.()
      await reloadList(saved.id)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!savedId) return
    if (!window.confirm(`Delete template "${title}"? Reports already generated are unaffected.`)) return
    setBusy(true)
    try {
      await api.deleteReportTemplate(savedId)
      onChanged?.()
      setSelectedId(null)
      setSavedId(null)
      setDetail(null)
      await reloadList()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rte-body">
      <aside className="rte-rail">
        <button className="rte-new" onClick={startNew} disabled={!online}>＋ New template</button>
        <ul className="rte-list">
          {list.map((t) => (
            <li
              key={t.id}
              className={'rte-item' + (t.id === selectedId ? ' active' : '')}
              onClick={() => void selectTemplate(t.id)}
            >
              <span className="rte-item-title">{t.title}</span>
              <span className={'rte-tag ' + (t.builtin ? 'builtin' : 'custom')}>
                {t.builtin ? 'built-in' : 'custom'}
              </span>
            </li>
          ))}
        </ul>
      </aside>

      <div className="rte-editor">
        {error && <div className="banner error">⚠ {error}</div>}
        <div className="rte-toolbar">
          <input
            className="pbuild-in grow"
            placeholder="Template title"
            value={title}
            readOnly={isBuiltin}
            onChange={(e) => edit(setTitle)(e.target.value)}
          />
          {dirty && <span className="rte-dirty" title="Unsaved changes">● unsaved</span>}
          {isBuiltin ? (
            <button className="primary" onClick={fork} disabled={!online}>Fork to edit</button>
          ) : (
            <>
              <button className="primary" onClick={save} disabled={!online || busy || !canSave}>
                {busy ? 'Saving…' : savedId ? 'Save' : 'Create'}
              </button>
              {savedId && (
                <button className="del" title="Delete template" onClick={remove} disabled={!online || busy}>Delete</button>
              )}
            </>
          )}
        </div>
        {isBuiltin && (
          <div className="rte-note">
            This is a built-in template (read-only). Fork it to make an editable copy.
          </div>
        )}

        <div className="rte-cols">
          <div className="rte-source">
            <label className="rte-lbl">Markdown template</label>
            <textarea
              className="rte-code"
              spellCheck={false}
              value={md}
              readOnly={isBuiltin}
              onChange={(e) => edit(setMd)(e.target.value)}
            />
            <label className="rte-lbl">HTML template</label>
            <textarea
              className="rte-code"
              spellCheck={false}
              value={html}
              readOnly={isBuiltin}
              onChange={(e) => edit(setHtml)(e.target.value)}
            />
            <div className="rte-fields">
              <span className="hint">
                Go <code>text/template</code> syntax. Fields: <code>.Project</code>, <code>.Findings</code>,
                {' '}<code>.Summary</code>, <code>.Scope</code>, <code>.Methodology</code>, <code>.Brand</code>,
                {' '}<code>.ExecutiveSummary</code>. Helpers: <code>sevs</code>, <code>sevfmt</code>,
                {' '}<code>cwegroups</code>, <code>date</code>.
              </span>
            </div>
          </div>
          <div className="rte-preview">
            <div className="rte-preview-h">
              <label className="rte-lbl">Live preview</label>
              {allowProjectPick ? (
                <select
                  className="rte-projsel"
                  value={pickedProject}
                  onChange={(e) => setPickedProject(e.target.value)}
                  title="Project whose data the preview renders against"
                >
                  {projects.length === 0 && <option value="">no projects</option>}
                  {projects.map((p) => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </select>
              ) : (
                <span className="muted">· against this project's data</span>
              )}
              <span className="grow" />
              <button onClick={runPreview} disabled={!online || busy || !previewProject || !md.trim() || !html.trim()}>
                {busy ? 'Rendering…' : 'Preview'}
              </button>
            </div>
            {preview ? (
              <iframe className="rte-frame" title="Report preview" srcDoc={preview} sandbox="" />
            ) : (
              <div className="rte-frame empty">
                {previewProject
                  ? <>Click <b>Preview</b> to render this template against real data.</>
                  : 'Create a project to preview a template against its data.'}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

// ReportTemplateEditor is the Reports-page overlay: the shared panel wrapped in a modal, previewing against
// the open project. Editing is also available globally in the Library (ReportTemplatesLibrary).
export function ReportTemplateEditor({
  projectId,
  online,
  onClose,
  onChanged,
}: {
  projectId: string
  online: boolean
  onClose: () => void
  onChanged: () => void
}) {
  return (
    <div className="rte-overlay" role="dialog" aria-label="Report template editor">
      <div className="rte-panel">
        <div className="rte-head">
          <b>Report templates</b>
          <span className="muted">Fork a built-in or author your own — used the next time you generate.</span>
          <span className="grow" />
          <button className="ghost-btn" onClick={onClose}>Close</button>
        </div>
        <ReportTemplatesPanel online={online} previewProjectId={projectId} onChanged={onChanged} />
      </div>
    </div>
  )
}

// ReportTemplatesLibrary is the global Library section: the same editor inline, with a project selector for
// the preview (the Library is not scoped to a project).
export function ReportTemplatesLibrary({ online }: { online: boolean }) {
  return (
    <div className="lib-section rte-inline">
      <div className="lib-head">
        <h2>Report templates</h2>
        <p>
          Fork a built-in report template or author your own. Custom templates appear in the generate menu
          on each project's Reports page.
        </p>
      </div>
      <ReportTemplatesPanel online={online} allowProjectPick />
    </div>
  )
}

const BLANK_MD = `# {{.Project.Name}} — Report

_Generated {{date .GeneratedAt}}_

## Findings

{{if .Findings}}{{range .Findings}}- **[{{sevfmt .Severity}}]** {{.Title}} — _{{.AppName}}_
{{end}}{{else}}_No reportable findings._
{{end}}`

const BLANK_HTML = `<!doctype html><html><head><meta charset="utf-8">
<title>{{.Project.Name}} — Report</title></head><body>
<h1>{{.Project.Name}}</h1>
<p>Generated {{date .GeneratedAt}}</p>
<h2>Findings</h2>
{{if .Findings}}<ul>{{range .Findings}}<li><b>[{{sevfmt .Severity}}]</b> {{.Title}} — {{.AppName}}</li>{{end}}</ul>{{else}}<p><em>No reportable findings.</em></p>{{end}}
</body></html>`
