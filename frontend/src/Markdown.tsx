import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

// Markdown renders CommonMark + GitHub-Flavored Markdown (tables, task lists, strikethrough, autolinks) via
// react-markdown. Raw HTML is NOT enabled (no rehype-raw), so untrusted model/authored input can't inject
// markup, and react-markdown's default urlTransform strips dangerous link protocols (javascript:, etc.).
// The `.md` wrapper carries all styling (see styles.css). Same `source` prop the old hand-rolled renderer
// exposed, so call sites are unchanged.
export function Markdown({ source }: { source: string }) {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // Open links in a new tab without leaking the referrer or window handle.
          a({ node, ...props }) {
            return <a {...props} target="_blank" rel="noreferrer noopener" />
          },
        }}
      >
        {source}
      </ReactMarkdown>
    </div>
  )
}
