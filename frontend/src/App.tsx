import { useEffect, useState } from 'react'
import { api, Project } from './api'
import { Home } from './Home'
import { Workbench } from './Workbench'
import { NotificationBell } from './NotificationBell'
import { ExtensionsView } from './ExtensionsView'

type Conn = 'connecting' | 'online' | 'offline'

export function App() {
  const [conn, setConn] = useState<Conn>('connecting')
  const [project, setProject] = useState<Project | null>(null)
  const [view, setView] = useState<'home' | 'ext'>('home')

  useEffect(() => {
    api
      .health()
      .then(() => setConn('online'))
      .catch(() => setConn('offline'))
  }, [])

  // A project open takes over the whole window as the IDE Workbench (ADR-0015).
  if (project) {
    return <Workbench project={project} conn={conn} onHome={() => { setProject(null); setView('home') }} />
  }

  return (
    <div className="app">
      <aside className="rail">
        <div className="logo" title="OpenSecBench" onClick={() => { setProject(null); setView('home') }} />
        <button className={`rail-btn ${view === 'home' ? 'active' : ''}`} title="Home" onClick={() => setView('home')}>
          ⌂
        </button>
        <button className={`rail-btn ${view === 'ext' ? 'active' : ''}`} title="Extensions & Governance" onClick={() => setView('ext')}>
          ⧉
        </button>
      </aside>

      <main className="main">
        <header className="topbar">
          <div className="crumb">Home</div>
          <div className="spacer" />
          <NotificationBell online={conn === 'online'} />
          <span className={`conn conn-${conn}`}>
            <i /> {conn === 'online' ? 'control plane online' : conn === 'offline' ? 'control plane offline' : 'connecting…'}
          </span>
          <code className="apiurl">{api.baseURL}</code>
        </header>

        {view === 'ext' ? (
          <ExtensionsView online={conn === 'online'} />
        ) : (
          <Home online={conn === 'online'} onOpen={setProject} />
        )}
      </main>
    </div>
  )
}
