import { ReactNode } from 'react'

// Markdown renders the subset of Markdown an LLM emits — headings, bold/italic, inline & fenced code,
// ordered/unordered lists, links, paragraphs — as escaped React elements (never dangerouslySetInnerHTML),
// so untrusted model output can't inject HTML. It's intentionally small, not a spec-complete parser.
export function Markdown({ source }: { source: string }) {
  return <div className="md">{renderBlocks(source)}</div>
}

type Block =
  | { kind: 'code'; lang: string; text: string }
  | { kind: 'heading'; level: number; text: string }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[] }
  | { kind: 'p'; text: string }

function renderBlocks(src: string): ReactNode[] {
  const lines = src.replace(/\r\n/g, '\n').split('\n')
  const blocks: Block[] = []
  let para: string[] = []
  const flushPara = () => {
    if (para.length) {
      blocks.push({ kind: 'p', text: para.join('\n') })
      para = []
    }
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const fence = line.match(/^```(\w*)\s*$/)
    if (fence) {
      flushPara()
      const lang = fence[1]
      const body: string[] = []
      i++
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        body.push(lines[i])
        i++
      }
      blocks.push({ kind: 'code', lang, text: body.join('\n') })
      continue
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/)
    if (heading) {
      flushPara()
      blocks.push({ kind: 'heading', level: heading[1].length, text: heading[2] })
      continue
    }
    const ol = line.match(/^\s*\d+[.)]\s+(.*)$/)
    if (ol) {
      flushPara()
      const items = [ol[1]]
      while (i + 1 < lines.length && /^\s*\d+[.)]\s+/.test(lines[i + 1])) {
        items.push(lines[++i].replace(/^\s*\d+[.)]\s+/, ''))
      }
      blocks.push({ kind: 'ol', items })
      continue
    }
    const ul = line.match(/^\s*[-*+]\s+(.*)$/)
    if (ul) {
      flushPara()
      const items = [ul[1]]
      while (i + 1 < lines.length && /^\s*[-*+]\s+/.test(lines[i + 1])) {
        items.push(lines[++i].replace(/^\s*[-*+]\s+/, ''))
      }
      blocks.push({ kind: 'ul', items })
      continue
    }
    if (line.trim() === '') {
      flushPara()
      continue
    }
    para.push(line)
  }
  flushPara()

  return blocks.map((b, i) => {
    switch (b.kind) {
      case 'code':
        return <pre key={i} className="md-code"><code>{b.text}</code></pre>
      case 'heading': {
        const Tag = `h${Math.min(b.level, 6)}` as keyof JSX.IntrinsicElements
        return <Tag key={i} className="md-h">{renderInline(b.text)}</Tag>
      }
      case 'ul':
        return <ul key={i} className="md-list">{b.items.map((it, j) => <li key={j}>{renderInline(it)}</li>)}</ul>
      case 'ol':
        return <ol key={i} className="md-list">{b.items.map((it, j) => <li key={j}>{renderInline(it)}</li>)}</ol>
      default:
        return <p key={i}>{renderInline(b.text)}</p>
    }
  })
}

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\s][^*]*\*)|(_[^_\s][^_]*_)|(\[[^\]]+\]\([^)]+\))/g

function renderInline(text: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  let m: RegExpExecArray | null
  INLINE.lastIndex = 0
  let k = 0
  while ((m = INLINE.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index))
    const tok = m[0]
    if (tok.startsWith('`')) {
      out.push(<code key={k++} className="md-inline-code">{tok.slice(1, -1)}</code>)
    } else if (tok.startsWith('**')) {
      out.push(<strong key={k++}>{renderInline(tok.slice(2, -2))}</strong>)
    } else if (tok.startsWith('*') || tok.startsWith('_')) {
      out.push(<em key={k++}>{renderInline(tok.slice(1, -1))}</em>)
    } else {
      // [label](url)
      const link = tok.match(/^\[([^\]]+)\]\(([^)]+)\)$/)
      if (link && /^https?:\/\//.test(link[2])) {
        out.push(<a key={k++} href={link[2]} target="_blank" rel="noreferrer noopener">{link[1]}</a>)
      } else if (link) {
        out.push(link[1]) // non-http(s) URL: show the label as plain text (no unsafe href)
      } else {
        out.push(tok)
      }
    }
    last = m.index + tok.length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}
