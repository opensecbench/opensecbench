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
  disposition: string // 'allow' | 'deny' (ADR-0051)
  created_at: string
}

// The engagement record — the frame of an assessment (ADR-0051). Every field optional.
export interface EngagementContact {
  id?: string
  project_id?: string
  role: string // technical | authorizer | breakglass
  name: string
  email?: string
  phone?: string
  note?: string
}
export interface EngagementTestAccount {
  id?: string
  project_id?: string
  role: string
  username?: string
  secret_ref?: string
  note?: string
}
export interface Engagement {
  project_id: string
  base_path?: string // project root on disk; relative asset paths resolve against it (ADR-0051)
  kinds?: string[]
  objective?: string
  reference?: string
  environment?: string // production | staging | dev | mixed
  data_class?: string // open | private | restricted
  standard?: string
  compliance?: string
  severity_scale?: string
  authorized?: boolean
  authorizer?: string
  auth_ref?: string
  auth_from?: string
  auth_to?: string
  window_start?: string
  window_end?: string
  report_due?: string
  techniques?: Record<string, boolean>
  notes?: string
  contacts?: EngagementContact[]
  test_accounts?: EngagementTestAccount[]
}

// A scope rule the setup modal seeds at creation.
export interface ScopeSeed {
  kind: string
  value: string
  disposition: string
}

