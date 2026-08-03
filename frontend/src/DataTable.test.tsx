import { screen, within, renderUI } from './test/utils'
import { DataTable, type Column } from './DataTable'

type Row = { id: string; name: string; score: number }

const rows: Row[] = [
  { id: 'a', name: 'Alpha', score: 3 },
  { id: 'b', name: 'Bravo', score: 1 },
  { id: 'c', name: 'Charlie', score: 2 },
]

const columns: Column<Row>[] = [
  { key: 'name', header: 'Name', render: (r) => r.name },
  { key: 'score', header: 'Score', render: (r) => String(r.score), sortable: true, sortValue: (r) => r.score },
]

// Row text in DOM order (skipping the header row), e.g. "Alpha3".
function bodyOrder(): string[] {
  return within(screen.getByRole('table'))
    .getAllByRole('row')
    .slice(1)
    .map((tr) => tr.textContent ?? '')
}

describe('DataTable', () => {
  it('renders a cell per row via the column render fn', () => {
    renderUI(<DataTable rows={rows} columns={columns} />)
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Charlie')).toBeInTheDocument()
  })

  it('shows the empty state when there are no rows', () => {
    renderUI(<DataTable rows={[]} columns={columns} empty="No rows." />)
    expect(screen.getByText('No rows.')).toBeInTheDocument()
  })

  it('calls onRowClick with the clicked row', async () => {
    const onRowClick = vi.fn()
    const { user } = renderUI(<DataTable rows={rows} columns={columns} onRowClick={onRowClick} />)
    await user.click(screen.getByText('Bravo'))
    expect(onRowClick).toHaveBeenCalledWith(expect.objectContaining({ id: 'b' }))
  })

  it('sorts ascending then descending when a sortable header is clicked', async () => {
    const { user } = renderUI(<DataTable rows={rows} columns={columns} />)
    const header = () => screen.getByRole('columnheader', { name: /Score/ })

    await user.click(header())
    expect(bodyOrder()).toEqual(['Bravo1', 'Charlie2', 'Alpha3']) // score ascending

    await user.click(header())
    expect(bodyOrder()).toEqual(['Alpha3', 'Charlie2', 'Bravo1']) // score descending
  })

  it('supports select-all and per-row selection (controlled)', async () => {
    const onSelectChange = vi.fn()
    const { user } = renderUI(
      <DataTable rows={rows} columns={columns} selectable selected={new Set()} onSelectChange={onSelectChange} />,
    )

    await user.click(screen.getByLabelText('Select all'))
    expect(onSelectChange).toHaveBeenCalledWith(new Set(['a', 'b', 'c']))

    onSelectChange.mockClear()
    await user.click(screen.getAllByLabelText('Select row')[0])
    expect(onSelectChange).toHaveBeenCalledWith(new Set(['a']))
  })
})
