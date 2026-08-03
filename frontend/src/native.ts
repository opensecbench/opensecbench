// Native OS integration via Wails bindings. These are available only in the desktop app; in a
// plain browser the helpers no-op so the UI degrades gracefully to manual path entry.

import { api, authHeaders } from './api'

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          SelectDirectory?: (defaultDir?: string) => Promise<string>
          SelectFile?: () => Promise<string>
          WorkingDir?: () => Promise<string>
          OpenURL?: (url: string) => Promise<void>
          OpenProxyBrowser?: (port: number, spki: string) => Promise<void>
          // APIToken hands the webview its control-plane bearer token at boot (ADR-0061); APIBase hands
          // it the control-plane URL, so the window can attach to an external daemon (main.go OSB_API mode).
          APIToken?: () => Promise<string>
          APIBase?: () => Promise<string>
          // SaveArtifact downloads an API resource to a user-chosen file, Go-side, so the token
          // stays in a header and never touches a URL/system browser (ADR-0061).
          SaveArtifact?: (path: string, suggestedName: string) => Promise<string>
        }
      }
    }
  }
}

/** hasNativePickers reports whether native file/directory dialogs are available (desktop app). */
export function hasNativePickers(): boolean {
  return !!window.go?.main?.App?.SelectDirectory
}

/** pickDirectory opens a native directory dialog, or returns null in the browser / on cancel. */
export async function pickDirectory(defaultDir?: string): Promise<string | null> {
  const fn = window.go?.main?.App?.SelectDirectory
  if (!fn) return null
  const path = await fn(defaultDir ?? '')
  return path || null
}

/** workingDir returns the desktop app's current working directory, or null in the browser / on error. */
export async function workingDir(): Promise<string | null> {
  const fn = window.go?.main?.App?.WorkingDir
  if (!fn) return null
  const path = await fn()
  return path || null
}


/**
 * openExternal opens a URL for the user to view. In the desktop app it hands off to the system browser
 * via the Wails binding (the WebKit webview ignores target="_blank"/window.open, so those silently do
 * nothing). In a plain browser it falls back to a new tab.
 */
export function openExternal(url: string): void {
  // For genuinely external links only (docs, third-party sites). API resources never go through here
  // — they are fetched with an Authorization header or downloaded via downloadArtifact (ADR-0061).
  const fn = window.go?.main?.App?.OpenURL
  if (fn) {
    void fn(url)
    return
  }
  window.open(url, '_blank', 'noopener')
}

/**
 * downloadArtifact saves an API resource to a file without the token ever entering a URL (ADR-0061).
 * In the desktop app, Go fetches it with an Authorization header and writes it via a native save
 * dialog. In a plain browser it falls back to an authenticated fetch → blob → anchor download.
 * `path` must be an API-relative path (e.g. "/v1/proxy/ca").
 */
export async function downloadArtifact(path: string, suggestedName: string): Promise<void> {
  const fn = window.go?.main?.App?.SaveArtifact
  if (fn) {
    await fn(path, suggestedName)
    return
  }
  const res = await fetch(api.baseURL + path, { headers: { ...authHeaders() } })
  if (!res.ok) throw new Error(res.statusText)
  const url = URL.createObjectURL(await res.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = suggestedName
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/** hasNativeBrowserLaunch reports whether the desktop app can launch a preconfigured browser. */
export function hasNativeBrowserLaunch(): boolean {
  return !!window.go?.main?.App?.OpenProxyBrowser
}

/** openProxyBrowser launches a throwaway browser through the proxy (desktop app only). */
export async function openProxyBrowser(port: number, spki: string): Promise<void> {
  const fn = window.go?.main?.App?.OpenProxyBrowser
  if (!fn) return
  await fn(port, spki)
}
