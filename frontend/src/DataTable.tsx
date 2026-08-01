import { ReactNode, useState, type MouseEvent as ReactMouseEvent } from 'react'

// A compact, spreadsheet-style table shared across surfaces (Observations first; Findings/Investigations
// can adopt it). It owns sort state; the parent owns filtering, selection, and row-click, so each surface
// composes its own toolbar, bulk actions, and detail panel around the same table + interaction model.

export interface Column<T> {
  key: string
  header: ReactNode
  render: (row: T) => ReactNode
  sortable?: boolean
  sortValue?: (row: T) => string | number // required for a sortable column
  className?: string
  width?: string
  align?: 'left' | 'right' | 'center'
}

interface SortState {
  key: string
  dir: 'asc' | 'desc'
}

export function DataTable<T extends { id: string }>({
  rows,
  columns,
  getRowClass,
  onRowClick,
  onRowContextMenu,
  activeId,
  selectable = false,
  selected,
  onSelectChange,
  defaultSort,
  empty = 'Nothing here.',
}: {
  rows: T[]
  columns: Column<T>[]
  getRowClass?: (row: T) => string
  onRowClick?: (row: T) => void
  onRowContextMenu?: (row: T, e: ReactMouseEvent) => void
  activeId?: string
  selectable?: boolean
  selected?: Set<string>
  onSelectChange?: (next: Set<string>) => void
  defaultSort?: SortState
  empty?: ReactNode
}) {
  const [sort, setSort] = useState<SortState | null>(defaultSort ?? null)

  const sorted = (() => {
    if (!sort) return rows
    const col = columns.find((c) => c.key === sort.key)
    if (!col?.sortValue) return rows
    const sv = col.sortValue
    const dir = sort.dir === 'asc' ? 1 : -1
    return [...rows].sort((a, b) => {
      const av = sv(a)
      const bv = sv(b)
      if (av < bv) return -dir
      if (av > bv) return dir
      return 0
    })
  })()

  function toggleSort(key: string) {
    setSort((s) => (s?.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' }))
  }

  const allSelected = selectable && !!selected && rows.length > 0 && rows.every((r) => selected.has(r.id))
  function toggleAll() {
    if (!onSelectChange || !selected) return
    const next = new Set(selected)
    if (allSelected) rows.forEach((r) => next.delete(r.id))
    else rows.forEach((r) => next.add(r.id))
    onSelectChange(next)
  }
  function toggleOne(id: string) {
    if (!onSelectChange || !selected) return
    const next = new Set(selected)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    onSelectChange(next)
  }

  const colCount = columns.length + (selectable ? 1 : 0)
  return (
    <div className="data-table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            {selectable && (
              <th className="dt-check">
                <input type="checkbox" checked={!!allSelected} onChange={toggleAll} aria-label="Select all" />
              </th>
            )}
            {columns.map((c) => (
              <th
                key={c.key}
                className={`${c.className ?? ''} ${c.sortable ? 'sortable' : ''} ${sort?.key === c.key ? 'sorted' : ''}`}
                style={{ width: c.width, textAlign: c.align }}
                onClick={c.sortable ? () => toggleSort(c.key) : undefined}
              >
                {c.header}
                {c.sortable && <span className="dt-arrow">{sort?.key === c.key ? (sort.dir === 'asc' ? '▲' : '▼') : ''}</span>}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {sorted.length === 0 ? (
            <tr>
              <td className="dt-empty" colSpan={colCount}>{empty}</td>
            </tr>
          ) : (
            sorted.map((row) => (
              <tr
                key={row.id}
                className={`${getRowClass?.(row) ?? ''} ${activeId === row.id ? 'active' : ''} ${onRowClick ? 'clickable' : ''}`}
                onClick={onRowClick ? () => onRowClick(row) : undefined}
                onContextMenu={onRowContextMenu ? (e) => onRowContextMenu(row, e) : undefined}
              >
                {selectable && (
                  <td className="dt-check" onClick={(e) => e.stopPropagation()}>
                    <input type="checkbox" checked={selected?.has(row.id) ?? false} onChange={() => toggleOne(row.id)} aria-label="Select row" />
                  </td>
                )}
                {columns.map((c) => (
                  <td key={c.key} className={c.className} style={{ textAlign: c.align }}>{c.render(row)}</td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  )
}