export interface CapabilityManifest {
  id: string
  version: string
  title: string
  description: string
  target_param?: string
  technique?: string // rules-of-engagement technique gated by the engagement (ADR-0051)
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

export interface KBEntry {
  id: string
  target_id: string
  kind: string
  scope: string
  title: string
  body?: string
  tags?: string
  sensitivity: string
  origin: string
  review_state: string
  source_ref?: string
  created_at: string
  updated_at: string
}

export interface MethodologyItem {
  id: string
  title: string
  objective?: string
  procedure?: string
  standards?: string[]
  suggested_capabilities?: string[]
}

export interface Methodology {
  id: string
  title: string
  tech: string
  version: string
  items: MethodologyItem[]
}

export interface CoverageView {
  packs: {
    id: string
    title: string
    tech: string
    items: { item: MethodologyItem; status: string; note?: string; evidence_count?: number }[]
  }[]
  summary: {
    total: number
    covered: number
    in_progress: number
    not_applicable: number
    not_started: number
    covered_pct: number
  }
}

export interface HeldItem {
  id: string
  phase: 'request' | 'response'
  method: string
  url: string
  request_headers: string
  request_body: string
  status?: number
  response_headers?: string
  response_body?: string
}
export interface InterceptState {
  requests: boolean
  responses: boolean
  held: HeldItem[]
}

export interface ActiveProvider {
  id?: string
  name: string
  type: string
  model: string
  is_local: boolean
  configured: boolean
}
export interface ProviderView {
  id: string
  name: string
  type: string
  model: string
  base_url: string
  has_key: boolean
  active: boolean
  created_at: string
}
export interface UsageByModel {
  provider: string
  model: string
  runs: number
  input_tokens: number
  output_tokens: number
}

export interface ProxyRule {
  id: string
  project_id: string
  enabled: boolean
  target: string
  match: string
  replace: string
  created_at: string
}

export interface GraphNode {
  id: string
  label: string
  kind: string
  group?: string
  meta?: string
}
export interface Graph {
  kind: string
  nodes: GraphNode[]
  edges: { from: string; to: string }[]
}

export interface MethodologySuggestion {
  methodology_id: string
  title: string
  reason: string
}

export interface ExtensionInfo {
  id: string
  name: string
  version: string
  publisher: string
  trusted: boolean
  digest: string
  capabilities?: string[]
  methodologies?: string[]
}

export interface IntegrationConfig {
  id: string
  project_id: string
  integration: string
  base_url: string
  project_key: string
  credential: string // vault secret name (never a value)
  updated_at: string
}
export interface ProjectIntegrations {
  configs: IntegrationConfig[] | null
  connectors: { name: string; pullable: boolean }[]
}
export interface PullResult {
  imported: number
  skipped: number
  total: number
}

export interface HubPackage {
  id: string
  name: string
  version: string
  publisher: string
  description?: string
  tags?: string[]
  publisher_key?: string
}

export interface PolicyProfile {
  name: string
  description: string
  allow_external_for_private: boolean
  agent_sees_private: boolean
}

export interface Notification {
  id: string
  kind: string
  title: string
  body?: string
  project_id?: string
  link?: string
  read: boolean
  created_at: string
}

export interface NotificationFeed {
  unread: number
  notifications: Notification[]
}

export interface ReportTemplate {
  id: string
  title: string
  kind: string
}

export interface Report {
  id: string
  project_id: string
  template_id: string
  format: string
  title: string
  artifact_id: string
  created_at: string
}

export interface ProxyStatus {
  running: boolean
  port?: number
  ca_spki_sha256?: string
  egress?: string // "" = local host; else the egress runner id (ADR-0026)
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
  egress?: string // "" = control-plane host; else the runner id that performed the send (ADR-0025)
  created_at: string
  sent_at?: string
  in_scope?: boolean // computed by the server against the project scope allowlist
}

export interface RunnerView {
  id: string
  name: string
  status: string
  online: boolean
  last_seen?: string
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
  project_id?: string
  origin: string
  review_state: string
  title: string
  detail?: string
  severity: string
  rule_id?: string
  location?: string
  attributes?: Record<string, string>
  // Resolved by the project-observations endpoint (ADR-0050): the source_repo asset the `location` path is
  // relative to, so the UI can offer click-to-file. Empty when the producing task had no source asset.
  asset_id?: string
}

// A source file's contents, from GET /v1/assets/{id}/source (ADR-0050).
export interface SourceFile {
  path: string
  content: string
  bytes: number
  lines: number
  truncated: boolean
}

// One entry in a source_repo directory listing, from GET /v1/assets/{id}/tree.
export interface TreeEntry {
  name: string
  path: string
  dir: boolean
  size?: number
}

export interface Investigation {
  id: string
  project_id: string
  observation_id: string
  title: string
  status: string
  thread_id?: string
  created_at: string
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

export interface ToolCall {
  id?: string
  tool: string
  args: Record<string, unknown>
}

export interface AgentProfile {
  id: string
  name: string
  description: string
  persona?: string
  tools?: string[]
  builtin?: boolean
}

export interface AgentTool {
  name: string
  description: string
}

export interface Schedule {
  id: string
  project_id: string
  playbook_id: string
  interval_seconds: number
  enabled: boolean
  last_run_at?: string
  next_run_at: string
  created_at: string
}

export interface ModelCatalogEntry {
  provider: string
  id: string
  name: string
  family: string
  context_window: number
  input_per_mtok: number
  output_per_mtok: number
  default_tags: string[]
}

export interface HomeData {
  approvals: { id: string; tool: string; thread_id: string; project_id?: string; project?: string; created_at: string }[]
  active: {
    tasks: { id: string; capability: string; status: string; project_id?: string; project?: string }[]
    threads: { id: string; title: string; status: string; agent_type: string; project_id?: string; project?: string }[]
  }
  projects: { id: string; name: string; status: string; findings: number; high: number; to_triage: number; adopted: number; covered_pct: number }[]
  usage: {
    month_input: number
    month_output: number
    all_input: number
    all_output: number
    top_models: { provider: string; model: string; runs: number; input_tokens: number; output_tokens: number }[]
    top_agents: { agent_type: string; runs: number; input_tokens: number; output_tokens: number }[]
  }
  schedules: {
    id: string
    project_id: string
    project?: string
    playbook_id: string
    playbook: string
    interval_seconds: number
    enabled: boolean
    next_run_at: string
    last_run_at?: string
  }[]
}

export interface ModelRef {
  provider_id: string
  model: string
}

export interface ModelRouting {
  default?: ModelRef
  tags?: Record<string, ModelRef>
}

export interface SettingField {
  key: string
  label: string
  type: string
  default?: string
  description?: string
  options?: { value: string; label: string }[]
}

export interface SettingSection {
  id: string
  title: string
  icon?: string
  order: number
  custom?: boolean
  source?: string // "" = core; "ext:<id>" = contributed by an extension
  fields?: SettingField[]
}

export interface ApprovalRule {
  tool: string
  profile?: string
  decision: 'auto' | 'approve'
}

export interface AgentPlaybookStep {
  key: string
  profile: string
  instruction: string
  depends_on: string[]
  gate?: boolean // a human-approval pause (ADR-0044)
}
export interface AgentPlaybook {
  id: string
  name: string
  description: string
  goal: string
  steps: AgentPlaybookStep[]
  builtin?: boolean
  source?: string
}

export interface PlanStep {
  id: string
  key: string
  profile: string
  instruction: string
  depends_on: string[]
  status: string
  result?: string
  error?: string
}

export interface Plan {
  id: string
  project_id: string
  playbook_id: string
  goal: string
  status: string
  steps?: PlanStep[]
  created_at: string
  updated_at: string
}

export interface Msg {
  id: string
  thread_id: string
  seq: number
  role: string
  content: string
  tool_calls?: ToolCall[]
  tool_call_id?: string
  tool_error?: boolean
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

// activeProjectId is the project the workbench is currently viewing. It is sent as the X-Project-Id
// header on every request so the backend routes flat-route entities (findings/{id}, threads/{id}, …) to
// the right per-project database (ADR-0049); project-nested routes carry the id in the path already.
let activeProjectId = ''

// setActiveProject updates the project scope applied to subsequent requests. Call it whenever the user
// selects (or clears) the active project.
export function setActiveProject(id: string | null | undefined): void {
  activeProjectId = id ?? ''
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body) headers['Content-Type'] = 'application/json'
  if (activeProjectId) headers['X-Project-Id'] = activeProjectId
  const res = await fetch(baseURL + path, {
    method,
    headers,
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
  // artifactContentURL is embedded in <img>/<a> where no header can be set, so it carries the active
  // project as a query param; the backend reads it as a fallback to X-Project-Id (ADR-0049).
  artifactContentURL: (id: string) =>
    `${baseURL}/v1/artifacts/${id}/content${activeProjectId ? `?project=${encodeURIComponent(activeProjectId)}` : ''}`,

  health: () => request<Record<string, string>>('GET', '/healthz'),

  // projects & templates
  listProjects: () => request<Project[]>('GET', '/v1/projects'),
  getProject: (id: string) => request<Project>('GET', '/v1/projects/' + id),
  createProject: (name: string) => request<Project>('POST', '/v1/projects', { name }),
  // Create a project with its engagement record + scope in one call (ADR-0051).
  createEngagement: (payload: { name: string; target_ids?: string[]; engagement?: Engagement; scope?: ScopeSeed[] }) =>
    request<Project>('POST', '/v1/projects', payload),
  getEngagement: (projectId: string) => request<Engagement>('GET', `/v1/projects/${projectId}/engagement`),
  setEngagement: (projectId: string, engagement: Engagement) =>
    request<Engagement>('PUT', `/v1/projects/${projectId}/engagement`, engagement),
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

  // source viewer (ADR-0050): read a source_repo asset's tree/files, path-confined server-side.
  assetSource: (assetId: string, path: string) =>
    request<SourceFile>('GET', `/v1/assets/${assetId}/source?path=${encodeURIComponent(path)}`),
  assetTree: (assetId: string, path = '') =>
    request<TreeEntry[]>('GET', `/v1/assets/${assetId}/tree${path ? `?path=${encodeURIComponent(path)}` : ''}`),
  listObservations: (projectId: string) =>
    request<Observation[]>('GET', `/v1/projects/${projectId}/observations`),

  // context
  listContext: (projectId: string) =>
    request<ContextItem[]>('GET', `/v1/projects/${projectId}/context`),
  ingestContext: async (projectId: string, name: string, type: string, file: File) => {
    const url =
      `${baseURL}/v1/projects/${projectId}/context?name=${encodeURIComponent(name)}&type=${encodeURIComponent(type)}`
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': file.type || 'application/octet-stream', ...(activeProjectId ? { 'X-Project-Id': activeProjectId } : {}) },
      body: file,
    })
    if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || res.statusText)
    return (await res.json()) as ContextItem
  },

