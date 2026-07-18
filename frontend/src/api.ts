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

// wsURL builds a WebSocket URL against the control plane for a given API path.
export function wsURL(path: string): string {
  return baseURL.replace(/^http/, 'ws') + path
}

// --- types (mirror pkg/model JSON) ---

export interface Project {
  id: string
  name: string
  status: string
  organization_id?: string
  target_ids: string[] | null
  created_at: string
  updated_at: string
}

export interface Template {
  id: string
  name: string
  description: string
  default_application: string
  suggested_capabilities: string[]
}

export interface Application {
  id: string
  project_id: string
  name: string
  created_at: string
  updated_at: string
}

export interface Asset {
  id: string
  application_id: string
  type: string
  location: string
  sensitivity: string
  created_at: string
  updated_at: string
}

export interface ContextItem {
  id: string
  project_id: string
  type: string
  name: string
  artifact_id: string
  created_at: string
}

export interface ScopeEntry {
  id: string
  project_id: string
  kind: string
  value: string
  created_at: string
}

export interface CapabilityManifest {
  id: string
  version: string
  title: string
  description: string
  target_param?: string
}

export interface AuditEvent {
  seq: number
  time: string
  actor: string
  action: string
  target?: string
  data?: unknown
  prev_hash: string
  hash: string
}

export interface Session {
  id: string
  project_id: string
  kind: string
  container: string
  image: string
  status: string
  actor: string
  transcript_artifact_id?: string
  created_at: string
  closed_at?: string
}

export interface HTTPExchange {
  id: string
  project_id: string
  name: string
  origin: string
  method: string
  url: string
  request_headers: string
  request_body: string
  status?: number
  response_headers: string
  response_body: string
  duration_ms?: number
  created_at: string
  sent_at?: string
}

export interface Artifact {
  id: string
  task_id?: string
  sha256: string
  media_type: string
  size: number
  kind: string
  name: string
}

export interface Task {
  id: string
  capability_id: string
  capability_version: string
  actor: string
  runner: string
  status: string
  exit_code?: number
  error?: string
  application_id?: string
  asset_id?: string
  created_at: string
}

export interface Observation {
  id: string
  task_id?: string
  artifact_id?: string
  origin: string
  review_state: string
  title: string
  detail?: string
  severity: string
  rule_id?: string
  location?: string
}

export interface Finding {
  id: string
  title: string
  severity: string
  status: string
  description?: string
  cwe?: string
  observation_ids: string[]
  created_at: string
}

export interface TaskOutcome {
  task: Task
  artifacts: Artifact[]
  observations: Observation[]
}

export interface SearchResult {
  kind: string
  id: string
  title: string
  detail?: string
}

export interface Playbook {
  id: string
  name: string
  description: string
  steps: { capability: string }[]
}

export interface PlaybookRun {
  id: string
  playbook_id: string
  status: string
  task_ids: string[]
}

export interface PlaybookRunResult {
  run: PlaybookRun
  outcomes: TaskOutcome[]
}

export interface Thread {
  id: string
  project_id?: string
  parent_thread_id?: string
  title: string
  status: string
  provider: string
  created_at: string
  updated_at: string
}

export interface Msg {
  id: string
  thread_id: string
  seq: number
  role: string
  content: string
  created_at: string
}

export interface Approval {
  id: string
  thread_id: string
  tool: string
  args: Record<string, unknown>
  status: string
  created_at: string
}

export interface SendResult {
  thread: Thread
  new_messages: Msg[]
  answer?: string
  pending_approval?: Approval
  input_tokens?: number
  output_tokens?: number
}

