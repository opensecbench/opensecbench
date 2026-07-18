import { useEffect, useMemo, useRef, useState, type WheelEvent as ReactWheelEvent } from 'react'
import { api, Graph, GraphNode, Project } from './api'

// nodeColor maps a node's kind/group to a fill.
function nodeColor(n: GraphNode): string {
  switch (n.kind) {
    case 'project':
      return '#4aa8ff'
    case 'application':
      return '#7c5cff'
    case 'host':
      return '#8792a5'
    case 'asset':
      return n.group === 'open_source' ? '#46c07a' : '#f0a83c'
    case 'finding':
      return sevColor(n.group)
    case 'endpoint':
      return statusColor(n.group)
    default:
      return '#5c6675'
  }
}
function sevColor(s?: string): string {
  return { critical: '#7c1d1d', high: '#dc2626', medium: '#f59e0b', low: '#3b82f6', info: '#6b7280' }[s ?? ''] ?? '#6b7280'
}
function statusColor(s?: string): string {
  return { '2xx': '#46c07a', '3xx': '#3b82f6', '4xx': '#f0a83c', '5xx': '#dc2626' }[s ?? ''] ?? '#8792a5'
}

const NODE_W = 168
const NODE_H = 26
const COL_W = 230
const ROW_H = 40

interface Placed extends GraphNode {
  x: number
  y: number
}

// layout assigns each node a depth (longest path from a root) and stacks nodes per depth column.
function layout(g: Graph): { nodes: Placed[]; byID: Record<string, Placed> } {
  const incoming: Record<string, number> = {}
  const children: Record<string, string[]> = {}
  g.nodes.forEach((n) => {
    incoming[n.id] = 0
    children[n.id] = []
  })
  g.edges.forEach((e) => {
    if (children[e.from]) children[e.from].push(e.to)
    if (incoming[e.to] !== undefined) incoming[e.to]++
  })
  const depth: Record<string, number> = {}
  const queue = g.nodes.filter((n) => incoming[n.id] === 0).map((n) => n.id)
  queue.forEach((id) => (depth[id] = 0))
  const seen = new Set(queue)
  while (queue.length) {
    const id = queue.shift() as string
    for (const c of children[id] ?? []) {
      depth[c] = Math.max(depth[c] ?? 0, (depth[id] ?? 0) + 1)
      if (!seen.has(c)) {
        seen.add(c)
        queue.push(c)
      }
    }
  }
  const perDepth: Record<number, number> = {}
  const placed: Placed[] = g.nodes.map((n) => {
    const d = depth[n.id] ?? 0
    const row = perDepth[d] ?? 0
    perDepth[d] = row + 1
    return { ...n, x: 20 + d * COL_W, y: 20 + row * ROW_H }
  })
  const byID: Record<string, Placed> = {}
  placed.forEach((p) => (byID[p.id] = p))
  return { nodes: placed, byID }
}

export function GraphTab({
  project,
  online,
  onError,
}: {
  project: Project
  online: boolean
  onError: (m: string) => void
}) {
  const [kind, setKind] = useState<'structure' | 'traffic' | 'topology' | 'dependency'>('structure')
  const [graph, setGraph] = useState<Graph | null>(null)
  const [hover, setHover] = useState<Placed | null>(null)
  const [tx, setTx] = useState(0)
  const [ty, setTy] = useState(0)
  const [scale, setScale] = useState(1)
  const drag = useRef<{ x: number; y: number } | null>(null)

  useEffect(() => {
    if (!online) return
    void (async () => {
      try {
        setGraph(await api.projectGraph(project.id, kind))
        setTx(0)
        setTy(0)
        setScale(1)
      } catch (e) {
        onError((e as Error).message)
      }
    })()
  }, [online, project.id, kind, onError])

  const { nodes, byID } = useMemo(() => (graph ? layout(graph) : { nodes: [], byID: {} }), [graph])

  function onWheel(e: ReactWheelEvent) {
    e.preventDefault()
    setScale((s) => Math.min(3, Math.max(0.3, s * (e.deltaY > 0 ? 0.9 : 1.1))))
  }

  return (
    <section className="panel">
      <div className="panel-head">
        Graph
        <span className="graph-tabs">
          {(['structure', 'traffic', 'topology', 'dependency'] as const).map((k) => (
            <button key={k} className={`tab ${kind === k ? 'on' : ''}`} onClick={() => setKind(k)}>
              {k[0].toUpperCase() + k.slice(1)}
            </button>
          ))}
        </span>
      </div>
      <p className="hint">
        {{
          structure: 'Project → applications → assets & findings.',
          traffic: 'Hosts → endpoints from captured traffic (proxy + replay).',
          topology: 'Hosts → open ports from nmap scans.',
          dependency: 'Components → dependencies from the latest syft SBOM.',
        }[kind]}{' '}
        Drag to pan, scroll to zoom.
      </p>

      {!graph || graph.nodes.length === 0 ? (
        <div className="empty">Nothing to graph yet.</div>
      ) : (
        <div className="graph-wrap">
          <svg
            className="graph-svg"
            onWheel={onWheel}
            onMouseDown={(e) => (drag.current = { x: e.clientX, y: e.clientY })}
            onMouseUp={() => (drag.current = null)}
            onMouseLeave={() => (drag.current = null)}
            onMouseMove={(e) => {
              if (!drag.current) return
              setTx((v) => v + (e.clientX - drag.current!.x))
              setTy((v) => v + (e.clientY - drag.current!.y))
              drag.current = { x: e.clientX, y: e.clientY }
            }}
          >
            <g transform={`translate(${tx},${ty}) scale(${scale})`}>
              {graph.edges.map((edge, i) => {
                const a = byID[edge.from]
                const b = byID[edge.to]
                if (!a || !b) return null
                return (
                  <line
                    key={i}
                    x1={a.x + NODE_W}
                    y1={a.y + NODE_H / 2}
                    x2={b.x}
                    y2={b.y + NODE_H / 2}
                    stroke="#2f3a4d"
                    strokeWidth={1}
                  />
                )
              })}
              {nodes.map((n) => (
                <g key={n.id} onMouseEnter={() => setHover(n)} onMouseLeave={() => setHover(null)} style={{ cursor: 'default' }}>
                  <rect x={n.x} y={n.y} width={NODE_W} height={NODE_H} rx={5} fill={nodeColor(n)} opacity={0.9} />
                  <text x={n.x + 8} y={n.y + NODE_H / 2 + 4} fill="#fff" fontSize={11} fontFamily="sans-serif">
                    {n.label.length > 24 ? n.label.slice(0, 23) + '…' : n.label}
                  </text>
                </g>
              ))}
            </g>
          </svg>
          {hover && (
            <div className="graph-tip">
              <b>{hover.label}</b>
              <span className="muted"> · {hover.kind}{hover.group ? ` · ${hover.group}` : ''}{hover.meta ? ` · ${hover.meta}` : ''}</span>
            </div>
          )}
        </div>
      )}
    </section>
  )
}
