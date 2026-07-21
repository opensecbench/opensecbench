import { useState } from 'react'
import { PlaybookLibrary } from './PlaybookLibrary'
import { CustomAgents } from './CustomAgents'

// Library is the global "build & reuse" surface (IA declutter): reusable definitions you build once and
// use across projects — agent playbooks, custom agents, and (added incrementally) the methodology catalog
// and integration connectors. It sits at the app level next to Home/Settings, off the per-project menu.
const SECTIONS: { id: string; title: string; icon: string; group: string }[] = [
  { id: 'playbooks', title: 'Playbooks', icon: '🧩', group: 'Build & reuse' },
  { id: 'agents', title: 'Custom agents', icon: '🤖', group: 'Build & reuse' },
]

export function Library({ online }: { online: boolean }) {
  const [active, setActive] = useState('playbooks')
  const groups = [...new Set(SECTIONS.map((s) => s.group))]

  return (
    <div className="settings">
      <nav className="settings-nav">
        {groups.map((g) => (
          <div key={g} className="settings-navgrp">
            <div className="settings-navgrp-h">{g}</div>
            {SECTIONS.filter((s) => s.group === g).map((s) => (
              <button key={s.id} className={active === s.id ? 'on' : ''} onClick={() => setActive(s.id)}>
                <span className="settings-ico">{s.icon}</span> {s.title}
              </button>
            ))}
          </div>
        ))}
      </nav>
      <div className="settings-body">
        {active === 'playbooks' && <PlaybookLibrary online={online} />}
        {active === 'agents' && <CustomAgents online={online} />}
      </div>
    </div>
  )
}
