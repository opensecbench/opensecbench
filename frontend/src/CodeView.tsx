import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import hljs from 'highlight.js/lib/common'
// Languages beyond highlight.js's ~40 "common" set that an appsec engineer routinely reads — infra/config,
// JVM/.NET ecosystem, and functional/scripting languages. Registered on top of the common bundle so coverage
// is broad without pulling the full ~190-language bundle (ADR-0050).
import powershell from 'highlight.js/lib/languages/powershell'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import scala from 'highlight.js/lib/languages/scala'
import groovy from 'highlight.js/lib/languages/groovy'
import dart from 'highlight.js/lib/languages/dart'
import elixir from 'highlight.js/lib/languages/elixir'
import erlang from 'highlight.js/lib/languages/erlang'
import clojure from 'highlight.js/lib/languages/clojure'
import haskell from 'highlight.js/lib/languages/haskell'
import nginx from 'highlight.js/lib/languages/nginx'
import apache from 'highlight.js/lib/languages/apache'
import properties from 'highlight.js/lib/languages/properties'
import protobuf from 'highlight.js/lib/languages/protobuf'
import x86asm from 'highlight.js/lib/languages/x86asm'
import ocaml from 'highlight.js/lib/languages/ocaml'
import fsharp from 'highlight.js/lib/languages/fsharp'
import nim from 'highlight.js/lib/languages/nim'
import julia from 'highlight.js/lib/languages/julia'
import pgsql from 'highlight.js/lib/languages/pgsql'
import coffeescript from 'highlight.js/lib/languages/coffeescript'
import handlebars from 'highlight.js/lib/languages/handlebars'
import twig from 'highlight.js/lib/languages/twig'
import vim from 'highlight.js/lib/languages/vim'
import { api, SourceFile } from './api'

// CodeView renders one source file (ADR-0050): line-numbered, syntax-highlighted, auto-scrolled to and
// highlighting a target line. It is read-only — a viewer for evidence, not an editor. Opened as a document
// in the Workbench editor area; the file bytes come from the path-confined /v1/assets/{id}/source endpoint.

type LanguageFn = Parameters<typeof hljs.registerLanguage>[1]
const extraLanguages: Record<string, LanguageFn> = {
  powershell, dockerfile, scala, groovy, dart, elixir, erlang, clojure, haskell, nginx, apache,
  properties, protobuf, x86asm, ocaml, fsharp, nim, julia, pgsql, coffeescript, handlebars, twig, vim,
}
for (const [name, fn] of Object.entries(extraLanguages)) {
  if (!hljs.getLanguage(name)) hljs.registerLanguage(name, fn)
}

// extLang maps a file extension to a highlight.js language id. Anything unmapped falls back to auto-detection,
// then to plain text — the viewer never fails to render, it just may not colour.
const extLang: Record<string, string> = {
  ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  py: 'python', pyw: 'python', go: 'go', rb: 'ruby', rake: 'ruby', php: 'php', phtml: 'php',
  java: 'java', kt: 'kotlin', kts: 'kotlin', rs: 'rust', scala: 'scala', sbt: 'scala', groovy: 'groovy',
  gradle: 'groovy', c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', cxx: 'cpp', hpp: 'cpp', hh: 'cpp',
  cs: 'csharp', swift: 'swift', dart: 'dart', ex: 'elixir', exs: 'elixir', erl: 'erlang', hrl: 'erlang',
  clj: 'clojure', cljs: 'clojure', hs: 'haskell', ml: 'ocaml', mli: 'ocaml', fs: 'fsharp', fsx: 'fsharp',
  nim: 'nim', jl: 'julia', lua: 'lua', pl: 'perl', pm: 'perl', r: 'r', m: 'objectivec', mm: 'objectivec',
  sh: 'bash', bash: 'bash', zsh: 'bash', ps1: 'powershell', psm1: 'powershell', bat: 'dos', cmd: 'dos',
  vim: 'vim', coffee: 'coffeescript',
  sql: 'sql', pgsql: 'pgsql', psql: 'pgsql', json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'ini',
  ini: 'ini', cfg: 'ini', conf: 'ini', properties: 'properties', proto: 'protobuf',
  xml: 'xml', html: 'xml', htm: 'xml', xhtml: 'xml', vue: 'xml', svelte: 'xml', svg: 'xml',
  css: 'css', scss: 'scss', sass: 'scss', less: 'less', md: 'markdown', markdown: 'markdown',
  tf: 'ini', hcl: 'ini', asm: 'x86asm', s: 'x86asm', diff: 'diff', patch: 'diff', graphql: 'graphql',
  gql: 'graphql', hbs: 'handlebars', twig: 'twig', jinja: 'twig', j2: 'twig',
}

// Filenames (no useful extension) whose whole name identifies the language.
const nameLang: Record<string, string> = {
  dockerfile: 'dockerfile', makefile: 'makefile', 'nginx.conf': 'nginx', gemfile: 'ruby',
  rakefile: 'ruby', 'cmakelists.txt': 'cmake', vimrc: 'vim', '.vimrc': 'vim',
}

function langFor(path: string): string | null {
  const base = (path.split('/').pop() ?? '').toLowerCase()
  if (nameLang[base]) return nameLang[base]
  const ext = base.includes('.') ? base.split('.').pop()! : ''
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