  // scope
  listScope: (projectId: string) =>
    request<ScopeEntry[]>('GET', `/v1/projects/${projectId}/scope`),
  addScope: (projectId: string, kind: string, value: string, disposition = 'allow') =>
    request<ScopeEntry>('POST', `/v1/projects/${projectId}/scope`, { kind, value, disposition }),
  deleteScope: (id: string) => request<void>('DELETE', `/v1/scope/${id}`),

  // replay (HTTP exchanges)
  listExchanges: (
    projectId: string,
    filter?: { origin?: string; method?: string; status?: number; q?: string; limit?: number },
  ) => {
    const p = new URLSearchParams()
    if (filter?.origin) p.set('origin', filter.origin)
    if (filter?.method) p.set('method', filter.method)
    if (filter?.status) p.set('status', String(filter.status))
    if (filter?.q) p.set('q', filter.q)
    if (filter?.limit) p.set('limit', String(filter.limit))
    const qs = p.toString()
    return request<HTTPExchange[]>('GET', `/v1/projects/${projectId}/exchanges${qs ? `?${qs}` : ''}`)
  },
  createExchange: (
    projectId: string,
    req: { name?: string; method?: string; url: string; request_headers?: string; request_body?: string },
  ) => request<HTTPExchange>('POST', `/v1/projects/${projectId}/exchanges`, req),
  // Send an exchange; optionally from an enrolled remote runner's vantage (ADR-0025).
  sendExchange: (id: string, runnerId?: string) =>
    request<HTTPExchange>('POST', `/v1/exchanges/${id}/send`, runnerId ? { runner_id: runnerId } : {}),
  listRunners: () => request<RunnerView[]>('GET', '/v1/runners'),

