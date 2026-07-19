import { useEffect, useState } from 'react'
import { api, ApprovalRule } from './api'

// ApprovalPolicy edits the trust-curve approval policy (ADR-0019): every sensitive action asks for
// approval by default; flip one to Auto once it's earned trust. Scope and DLP are enforced separately
// and unaffected. The editor manages global (any-profile) rules; profile-scoped rules set via the API
// are preserved untouched.
export function ApprovalPolicy({ online }: { online: boolean }) {
  const [tools, setTools] = useState<string[]>([])
  const [rules, setRules] = useState<ApprovalRule[]>([])
  const [saving, setSaving] = useState('')
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!online) return
    void api
      .getApprovalPolicy()
      .then((p) => {
        setTools((p.sensitive_tools ?? []).slice().sort())
        setRules(p.rules ?? [])
      })
      .catch((e) => setError((e as Error).message))
  }, [online])

  const decisionFor = (tool: string): 'auto' | 'approve' =>
    rules.find((r) => r.tool === tool && !r.profile)?.decision ?? 'approve'

  async function setDecision(tool: string, decision: 'auto' | 'approve') {
    if (decisionFor(tool) === decision) return
    // Keep profile-scoped rules; replace this tool's global rule. 'approve' is the base, so we simply
    // drop the override rather than storing it.
    const next = rules.filter((r) => !(r.tool === tool && !r.profile))
    if (decision === 'auto') next.push({ tool, decision: 'auto' })
    setSaving(tool)
    setError(null)
    try {
      const saved = await api.setApprovalPolicy(next)
      setRules(saved.rules ?? next)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving('')
    }
  }

  return (
    <div className="policy">
      <div className="prov-add-title">Approvals</div>
      <div className="policy-note">
        Each action asks before it runs. Flip one to <b>Auto</b> once you trust it — scope and data-egress
        limits still apply either way.
      </div>
      {error && <div className="banner error">⚠ {error}</div>}
      {tools.map((tool) => {
        const d = decisionFor(tool)
        return (
          <div key={tool} className="policy-row">
            <code>{tool}</code>
            <span className="grow" />
            <div className={`policy-toggle ${saving === tool ? 'saving' : ''}`}>
              <button className={d === 'approve' ? 'on' : ''} disabled={!online} onClick={() => setDecision(tool, 'approve')}>
                Ask
              </button>
              <button className={d === 'auto' ? 'on auto' : ''} disabled={!online} onClick={() => setDecision(tool, 'auto')}>
                Auto
              </button>
            </div>
          </div>
        )
      })}
    </div>
  )
}
