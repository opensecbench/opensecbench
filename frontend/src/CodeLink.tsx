import { Observation } from './api'

// Shared click-to-file plumbing (ADR-0050): turning an observation's source location into a jump that opens
// the file in CodeView. Used by the Findings surface, the Explorer, and Investigations, so it lives in one
// place rather than being reimplemented per surface.

export type OpenCode = (assetId: string, path: string, line?: number) => void

// parseLoc splits a scanner location "path:line" into its parts (split on the LAST colon). A bare path with
// no line is returned as-is. Callers only treat it as a source jump when the observation also resolved to an
// asset_id — that presence is what distinguishes a code scanner's file path from nmap's "host:port/proto".
export function parseLoc(loc: string): { path: string; line?: number } {
  const m = loc.match(/^(.*):(\d+)$/)
  if (m) return { path: m[1], line: parseInt(m[2], 10) }
  return { path: loc }
}

function JumpChip({ assetId, loc, label, onOpenCode }: { assetId: string; loc: string; label?: string; onOpenCode: OpenCode }) {
  const { path, line } = parseLoc(loc)
  return (
    <button className="loc-chip" title={`Open ${loc}`} onClick={() => onOpenCode(assetId, path, line)}>
      ↦ {label && <b className="loc-label">{label}</b>}{loc}
    </button>
  )
}

// LocationChip renders an observation's source location(s). When the observation resolved to a source asset,
// each is a clickable jump; otherwise it is plain monospace text. Taint findings (ADR-0032) also carry
// `attributes.dataflow_source` — where untrusted input enters — so those get a second "source" jump alongside
// the sink, both in the same repo.
export function LocationChip({ obs, onOpenCode }: { obs: Observation; onOpenCode: OpenCode }) {
  if (!obs.location) return null
  if (!obs.asset_id) return <span className="loc-plain">{obs.location}</span>
  const source = obs.attributes?.dataflow_source
  const hasFlow = !!source && source !== obs.location
  return (
    <>
      {hasFlow && <JumpChip assetId={obs.asset_id} loc={source!} label="source" onOpenCode={onOpenCode} />}
      <JumpChip assetId={obs.asset_id} loc={obs.location} label={hasFlow ? 'sink' : undefined} onOpenCode={onOpenCode} />
    </>
  )
}
