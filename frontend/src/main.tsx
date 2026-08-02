import React from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './App'
import { initAuth } from './api'
import './styles.css'

// Resolve the API token (ADR-0061) before the app makes its first control-plane call. In the desktop
// app this reads it over the Wails bridge; in the browser it falls back to VITE_OSB_TOKEN or empty.
void initAuth().then(() => {
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  )
})
