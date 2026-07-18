// Native OS integration via Wails bindings. These are available only in the desktop app; in a
// plain browser the helpers no-op so the UI degrades gracefully to manual path entry.

declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          SelectDirectory?: () => Promise<string>
          SelectFile?: () => Promise<string>
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
