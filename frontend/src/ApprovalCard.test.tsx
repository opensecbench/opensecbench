import { screen } from './test/utils'
import { renderUI } from './test/utils'
import { ApprovalCard } from './ApprovalCard'
import { Approval } from './api'

function approval(over: Partial<Approval> = {}): Approval {
  return {
    id: 'ap1',
    thread_id: 't1',
    tool: 'delegate',
    args: { agent: 'pentester', task: 'probe' },
    status: 'pending',
    created_at: '2026-08-03T00:00:00Z',
    ...over,
  }
}

describe('ApprovalCard', () => {
  it('shows the tool and its arguments', () => {
    renderUI(<ApprovalCard approval={approval()} busy={false} onDecide={() => {}} />)
    const code = screen.getByText(/delegate/)
    expect(code).toHaveTextContent('"agent":"pentester"')
  })

  it('lists authorized tools only when present (ADR-0070 informed consent)', () => {
    const { rerender } = renderUI(
      <ApprovalCard approval={approval()} busy={false} onDecide={() => {}} />,
    )
    expect(screen.queryByText(/Approving authorizes/)).toBeNull()

    rerender(
      <ApprovalCard
        approval={approval({ authorized_tools: ['run_code', 'send_request'] })}
        busy={false}
        onDecide={() => {}}
      />,
    )
    expect(screen.getByText(/Approving authorizes this sub-agent to use:/)).toBeInTheDocument()
    expect(screen.getByText('run_code, send_request')).toBeInTheDocument()
  })

  it('calls onDecide with the chosen decision', async () => {
    const onDecide = vi.fn()
    const { user } = renderUI(<ApprovalCard approval={approval()} busy={false} onDecide={onDecide} />)

    await user.click(screen.getByRole('button', { name: /Approve/ }))
    expect(onDecide).toHaveBeenCalledWith('approve')

    await user.click(screen.getByRole('button', { name: /Deny/ }))
    expect(onDecide).toHaveBeenCalledWith('deny')
  })

  it('disables both buttons while a decision is in flight', () => {
    renderUI(<ApprovalCard approval={approval()} busy={true} onDecide={() => {}} />)
    expect(screen.getByRole('button', { name: /Approve/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Deny/ })).toBeDisabled()
  })
})
