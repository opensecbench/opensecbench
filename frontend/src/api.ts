// Thin client for the OpenSecBench control-plane HTTP API (ADR-0001).
// The desktop app injects window.__OSB_API__; a browser falls back to the local daemon.

declare global {
  interface Window {
    __OSB_API__?: string
  }
}

const baseURL: string =
  window.__OSB_API__ ||
  (import.meta.env.VITE_OSB_API as string | undefined) ||
  'http://127.0.0.1:7373'

export interface Project {
  id: string
  name: string
  status: string
  organization_id?: string
  target_ids: string[] | null
  created_at: string
  updated_at: string
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(baseURL + path, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    let message = res.statusText
    try {
      const err = await res.json()
      if (err?.error) message = err.error
    } catch {
      // response had no JSON error body
    }
    throw new Error(message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  baseURL,
  health: () => request<Record<string, string>>('GET', '/healthz'),
  listProjects: () => request<Project[]>('GET', '/v1/projects'),
  createProject: (name: string) => request<Project>('POST', '/v1/projects', { name }),
  deleteProject: (id: string) => request<void>('DELETE', '/v1/projects/' + id),
}
