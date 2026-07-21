import { api } from './api'

// Theme application (ADR-0021). The palette lives in CSS custom properties; we stamp data-theme on the
// root and (optionally) override the accent token. "system" resolves to the OS preference.
export function resolveTheme(theme: string): 'dark' | 'light' {
  if (theme === 'light' || theme === 'dark') return theme
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

// Text size is applied as a root-level `zoom` so it scales the whole UI (text and spacing) even though
// most rules use fixed px — a font-size change alone would not cascade to those. "1" is the default.
function resolveScale(scale: string): number {
  const n = parseFloat(scale)
  return Number.isFinite(n) && n > 0 ? Math.min(Math.max(n, 0.7), 2) : 1
}

export function applyTheme(theme: string, accent: string, textSize = '1') {
  document.documentElement.setAttribute('data-theme', resolveTheme(theme))
  if (accent) document.documentElement.style.setProperty('--acc', accent)
  else document.documentElement.style.removeProperty('--acc')
  // `zoom` is non-standard but well supported in the WebKit webview Wails uses.
  ;(document.documentElement.style as CSSStyleDeclaration & { zoom?: string }).zoom = String(resolveScale(textSize))
}

export async function loadTheme(): Promise<{ theme: string; accent: string; textSize: string }> {
  try {
    const s = await api.getSettings()
    return {
      theme: s.values['appearance.theme'] || 'dark',
      accent: s.values['appearance.accent'] || '',
      textSize: s.values['appearance.text_size'] || '1',
    }
  } catch {
    return { theme: 'dark', accent: '', textSize: '1' }
  }
}
