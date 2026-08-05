import { screen, renderUI } from './test/utils'
import { MessageTurn } from './MessageTurn'
import { Msg } from './api'

function msg(over: Partial<Msg> = {}): Msg {
  return { id: 'm1', thread_id: 't1', seq: 1, role: 'tool', content: '', created_at: '2026-08-04T00:00:00Z', ...over }
}

describe('MessageTurn', () => {
  // The regression this component guards: the chat used to drop a tool turn's content, showing only a bare
  // "tool result" label. Both variants must render the raw output, expandable.
  it.each(['chat', 'transcript'] as const)('renders tool output in an expandable details (%s)', (variant) => {
    const { container } = renderUI(
      <MessageTurn m={msg({ content: 'SARIF: 3 findings\n{"runs":[]}' })} variant={variant} />,
    )
    const details = container.querySelector('details')
    expect(details).not.toBeNull()
    // The full output is present (not just the summary label).
    expect(details?.textContent).toContain('{"runs":[]}')
  })

  it('labels an errored tool turn', () => {
    const { container } = renderUI(<MessageTurn m={msg({ content: 'boom', tool_error: true })} variant="chat" />)
    expect(container.querySelector('details.tool.error')).not.toBeNull()
  })

  it('names the agent per variant', () => {
    renderUI(<MessageTurn m={msg({ role: 'assistant', content: 'hello' })} variant="chat" />)
    expect(screen.getByText('Analyst')).toBeTruthy()
    renderUI(<MessageTurn m={msg({ role: 'assistant', content: 'hello' })} variant="transcript" />)
    expect(screen.getByText('Agent')).toBeTruthy()
  })
})
