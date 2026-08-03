import { Approval } from './api'

// The pending-approval prompt shown in the Analyst chat when a gated tool call awaits a human decision.
// Presentational (no data fetching) so it renders from props and is unit-testable; AnalystPanel owns the
// decide() side effect and busy state.
export function ApprovalCard({
  approval,
  busy,
  onDecide,
}: {
  approval: Approval
  busy: boolean
  onDecide: (decision: 'approve' | 'deny') => void
}) {
  const authorizes = approval.authorized_tools ?? []
  return (
    <div className="approval-card">
      <div className="ac-title">⏸ Approval required</div>
      <code>
        {approval.tool} {JSON.stringify(approval.args)}
      </code>
      {authorizes.length > 0 && (
        <div className="ac-authorizes">
          Approving authorizes this sub-agent to use: <b>{authorizes.join(', ')}</b>
        </div>
      )}
      <div className="ac-btns">
        <button className="ok" disabled={busy} onClick={() => onDecide('approve')}>
          ✓ Approve
        </button>
        <button className="no" disabled={busy} onClick={() => onDecide('deny')}>
          ✕ Deny
        </button>
      </div>
    </div>
  )
}