  // Integrations (ADR-0027): per-project config + inbound pull.
  listSecrets: () => request<{ name: string }[]>('GET', '/v1/secrets'),
  getProjectIntegrations: (projectId: string) =>
    request<ProjectIntegrations>('GET', `/v1/projects/${projectId}/integrations`),
  setIntegrationConfig: (
    projectId: string,
    integration: string,
    cfg: { base_url: string; project_key: string; credential: string },
  ) => request<IntegrationConfig>('PUT', `/v1/projects/${projectId}/integrations/${integration}`, cfg),
  deleteIntegrationConfig: (projectId: string, integration: string) =>
    request<void>('DELETE', `/v1/projects/${projectId}/integrations/${integration}`),
  pullIntegration: (projectId: string, integration: string) =>
    request<PullResult>('POST', `/v1/projects/${projectId}/integrations/${integration}/pull`, {}),

  // Investigations (ADR-0028): disposition-routed follow-ups.
  listInvestigations: (projectId: string) =>
    request<Investigation[]>('GET', `/v1/projects/${projectId}/investigations`),
  runInvestigation: (id: string) =>
    request<{ investigation_id: string; thread: Thread }>('POST', `/v1/investigations/${id}/run`, {}),
  setInvestigationStatus: (id: string, status: string) =>
    request<void>('POST', `/v1/investigations/${id}/status`, { status }),

  // Live project event stream (SSE): captured exchanges, proxy status, and intercept queue changes,
  // so clients react instead of polling. EventSource auto-reconnects; callers resync with a fetch on
  // (re)connect. Returns a close fn.
  subscribeProjectEvents: (
    projectId: string,
    handlers: {
      exchange?: (ex: HTTPExchange) => void
      proxy?: (st: ProxyStatus) => void
      interceptState?: (st: InterceptState) => void
      held?: (h: HeldItem) => void
      resolved?: (id: string) => void
    },
  ): (() => void) => {
    const es = new EventSource(`${baseURL}/v1/projects/${projectId}/events`)
    const on = (type: string, fn: (payload: unknown) => void) =>
      es.addEventListener(type, (e) => {
        try {
          fn(JSON.parse((e as MessageEvent).data).payload)
        } catch {
          /* ignore malformed frame */
        }
      })
    if (handlers.exchange) on('exchange', (p) => handlers.exchange!(p as HTTPExchange))
    if (handlers.proxy) on('proxy', (p) => handlers.proxy!(p as ProxyStatus))
    if (handlers.interceptState) on('intercept', (p) => handlers.interceptState!(p as InterceptState))
    if (handlers.held) on('intercept.held', (p) => handlers.held!(p as HeldItem))
    if (handlers.resolved) on('intercept.resolved', (p) => handlers.resolved!((p as { id: string }).id))
    return () => es.close()
  },

