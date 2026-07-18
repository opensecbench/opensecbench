// Native OS integration via Wails bindings. These are available only in the desktop app; in a
// plain browser the helpers no-op so the UI degrades gracefully to manual path entry.

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          SelectDirectory?: () => Promise<string>
          SelectFile?: () => Promise<string>
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

/** pickFile opens a native file dialog, or returns null in the browser / on cancel. */
export async function pickFile(): Promise<string | null> {
  const fn = window.go?.main?.App?.SelectFile
  if (!fn) return null
  const path = await fn()
  return path || null
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
