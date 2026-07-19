import { api } from './api'

// Theme application (ADR-0021). The palette lives in CSS custom properties; we stamp data-theme on the
// root and (optionally) override the accent token. "system" resolves to the OS preference.
export function resolveTheme(theme: string): 'dark' | 'light' {
  if (theme === 'light' || theme === 'dark') return theme
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

export function applyTheme(theme: string, accent: string) {
  document.documentElement.setAttribute('data-theme', resolveTheme(theme))
  if (accent) document.documentElement.style.setProperty('--acc', accent)
  else document.documentElement.style.removeProperty('--acc')
}

export async function loadTheme(): Promise<{ theme: string; accent: string }> {
  try {
    const s = await api.getSettings()
    return { theme: s.values['appearance.theme'] || 'dark', accent: s.values['appearance.accent'] || '' }
  } catch {
    return { theme: 'dark', accent: '' }
  }
}