  // intercept (hold → edit → forward/drop)
  getIntercept: (projectId: string) =>
    request<InterceptState>('GET', `/v1/projects/${projectId}/intercept`),
  setIntercept: (projectId: string, requests: boolean, responses: boolean) =>
    request<InterceptState>('PUT', `/v1/projects/${projectId}/intercept`, { requests, responses }),
  resolveIntercept: (
    projectId: string,
    holdId: string,
    body: {
      action: 'forward' | 'drop'
      method?: string
      url?: string
      request_headers?: string
      request_body?: string
      status?: number
      response_headers?: string
      response_body?: string
    },
  ) => request<{ status: string }>('POST', `/v1/projects/${projectId}/intercept/${holdId}`, body),

  // proxy match/replace rules
  listProxyRules: (projectId: string) =>
    request<ProxyRule[]>('GET', `/v1/projects/${projectId}/proxy-rules`),
  createProxyRule: (projectId: string, body: { target: string; match: string; replace: string; enabled?: boolean }) =>
    request<ProxyRule>('POST', `/v1/projects/${projectId}/proxy-rules`, body),
  setProxyRuleEnabled: (ruleId: string, enabled: boolean) =>
    request<{ enabled: boolean }>('PUT', `/v1/proxy-rules/${ruleId}`, { enabled }),
  deleteProxyRule: (ruleId: string) => request<void>('DELETE', `/v1/proxy-rules/${ruleId}`),
  saveExchangeEvidence: (id: string, note: string, itemId?: string) =>
    request<Observation>('POST', `/v1/exchanges/${id}/evidence`, { note, item_id: itemId ?? '' }),

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
  // Enqueues the task and returns it in the pending state (ADR-0022); poll getTask until terminal.
  runTask: (req: { capability_id: string; asset_id?: string; target_dir?: string; project_id?: string; params?: Record<string, unknown>; actor?: string }) =>
    request<Task>('POST', '/v1/tasks', req),
  getTask: (id: string) => request<Task>('GET', `/v1/tasks/${id}`),
  cancelTask: (id: string) => request<void>('POST', `/v1/tasks/${id}/cancel`),
  getTaskArtifacts: (taskId: string) => request<Artifact[]>('GET', `/v1/tasks/${taskId}/artifacts`),
  listPlaybooks: () => request<Playbook[]>('GET', '/v1/playbooks'),
  // Enqueues the playbook and returns the run in the running state (ADR-0022); poll getPlaybookRun.
  runPlaybook: (id: string, assetId: string) =>
    request<PlaybookRun>('POST', `/v1/playbooks/${id}/run`, { asset_id: assetId }),
  getPlaybookRun: (runId: string) => request<PlaybookRun>('GET', `/v1/playbook-runs/${runId}`),
  listTaskObservations: (taskId: string) =>
    request<Observation[]>('GET', `/v1/tasks/${taskId}/observations`),
  artifactContent: async (id: string) => {
    const res = await fetch(baseURL + '/v1/artifacts/' + id + '/content', {
      headers: activeProjectId ? { 'X-Project-Id': activeProjectId } : undefined,
    })
    if (!res.ok) throw new Error(res.statusText)
    return res.text()
  },

  // observations & findings
  reviewObservation: (id: string, state: string) =>
    request<void>('POST', `/v1/observations/${id}/review`, { state }),
  listFindings: () => request<Finding[]>('GET', '/v1/findings'),
  getFinding: (id: string) => request<Finding>('GET', '/v1/findings/' + id),
  setFindingStatus: (id: string, status: string) =>
    request<Finding>('POST', `/v1/findings/${id}/status`, { status }),
  createFinding: (req: { title: string; severity?: string; cwe?: string; observation_ids: string[] }) =>
    request<Finding>('POST', '/v1/findings', req),

  // proxy
  getProxy: (projectId: string) => request<ProxyStatus>('GET', `/v1/projects/${projectId}/proxy`),
  startProxy: (projectId: string, port = 0, runnerId?: string) =>
    request<ProxyStatus>('POST', `/v1/projects/${projectId}/proxy/start`, { port, runner_id: runnerId || undefined }),
  stopProxy: (projectId: string) =>
    request<ProxyStatus>('POST', `/v1/projects/${projectId}/proxy/stop`, {}),
  proxyCAURL: () => `${baseURL}/v1/proxy/ca`,

