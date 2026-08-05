// Custom actions on findings & observations (ADR-0059) — the on-subject equivalent of exchangeActions.
//
// Unlike exchange actions (a hard-coded registry), custom actions are user-authored and fetched from the
// backend. This module holds the client-side applicability filter — which actions to offer for a given
// subject — mirroring the server predicate so the Actions menu shows only what fits, and the mapping of a
// finding/observation into the subject-kind the run endpoints expect.

import { Action, Finding, Observation } from './api'

const SEV_RANK: Record<string, number> = { critical: 5, high: 4, medium: 3, low: 2, info: 1 }

function matches(a: Action, severity: string, status: string, cwe: string): boolean {
  const p = a.applies_when || {}
  if (p.min_severity && (SEV_RANK[(severity || '').toLowerCase()] ?? 0) < (SEV_RANK[p.min_severity.toLowerCase()] ?? 0)) {
    return false
  }
  if (p.statuses && p.statuses.length && !p.statuses.includes(status)) return false
  if (p.cwe_prefixes && p.cwe_prefixes.length && !p.cwe_prefixes.some((pre) => (cwe || '').startsWith(pre))) {
    return false
  }
  return true
}

/** Actions applicable to a finding, in registry order. */
export function actionsForFinding(actions: Action[], f: Finding): Action[] {
  return actions.filter((a) => a.subject_kinds?.includes('finding') && matches(a, f.severity, f.status, f.cwe ?? ''))
}

/** Actions applicable to an observation, in registry order. */
export function actionsForObservation(actions: Action[], o: Observation): Action[] {
  return actions.filter(
    (a) => a.subject_kinds?.includes('observation') && matches(a, o.severity, o.review_state, o.attributes?.cwe ?? ''),
  )
}

/** A short glyph for an action, defaulting by kind when the action declares no icon. */
export function actionIcon(a: Action): string {
  return a.icon || (a.kind === 'script' ? '›_' : '✦')
}
