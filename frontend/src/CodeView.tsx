import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import hljs from 'highlight.js/lib/common'
import { api, SourceFile } from './api'

// CodeView renders one source file (ADR-0050): line-numbered, syntax-highlighted, auto-scrolled to and
// highlighting a target line. It is read-only — a viewer for evidence, not an editor. Opened as a document
// in the Workbench editor area; the file bytes come from the path-confined /v1/assets/{id}/source endpoint.

// extLang maps a file extension to a highlight.js language id. Anything unmapped falls back to auto-detection,
// then to plain text — the viewer never fails to render, it just may not colour.
const extLang: Record<string, string> = {
  ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript', mjs: 'javascript',
  py: 'python', go: 'go', rb: 'ruby', php: 'php', java: 'java', kt: 'kotlin', rs: 'rust',
  c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', hpp: 'cpp', cs: 'csharp', swift: 'swift', scala: 'scala',
  sh: 'bash', bash: 'bash', zsh: 'bash', ps1: 'powershell', sql: 'sql', json: 'json', yaml: 'yaml',
  yml: 'yaml', toml: 'ini', ini: 'ini', xml: 'xml', html: 'xml', css: 'css', scss: 'scss',
  md: 'markdown', dockerfile: 'dockerfile', makefile: 'makefile',
}

function langFor(path: string): string | null {
  const base = path.split('/').pop() ?? ''
  if (base.toLowerCase() === 'dockerfile') return 'dockerfile'
  if (base.toLowerCase() === 'makefile') return 'makefile'
  const ext = base.includes('.') ? base.split('.').pop()!.toLowerCase() : ''
  return extLang[ext] ?? null
}

// splitHighlighted turns highlight.js's single HTML string into one entry per line, re-opening any spans that
// straddle a newline (e.g. a block comment) so each line is independently valid HTML. This is what makes a
// per-line gutter possible without breaking multi-line tokens.
function splitHighlighted(html: string): string[] {
  const lines = html.split('\n')
  const open: string[] = []
  const tagRe = /<span[^>]*>|<\/span>/g
  return lines.map((line) => {
    const prefix = open.join('')
    let m: RegExpExecArray | null
    while ((m = tagRe.exec(line))) {
      if (m[0] === '</span>') open.pop()
      else open.push(m[0])
    }
    const suffix = '</span>'.repeat(open.length)
    return prefix + line + suffix
  })
}

export function CodeView({
  assetId,
  path,
  line,
  online,
}: {
  assetId: string
  path: string
  line?: number
  online: boolean
}) {
  const [file, setFile] = useState<SourceFile | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const targetRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!online) return
    let alive = true
    setBusy(true)
    setError(null)
    api
      .assetSource(assetId, path)
      .then((f) => alive && setFile(f))
      .catch((e) => alive && setError((e as Error).message))
      .finally(() => alive && setBusy(false))
    return () => {
      alive = false
    }
  }, [assetId, path, online])

  // Highlight once per file, then split into lines for the gutter.
  const lines = useMemo(() => {
    if (!file) return []
    const lang = langFor(path)
    let html: string
    try {
      html = lang && hljs.getLanguage(lang)
        ? hljs.highlight(file.content, { language: lang, ignoreIllegals: true }).value
        : hljs.highlightAuto(file.content).value
    } catch {
      // Never let a highlighter error blank the viewer — fall back to escaped plain text.
      const div = document.createElement('div')
      div.textContent = file.content
      html = div.innerHTML
    }
    return splitHighlighted(html)
  }, [file, path])

  // Scroll the target line into view once rendered.
  useLayoutEffect(() => {
    if (targetRef.current) {
      targetRef.current.scrollIntoView({ block: 'center' })
    }
  }, [lines, line])

  if (!online) return <div className="empty">Offline — source unavailable.</div>
  if (busy && !file) return <div className="empty">Loading {path}…</div>
  if (error) return <div className="banner error wb-banner">⚠ Source unavailable: {error}</div>
  if (!file) return null

  return (
    <div className="cv">
      <div className="cv-head">
        <span className="cv-path">{path}</span>
        <span className="cv-meta">
          {file.lines} lines{line ? ` · line ${line}` : ''}
          {file.truncated && ' · truncated'}
        </span>
      </div>
      <div className="cv-body">
        <pre className="cv-code">
          {lines.map((html, i) => {
            const n = i + 1
            const isTarget = n === line
            return (
              <div
                key={n}
                ref={isTarget ? targetRef : undefined}
                className={`cv-line${isTarget ? ' target' : ''}`}
              >
                <span className="cv-num">{n}</span>
                <code className="cv-text hljs" dangerouslySetInnerHTML={{ __html: html || ' ' }} />
              </div>
            )
          })}
        </pre>
      </div>
    </div>
  )
}