  // knowledge base
  listProjectKB: (projectId: string) => request<KBEntry[]>('GET', `/v1/projects/${projectId}/kb`),
  createKBEntry: (
    targetId: string,
    e: { kind: string; title: string; body?: string; tags?: string },
  ) => request<KBEntry>('POST', `/v1/targets/${targetId}/kb`, e),
  reviewKBEntry: (id: string, state: string) =>
    request<KBEntry>('POST', `/v1/kb/${id}/review`, { state }),

  // methodology
  listMethodologies: () => request<Methodology[]>('GET', '/v1/methodologies'),
  getMethodologyCoverage: (projectId: string) =>
    request<CoverageView>('GET', `/v1/projects/${projectId}/methodology`),
  adoptMethodology: (projectId: string, methodologyId: string) =>
    request<void>('POST', `/v1/projects/${projectId}/methodology/adopt`, { methodology_id: methodologyId }),
  unadoptMethodology: (projectId: string, methodologyId: string) =>
    request<void>('POST', `/v1/projects/${projectId}/methodology/unadopt`, { methodology_id: methodologyId }),
  setCoverage: (projectId: string, itemId: string, status: string, note = '') =>
    request<void>('POST', `/v1/projects/${projectId}/coverage`, { item_id: itemId, status, note }),

  methodologySuggestions: (projectId: string) =>
    request<MethodologySuggestion[]>('GET', `/v1/projects/${projectId}/methodology/suggestions`),
  projectGraph: (projectId: string, kind: string) =>
    request<Graph>('GET', `/v1/projects/${projectId}/graph?kind=${kind}`),

  // extensions, hub, policy
  listExtensions: () => request<ExtensionInfo[]>('GET', '/v1/extensions'),
  hubIndex: (url: string) => request<{ packages: HubPackage[] }>('GET', `/v1/hub/index?url=${encodeURIComponent(url)}`),
  hubInstall: (url: string, id: string, trust: boolean, allowUnsigned: boolean) =>
    request<ExtensionInfo>('POST', '/v1/hub/install', { url, id, trust, allow_unsigned: allowUnsigned }),
  listPolicyProfiles: () => request<PolicyProfile[]>('GET', '/v1/policy/profiles'),
  getActivePolicy: () => request<PolicyProfile>('GET', '/v1/policy/active'),
  setActivePolicy: (profile: string) => request<PolicyProfile>('PUT', '/v1/policy/active', { profile }),

  // reports
  listReportTemplates: () => request<ReportTemplate[]>('GET', '/v1/report-templates'),
  listReports: (projectId: string) => request<Report[]>('GET', `/v1/projects/${projectId}/reports`),
  generateReport: (projectId: string, template: string, format: string) =>
    request<Report>('POST', `/v1/projects/${projectId}/reports`, { template, format }),

  // notifications
  listNotifications: (limit = 50) =>
    request<NotificationFeed>('GET', `/v1/notifications?limit=${limit}`),
  markNotificationRead: (id: string) =>
    request<void>('POST', `/v1/notifications/${id}/read`),
  markAllNotificationsRead: () => request<{ marked: number }>('POST', '/v1/notifications/read-all', {}),

  // audit
  listAudit: (limit = 100) => request<AuditEvent[]>('GET', `/v1/audit?limit=${limit}`),

  // search
  search: (q: string) => request<SearchResult[]>('GET', '/v1/search?q=' + encodeURIComponent(q)),

  // analyst threads & approvals
  // Analyst provider / model
  getActiveProvider: () => request<ActiveProvider>('GET', '/v1/analyst/provider'),
  listProviders: () => request<ProviderView[]>('GET', '/v1/analyst/providers'),
  addProvider: (body: { name: string; type: string; model: string; base_url: string; api_key: string }) =>
    request<ProviderView>('POST', '/v1/analyst/providers', body),
  activateProvider: (id: string) => request<ActiveProvider>('POST', `/v1/analyst/providers/${id}/activate`, {}),
  testProvider: (id: string) =>
    request<{ ok: boolean; latency_ms?: number; sample?: string; error?: string }>('POST', `/v1/analyst/providers/${id}/test`, {}),
  deleteProvider: (id: string) => request<void>('DELETE', `/v1/analyst/providers/${id}`),
  getProjectUsage: (projectId: string) => request<UsageByModel[]>('GET', `/v1/projects/${projectId}/usage`),