// --- request helper ---

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
      // no JSON error body
    }
    throw new Error(message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  baseURL,
  artifactContentURL: (id: string) => `${baseURL}/v1/artifacts/${id}/content`,

  health: () => request<Record<string, string>>('GET', '/healthz'),

  // projects & templates
  listProjects: () => request<Project[]>('GET', '/v1/projects'),
  getProject: (id: string) => request<Project>('GET', '/v1/projects/' + id),
  createProject: (name: string) => request<Project>('POST', '/v1/projects', { name }),
  deleteProject: (id: string) => request<void>('DELETE', '/v1/projects/' + id),
  listTemplates: () => request<Template[]>('GET', '/v1/templates'),
  createProjectFromTemplate: (template_id: string, name: string) =>
    request<{ project: Project; application?: Application; template: Template }>(
      'POST',
      '/v1/projects/from-template',
      { template_id, name },
    ),

  // applications & assets
  listApplications: (projectId: string) =>
    request<Application[]>('GET', `/v1/projects/${projectId}/applications`),
  createApplication: (projectId: string, name: string) =>
    request<Application>('POST', `/v1/projects/${projectId}/applications`, { name }),
  listAssets: (appId: string) => request<Asset[]>('GET', `/v1/applications/${appId}/assets`),
  createAsset: (appId: string, type: string, location: string, sensitivity: string) =>
    request<Asset>('POST', `/v1/applications/${appId}/assets`, { type, location, sensitivity }),

  // context
  listContext: (projectId: string) =>
    request<ContextItem[]>('GET', `/v1/projects/${projectId}/context`),
  ingestContext: async (projectId: string, name: string, type: string, file: File) => {
    const url =
      `${baseURL}/v1/projects/${projectId}/context?name=${encodeURIComponent(name)}&type=${encodeURIComponent(type)}`
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': file.type || 'application/octet-stream' },
      body: file,
    })
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || res.statusText)
    return (await res.json()) as ContextItem
  },

  // scope
  listScope: (projectId: string) =>
    request<ScopeEntry[]>('GET', `/v1/projects/${projectId}/scope`),
  addScope: (projectId: string, kind: string, value: string) =>
    request<ScopeEntry>('POST', `/v1/projects/${projectId}/scope`, { kind, value }),
  deleteScope: (id: string) => request<void>('DELETE', `/v1/scope/${id}`),

  // repeater (HTTP exchanges)
  listExchanges: (projectId: string) =>
    request<HTTPExchange[]>('GET', `/v1/projects/${projectId}/exchanges`),
  createExchange: (
    projectId: string,
    req: { name?: string; method?: string; url: string; request_headers?: string; request_body?: string },
  ) => request<HTTPExchange>('POST', `/v1/projects/${projectId}/exchanges`, req),
  sendExchange: (id: string) => request<HTTPExchange>('POST', `/v1/exchanges/${id}/send`, {}),
  saveExchangeEvidence: (id: string, note: string) =>
    request<Observation>('POST', `/v1/exchanges/${id}/evidence`, { note }),

  // terminal sessions
  listSessions: (projectId: string) =>
    request<Session[]>('GET', `/v1/projects/${projectId}/sessions`),
  openSession: (projectId: string, actor: string) =>
    request<Session>('POST', `/v1/projects/${projectId}/sessions`, { actor }),
  getSession: (id: string) => request<Session>('GET', `/v1/sessions/${id}`),
  closeSession: (id: string) => request<Session>('POST', `/v1/sessions/${id}/close`, {}),
  saveSessionEvidence: (id: string, note: string) =>
    request<Observation>('POST', `/v1/sessions/${id}/evidence`, { note }),

  // capabilities & tasks
  listCapabilities: () => request<CapabilityManifest[]>('GET', '/v1/capabilities'),
  listTasks: () => request<Task[]>('GET', '/v1/tasks'),
  runTask: (req: { capability_id: string; asset_id?: string; target_dir?: string; project_id?: string; params?: Record<string, unknown>; actor?: string }) =>
    request<TaskOutcome>('POST', '/v1/tasks', req),
  cancelTask: (id: string) => request<void>('POST', `/v1/tasks/${id}/cancel`),
  getTaskArtifacts: (taskId: string) => request<Artifact[]>('GET', `/v1/tasks/${taskId}/artifacts`),
  listPlaybooks: () => request<Playbook[]>('GET', '/v1/playbooks'),
  runPlaybook: (id: string, assetId: string) =>
    request<PlaybookRunResult>('POST', `/v1/playbooks/${id}/run`, { asset_id: assetId }),
  listTaskObservations: (taskId: string) =>
    request<Observation[]>('GET', `/v1/tasks/${taskId}/observations`),
  artifactContent: async (id: string) => {
    const res = await fetch(baseURL + '/v1/artifacts/' + id + '/content')
    if (!res.ok) throw new Error(res.statusText)
    return res.text()
  },

  // observations & findings
  reviewObservation: (id: string, state: string) =>
    request<void>('POST', `/v1/observations/${id}/review`, { state }),
  listFindings: () => request<Finding[]>('GET', '/v1/findings'),
  getFinding: (id: string) => request<Finding>('GET', '/v1/findings/' + id),
  createFinding: (req: { title: string; severity?: string; cwe?: string; observation_ids: string[] }) =>
    request<Finding>('POST', '/v1/findings', req),

  // audit
  listAudit: (limit = 100) => request<AuditEvent[]>('GET', `/v1/audit?limit=${limit}`),

  // search
  search: (q: string) => request<SearchResult[]>('GET', '/v1/search?q=' + encodeURIComponent(q)),

  // analyst threads & approvals
  listThreads: () => request<Thread[]>('GET', '/v1/threads'),
  createThread: (projectId?: string, title?: string) =>
    request<Thread>('POST', '/v1/threads', { project_id: projectId, title }),
  getThread: (id: string) => request<{ thread: Thread; messages: Msg[] }>('GET', '/v1/threads/' + id),
  sendMessage: (id: string, message: string) =>
    request<SendResult>('POST', `/v1/threads/${id}/messages`, { message }),
  listApprovals: () => request<Approval[]>('GET', '/v1/approvals'),
  decideApproval: (id: string, decision: 'approve' | 'deny') =>
    request<SendResult>('POST', `/v1/approvals/${id}/decide`, { decision }),
}
