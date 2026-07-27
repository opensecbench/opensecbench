import { useEffect, useState } from 'react'
import { api, ReportTemplate, ReportTemplateDetail } from './api'

// ReportTemplateEditor is the full-screen editor for report templates. It lists the built-in and saved
// templates; selecting a built-in shows its source read-only with a "Fork" action, selecting a saved one
// edits it in place. The right pane live-previews the current draft against the active project's real data
// (via /report-templates/preview, which renders without saving). Mirrors the fork/edit split the
// MethodologyBuilder uses: built-ins are immutable, editing one saves a copy under a new id.
export function ReportTemplateEditor({
  projectId,
  online,
  onClose,
  onChanged,
}: {
  projectId: string
  online: boolean
  onClose: () => void
  onChanged: () => void // notify the parent so its template dropdown refreshes
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
    if (online) void reloadList()
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
    setBusy(true)
    setError('')
    try {
      setPreview(await api.previewReportTemplate({ project_id: projectId, format: 'html', md, html }))
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
      onChanged()
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
      onChanged()
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
    <div className="rte-overlay" role="dialog" aria-label="Report template editor">
      <div className="rte-panel">
        <div className="rte-head">
          <b>Report templates</b>
          <span className="muted">Fork a built-in or author your own — used the next time you generate.</span>
          <span className="grow" />
          {dirty && <span className="rte-dirty" title="Unsaved changes">● unsaved</span>}
          <button className="ghost-btn" onClick={onClose}>Close</button>
        </div>
        {error && <div className="banner error">⚠ {error}</div>}
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
            <div className="rte-toolbar">
              <input
                className="pbuild-in grow"
                placeholder="Template title"
                value={title}
                readOnly={isBuiltin}
                onChange={(e) => edit(setTitle)(e.target.value)}
              />
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
                  <label className="rte-lbl">Live preview <span className="muted">· against this project's data</span></label>
                  <span className="grow" />
                  <button onClick={runPreview} disabled={!online || busy || !md.trim() || !html.trim()}>
                    {busy ? 'Rendering…' : 'Preview'}
                  </button>
                </div>
                {preview ? (
                  <iframe className="rte-frame" title="Report preview" srcDoc={preview} sandbox="" />
                ) : (
                  <div className="rte-frame empty">Click <b>Preview</b> to render this template against the current project.</div>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
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