  getModelCatalog: () =>
    request<{ models: ModelCatalogEntry[] }>('GET', '/v1/models/catalog').then((r) => r.models ?? []),
  getModelRouting: () => request<{ tags: string[]; routing: ModelRouting }>('GET', '/v1/models/routing'),
  setModelRouting: (routing: ModelRouting) => request<void>('PUT', '/v1/models/routing', { routing }),

  getSettings: () =>
    request<{ sections: SettingSection[]; values: Record<string, string> }>('GET', '/v1/settings'),
  putSettings: (values: Record<string, string>) => request<void>('PUT', '/v1/settings', { values }),

  getHome: () => request<HomeData>('GET', '/v1/home'),

  getApprovalPolicy: () =>
    request<{ sensitive_tools: string[]; rules: ApprovalRule[] }>('GET', '/v1/analyst/approval-policy'),
  setApprovalPolicy: (rules: ApprovalRule[]) =>
    request<{ rules: ApprovalRule[] }>('PUT', '/v1/analyst/approval-policy', { rules }),

  listAgentPlaybooks: () =>
    request<{ playbooks: AgentPlaybook[] }>('GET', '/v1/analyst/playbooks').then((r) => r.playbooks ?? []),
  createAgentPlaybook: (pb: {
    name: string
    description: string
    goal: string
    steps: AgentPlaybookStep[]
  }) => request<{ id: string }>('POST', '/v1/analyst/playbooks', pb),
  getAgentPlaybook: (id: string) => request<AgentPlaybook>('GET', '/v1/analyst/playbooks/' + id),
  updateAgentPlaybook: (id: string, pb: { name: string; description: string; goal: string; steps: AgentPlaybookStep[] }) =>
    request<AgentPlaybook>('PUT', '/v1/analyst/playbooks/' + id, pb),
  deleteAgentPlaybook: (id: string) => request<void>('DELETE', '/v1/analyst/playbooks/' + id),
  savePlanAsPlaybook: (planId: string, name: string, description: string) =>
    request<{ id: string }>('POST', `/v1/plans/${planId}/save-as-playbook`, { name, description }),
  startPlan: (projectId: string, playbookId: string) =>
    request<Plan>('POST', `/v1/projects/${projectId}/plans`, { playbook_id: playbookId }),
  listSchedules: (projectId: string) => request<Schedule[]>('GET', `/v1/projects/${projectId}/schedules`),
  createSchedule: (projectId: string, playbookId: string, intervalSeconds: number) =>
    request<Schedule>('POST', `/v1/projects/${projectId}/schedules`, { playbook_id: playbookId, interval_seconds: intervalSeconds }),
  setScheduleEnabled: (id: string, enabled: boolean) => request<void>('PUT', '/v1/schedules/' + id, { enabled }),
  deleteSchedule: (id: string) => request<void>('DELETE', '/v1/schedules/' + id),
  getPlan: (id: string) => request<Plan>('GET', '/v1/plans/' + id),
  listPlans: (projectId: string) => request<Plan[]>('GET', `/v1/projects/${projectId}/plans`),

  listThreads: () => request<Thread[]>('GET', '/v1/threads'),
  listAgentProfiles: () =>
    request<{ profiles: AgentProfile[] }>('GET', '/v1/analyst/profiles').then((r) => r.profiles ?? []),
  listAgentTools: () =>
    request<{ tools: AgentTool[] }>('GET', '/v1/analyst/tools').then((r) => r.tools ?? []),
  createAgentProfile: (p: { name: string; description: string; persona: string; tools: string[] }) =>
    request<{ id: string }>('POST', '/v1/analyst/profiles', p),
  deleteAgentProfile: (id: string) => request<void>('DELETE', '/v1/analyst/profiles/' + id),
  createThread: (projectId?: string, title?: string, agentType?: string) =>
    request<Thread>('POST', '/v1/threads', { project_id: projectId, title, agent_type: agentType }),
  getThread: (id: string) => request<{ thread: Thread; messages: Msg[] }>('GET', '/v1/threads/' + id),
  sendMessage: (id: string, message: string) =>
    request<SendResult>('POST', `/v1/threads/${id}/messages`, { message }),
  listApprovals: () => request<Approval[]>('GET', '/v1/approvals'),
  decideApproval: (id: string, decision: 'approve' | 'deny') =>
    request<SendResult>('POST', `/v1/approvals/${id}/decide`, { decision }),
}
