// Native OS integration via Wails bindings. These are available only in the desktop app; in a
// plain browser the helpers no-op so the UI degrades gracefully to manual path entry.

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          SelectDirectory?: () => Promise<string>
          SelectFile?: () => Promise<string>
          OpenURL?: (url: string) => Promise<void>
          OpenProxyBrowser?: (port: number, spki: string) => Promise<void>
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
export async function pickDirectory(): Promise<string | null> {
  const fn = window.go?.main?.App?.SelectDirectory
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
  const fn = window.go?.main?.App?.OpenURL
  if (fn) {
    void fn(url)
    return
  }
  window.open(url, '_blank', 'noopener')
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
