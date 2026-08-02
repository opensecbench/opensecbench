import { useEffect, useState } from 'react'
import { api, Project, setActiveProject } from './api'
import { Home } from './Home'
import { Workbench } from './Workbench'
import { NotificationBell } from './NotificationBell'
import { ActivityMenu } from './ActivityMenu'
import { Settings } from './Settings'
import { Library } from './Library'
import { applyTheme, loadTheme } from './theme'

type Conn = 'connecting' | 'online' | 'offline'

export function App() {
  const [conn, setConn] = useState<Conn>('connecting')
  const [project, setProject] = useState<Project | null>(null)
  const [target, setTarget] = useState<{ surface?: string; thread?: string } | undefined>()
  const [view, setView] = useState<'home' | 'library' | 'settings'>('home')

  const openProject = (p: Project, t?: { surface?: string; thread?: string }) => {
    setTarget(t)
    setProject(p)
  }

  // Scope every API request to the open project (ADR-0049): its id rides as X-Project-Id so the backend
  // routes flat-route entities to that project's database. Cleared on return to home.
  useEffect(() => {
    setActiveProject(project?.id)
  }, [project])

  useEffect(() => {
    api
      .health()
      .then(() => setConn('online'))
      .catch(() => setConn('offline'))
  }, [])

  // Apply the saved theme once online; re-resolve on OS change while in "system" mode.
  useEffect(() => {
    if (conn !== 'online') return
    let theme = 'dark'
    let accent = ''
    let textSize = '1'
    void loadTheme().then((t) => {
      theme = t.theme
      accent = t.accent
      textSize = t.textSize
      applyTheme(theme, accent, textSize)
    })
    const mq = window.matchMedia('(prefers-color-scheme: light)')
    const onChange = () => theme === 'system' && applyTheme('system', accent, textSize)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [conn])

  // A project open takes over the whole window as the IDE Workbench (ADR-0015).
  if (project) {
    return <Workbench project={project} conn={conn} initial={target} onHome={() => { setProject(null); setView('home') }} />
  }

  return (
    <div className="app">
      <aside className="rail">
        <div className="logo" title="OpenSecBench" onClick={() => { setProject(null); setView('home') }} />
        <button className={`rail-btn ${view === 'home' ? 'active' : ''}`} title="Home" onClick={() => setView('home')}>
          ⌂
        </button>
        <button className={`rail-btn ${view === 'library' ? 'active' : ''}`} title="Library — playbooks, agents, checklists, report templates, secrets, extensions" onClick={() => setView('library')}>
          📚
        </button>
        <div className="rail-spacer" />
        <button className={`rail-btn ${view === 'settings' ? 'active' : ''}`} title="Settings" onClick={() => setView('settings')}>
          ⚙
        </button>
      </aside>

      <main className="main">
        <header className="topbar">
          <div className="crumb">{view === 'settings' ? 'Settings' : view === 'library' ? 'Library' : 'Home'}</div>
          <div className="spacer" />
          <ActivityMenu
            online={conn === 'online'}
            onOpen={async (kind, projectId) => {
              if (!projectId) return
              const p = (await api.listProjects().catch(() => []))?.find((x) => x.id === projectId)
              const surface = kind === 'plan' ? 'orchestrate' : kind === 'agent' ? 'observations' : 'tasks'
              if (p) openProject(p, { surface })
            }}
          />
          <NotificationBell online={conn === 'online'} />
          <span className={`conn conn-${conn}`}>
            <i /> {conn === 'online' ? 'control plane online' : conn === 'offline' ? 'control plane offline' : 'connecting…'}
          </span>
        </header>

        {view === 'settings' ? (
          <Settings online={conn === 'online'} />
        ) : view === 'library' ? (
          <Library online={conn === 'online'} />
        ) : (
          <Home online={conn === 'online'} onOpen={openProject} />
        )}
      </main>
    </div>
  )
}
