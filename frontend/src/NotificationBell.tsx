import { useEffect, useRef, useState } from 'react'
import { api, Notification } from './api'

export function NotificationBell({ online }: { online: boolean }) {
  const [items, setItems] = useState<Notification[]>([])
  const [unread, setUnread] = useState(0)
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  async function reload() {
    try {
      const feed = await api.listNotifications(50)
      setItems(feed.notifications ?? [])
      setUnread(feed.unread)
    } catch {
      /* offline; leave as-is */
    }
  }

  useEffect(() => {
    if (!online) return
    void reload()
    const timer = setInterval(reload, 15000)
    return () => clearInterval(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [online])

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  async function markAll() {
    await api.markAllNotificationsRead()
    await reload()
  }

  async function click(n: Notification) {
    if (!n.read) {
      await api.markNotificationRead(n.id)
      await reload()
    }
  }

  return (
    <div className="bell" ref={ref}>
      <button className="bell-btn" onClick={() => setOpen((o) => !o)} title="Notifications">
        🔔{unread > 0 && <span className="bell-badge">{unread > 9 ? '9+' : unread}</span>}
      </button>
      {open && (
        <div className="bell-panel">
          <div className="bell-head">
            <span>Notifications</span>
            {unread > 0 && <button className="link" onClick={markAll}>mark all read</button>}
          </div>
          {items.length === 0 ? (
            <div className="empty">Nothing yet.</div>
          ) : (
            <ul>
              {items.map((n) => (
                <li key={n.id} className={n.read ? 'read' : ''} onClick={() => click(n)}>
                  <span className="ntitle">{n.title}</span>
                  {n.body && <span className="nbody">{n.body}</span>}
                  <span className="ntime">{new Date(n.created_at).toLocaleString()}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
