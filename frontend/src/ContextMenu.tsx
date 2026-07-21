import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'

// A reusable right-click context menu (ADR-0016 UX seam). The native webview menu is suppressed on
// build (and, once we call preventDefault, in `make dev` too), so panels render their own menu from a
// list of items. Keep this primitive dumb: it positions, clamps, and dismisses — callers supply items.

export interface MenuItem {
  id: string
  label: string
  icon?: ReactNode
  disabled?: boolean
  onSelect: () => void | Promise<void>
}

export interface MenuState<T> {
  x: number
  y: number
  payload: T
}

// useContextMenu tracks the open menu and the payload it was opened over (e.g. the HTTP exchange under
// the cursor). `open` is meant for an onContextMenu handler: it suppresses the native menu and records
// the cursor position + payload.
export function useContextMenu<T>() {
  const [menu, setMenu] = useState<MenuState<T> | null>(null)
  return {
    menu,
    open: (e: { preventDefault(): void; clientX: number; clientY: number }, payload: T) => {
      e.preventDefault()
      setMenu({ x: e.clientX, y: e.clientY, payload })
    },
    close: () => setMenu(null),
  }
}

// ContextMenu renders a floating menu at (x, y), clamped inside the viewport, and dismisses on outside
// click / Escape / scroll / resize / blur. Render it only while a menu is open.
export function ContextMenu({
  x,
  y,
  items,
  onClose,
}: {
  x: number
  y: number
  items: MenuItem[]
  onClose: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ x, y })

  // Measure the menu, then nudge it back inside the window if it would overflow the right/bottom edge —
  // matters in a fixed 1280×820 window where a right-click near a corner is common.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    const nx = Math.max(6, Math.min(x, window.innerWidth - width - 6))
    const ny = Math.max(6, Math.min(y, window.innerHeight - height - 6))
    setPos({ x: nx, y: ny })
  }, [x, y])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    // A pointerdown anywhere dismisses; our own menu stops propagation so clicks on items don't self-close
    // before their onClick runs. This handler is attached only while the menu is mounted, so the very
    // right-click that opened it (which already fired) can't immediately close it.
    window.addEventListener('keydown', onKey)
    window.addEventListener('pointerdown', onClose)
    window.addEventListener('resize', onClose)
    window.addEventListener('blur', onClose)
    window.addEventListener('scroll', onClose, true) // capture: catch scrolls in any nested container
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('pointerdown', onClose)
      window.removeEventListener('resize', onClose)
      window.removeEventListener('blur', onClose)
      window.removeEventListener('scroll', onClose, true)
    }
  }, [onClose])

  if (items.length === 0) return null
  return (
    <div
      ref={ref}
      className="ctx-menu"
      style={{ left: pos.x, top: pos.y }}
      onPointerDown={(e) => e.stopPropagation()}
      onContextMenu={(e) => e.preventDefault()}
    >
      {items.map((it) => (
        <button
          key={it.id}
          className="ctx-item"
          disabled={it.disabled}
          onClick={() => { void it.onSelect(); onClose() }}
        >
          {it.icon != null && <span className="ctx-ico">{it.icon}</span>}
          <span className="ctx-lbl">{it.label}</span>
        </button>
      ))}
    </div>
  )
}
