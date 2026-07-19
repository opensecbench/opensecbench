// Package api exposes the control-plane HTTP API that every client (desktop, CLI, future web)
// talks to. Domain logic lives in the control-plane packages, never in a client (ADR-0001).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/opensecbench/opensecbench/pkg/analyst"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/dlp"
	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/extension"
	"github.com/opensecbench/opensecbench/pkg/hub"
	"github.com/opensecbench/opensecbench/pkg/integration"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/playbook"
	"github.com/opensecbench/opensecbench/pkg/policy"
	"github.com/opensecbench/opensecbench/pkg/proxy"
	"github.com/opensecbench/opensecbench/pkg/replay"
	"github.com/opensecbench/opensecbench/pkg/report"
	"github.com/opensecbench/opensecbench/pkg/scope"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/session"
	"github.com/opensecbench/opensecbench/pkg/settings"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
	"github.com/opensecbench/opensecbench/pkg/template"
	"github.com/opensecbench/opensecbench/pkg/version"
)

// Deps are the control-plane services the API exposes.
type Deps struct {
	Store        *store.DB
	Engine       *task.Engine
	CAS          *cas.Store
	Provider     llm.Provider
	SessionMgr   *session.Manager
	ProxyCA      *proxy.CA
	Vault        *secret.Vault
	Methods      *methodology.Registry // built-ins + loaded extensions; nil = built-ins only
	Reports      *report.Registry      // built-ins + loaded extensions; nil = built-ins only
	Extensions   []extension.Loaded    // loaded extension packages (for listing)
	TrustStore   *extension.TrustStore // publisher trust store (for hub install / trust)
	ExtDir       string                // where installed packages are extracted
	WorkspaceDir string                // root for per-project agent workspaces (ADR-0020)
}

// Server routes control-plane HTTP requests against the control-plane services.
type Server struct {
	mux            *http.ServeMux
	store          *store.DB
	engine         *task.Engine
	cas            *cas.Store
	providerMu     sync.RWMutex
	provider       llm.Provider
	activeProvider providerInfo
	replay         *replay.Client
	events         *events.Hub
	sessMgr        *session.Manager
	proxyCA        *proxy.CA
	reports        *report.Registry
	methods        *methodology.Registry
	vault          *secret.Vault
	integr         *integration.Registry
	trust          *extension.TrustStore
	extDir         string
	workspaceDir   string
	hubCli         *hub.Client
	sched          *analyst.Scheduler
	schedCancel    context.CancelFunc

	extMu sync.Mutex
	exts  []extension.Loaded

	sessMu   sync.Mutex
	sessions map[string]*liveSession

	proxyMu sync.Mutex
	proxies map[string]*liveProxy

	ruleMu       sync.Mutex
	matchReplace map[string]*ruleEngine // per-project match/replace engines (ADR-0016 Step 4)
}

// New builds the API server with its routes registered.
func New(deps Deps) *Server {
	s := &Server{
		mux:          http.NewServeMux(),
		store:        deps.Store,
		engine:       deps.Engine,
		cas:          deps.CAS,
		provider:     deps.Provider,
		replay:       replay.New(0),
		events:       events.NewHub(),
		sessMgr:      deps.SessionMgr,
		proxyCA:      deps.ProxyCA,
		reports:      deps.Reports,
		methods:      deps.Methods,
		vault:        deps.Vault,
		integr:       integration.BuiltIns(),
		trust:        deps.TrustStore,
		extDir:       deps.ExtDir,
		workspaceDir: deps.WorkspaceDir,
		hubCli:       hub.NewClient(0),
		exts:         deps.Extensions,
		sessions:     make(map[string]*liveSession),
		proxies:      make(map[string]*liveProxy),
		matchReplace: make(map[string]*ruleEngine),
	}
	if s.methods == nil {
		s.methods = methodology.BuiltIns()
	}
	if s.reports == nil {
		s.reports = report.BuiltIns()
	}
	s.activeProvider = envProviderInfo(s.provider)
	s.routes()
	s.loadActiveProvider() // a persisted active provider overrides the env default
	s.startScheduler()
	return s
}

// startScheduler runs the playbook scheduler in the background (ADR-0019 step 4). A due schedule fires
// StartPlan; it is stopped on Close.
func (s *Server) startScheduler() {
	if s.store == nil {
		return
	}
	s.sched = analyst.NewScheduler(s.store, func(ctx context.Context, projectID, playbookID string) error {
		_, err := s.analystService().StartPlan(ctx, projectID, playbookID)
		return err
	}, func(action, detail string) {
		s.record(context.Background(), "scheduler", "analyst."+action, detail, nil)
	})
	ctx, cancel := context.WithCancel(context.Background())
	s.schedCancel = cancel
	go s.sched.Run(ctx)
}

// Handler returns the root HTTP handler, wrapped with CORS so a browser-based or Wails frontend
// on another loopback origin can call the API. The API binds to loopback only, so reflecting the
// request origin is safe for a local single-user workbench.
func (s *Server) Handler() http.Handler { return withCORS(s.mux) }

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)

	s.mux.HandleFunc("GET /v1/organizations", s.listOrganizations)
	s.mux.HandleFunc("POST /v1/organizations", s.createOrganization)

	s.mux.HandleFunc("GET /v1/targets", s.listTargets)
	s.mux.HandleFunc("POST /v1/targets", s.createTarget)

	s.mux.HandleFunc("GET /v1/search", s.search)
	s.mux.HandleFunc("GET /v1/audit", s.listAudit)
	s.mux.HandleFunc("GET /v1/audit/verify", s.verifyAudit)
	s.mux.HandleFunc("GET /v1/notifications", s.listNotifications)
	s.mux.HandleFunc("POST /v1/notifications/{id}/read", s.markNotificationRead)
	s.mux.HandleFunc("POST /v1/notifications/read-all", s.markAllNotificationsRead)
	s.mux.HandleFunc("GET /v1/projects/{id}/kb", s.listProjectKB)
	s.mux.HandleFunc("GET /v1/targets/{id}/kb", s.listTargetKB)
	s.mux.HandleFunc("POST /v1/targets/{id}/kb", s.createKBEntry)
	s.mux.HandleFunc("GET /v1/kb/{id}", s.getKBEntry)
	s.mux.HandleFunc("PUT /v1/kb/{id}", s.updateKBEntry)
	s.mux.HandleFunc("POST /v1/kb/{id}/review", s.reviewKBEntry)
	s.mux.HandleFunc("GET /v1/secrets", s.listSecrets)
	s.mux.HandleFunc("POST /v1/secrets", s.setSecret)
	s.mux.HandleFunc("DELETE /v1/secrets/{name}", s.deleteSecret)
	s.mux.HandleFunc("GET /v1/canaries", s.listCanaries)
	s.mux.HandleFunc("POST /v1/canaries", s.createCanary)
	s.mux.HandleFunc("DELETE /v1/canaries/{id}", s.deleteCanary)
	s.mux.HandleFunc("GET /v1/dlp-events", s.listDLPEvents)
	s.mux.HandleFunc("GET /v1/policy/profiles", s.listPolicyProfiles)
	s.mux.HandleFunc("GET /v1/policy/active", s.getActivePolicy)
	s.mux.HandleFunc("PUT /v1/policy/active", s.setActivePolicy)
	s.mux.HandleFunc("GET /v1/extensions", s.listExtensions)
	s.mux.HandleFunc("POST /v1/extensions/trust", s.trustPublisher)
	s.mux.HandleFunc("GET /v1/hub/index", s.hubIndex)
	s.mux.HandleFunc("POST /v1/hub/install", s.hubInstall)
	s.mux.HandleFunc("GET /v1/analyst/profiles", s.listAgentProfiles)
	s.mux.HandleFunc("POST /v1/analyst/profiles", s.createSavedProfile)
	s.mux.HandleFunc("DELETE /v1/analyst/profiles/{id}", s.deleteSavedProfile)
	s.mux.HandleFunc("GET /v1/analyst/tools", s.listAgentTools)
	s.mux.HandleFunc("GET /v1/settings", s.getSettings)
	s.mux.HandleFunc("PUT /v1/settings", s.putSettings)
	s.mux.HandleFunc("GET /v1/analyst/approval-policy", s.getApprovalPolicy)
	s.mux.HandleFunc("PUT /v1/analyst/approval-policy", s.setApprovalPolicy)
	s.mux.HandleFunc("GET /v1/analyst/playbooks", s.listAgentPlaybooks)
	s.mux.HandleFunc("POST /v1/analyst/playbooks", s.createSavedPlaybook)
	s.mux.HandleFunc("DELETE /v1/analyst/playbooks/{id}", s.deleteSavedPlaybook)
	s.mux.HandleFunc("POST /v1/projects/{id}/plans", s.startPlan)
	s.mux.HandleFunc("GET /v1/projects/{id}/plans", s.listPlans)
	s.mux.HandleFunc("GET /v1/plans/{id}", s.getPlan)
	s.mux.HandleFunc("POST /v1/plans/{id}/save-as-playbook", s.savePlanAsPlaybook)
	s.mux.HandleFunc("POST /v1/projects/{id}/schedules", s.createSchedule)
	s.mux.HandleFunc("GET /v1/projects/{id}/schedules", s.listSchedules)
	s.mux.HandleFunc("PUT /v1/schedules/{id}", s.updateSchedule)
	s.mux.HandleFunc("DELETE /v1/schedules/{id}", s.deleteSchedule)
	s.mux.HandleFunc("GET /v1/analyst/provider", s.getActiveProvider)
	s.mux.HandleFunc("GET /v1/analyst/providers", s.listProviders)
	s.mux.HandleFunc("POST /v1/analyst/providers", s.addProvider)
	s.mux.HandleFunc("POST /v1/analyst/providers/{id}/activate", s.activateProvider)
	s.mux.HandleFunc("POST /v1/analyst/providers/{id}/test", s.testProvider)
	s.mux.HandleFunc("DELETE /v1/analyst/providers/{id}", s.deleteProvider)
	s.mux.HandleFunc("GET /v1/projects/{id}/usage", s.projectUsage)
	s.mux.HandleFunc("GET /v1/methodologies", s.listMethodologies)
	s.mux.HandleFunc("GET /v1/projects/{id}/methodology", s.getMethodologyCoverage)
	s.mux.HandleFunc("GET /v1/projects/{id}/methodology/suggestions", s.methodologySuggestions)
	s.mux.HandleFunc("GET /v1/projects/{id}/graph", s.projectGraph)
	s.mux.HandleFunc("POST /v1/projects/{id}/methodology/adopt", s.adoptMethodology)
	s.mux.HandleFunc("POST /v1/projects/{id}/methodology/unadopt", s.unadoptMethodology)
	s.mux.HandleFunc("POST /v1/projects/{id}/coverage", s.setCoverage)
	s.mux.HandleFunc("GET /v1/report-templates", s.listReportTemplates)
	s.mux.HandleFunc("GET /v1/projects/{id}/reports", s.listReports)
	s.mux.HandleFunc("POST /v1/projects/{id}/reports", s.generateReport)

	s.mux.HandleFunc("GET /v1/templates", s.listTemplates)
	s.mux.HandleFunc("POST /v1/projects/from-template", s.createProjectFromTemplate)

	s.mux.HandleFunc("GET /v1/projects", s.listProjects)
	s.mux.HandleFunc("POST /v1/projects", s.createProject)
	s.mux.HandleFunc("GET /v1/projects/{id}", s.getProject)
	s.mux.HandleFunc("DELETE /v1/projects/{id}", s.deleteProject)
	s.mux.HandleFunc("POST /v1/projects/{id}/export", s.exportProject)
	s.mux.HandleFunc("POST /v1/import", s.importBundle)

	s.mux.HandleFunc("GET /v1/projects/{id}/applications", s.listApplications)
	s.mux.HandleFunc("POST /v1/projects/{id}/applications", s.createApplication)
	s.mux.HandleFunc("GET /v1/applications/{id}", s.getApplication)
	s.mux.HandleFunc("GET /v1/projects/{id}/context", s.listContext)
	s.mux.HandleFunc("POST /v1/projects/{id}/context", s.ingestContext)
	s.mux.HandleFunc("GET /v1/projects/{id}/scope", s.listScope)
	s.mux.HandleFunc("POST /v1/projects/{id}/scope", s.addScope)
	s.mux.HandleFunc("DELETE /v1/scope/{id}", s.deleteScope)
	s.mux.HandleFunc("GET /v1/projects/{id}/events", s.projectEvents)
	s.mux.HandleFunc("GET /v1/projects/{id}/intercept", s.getIntercept)
	s.mux.HandleFunc("PUT /v1/projects/{id}/intercept", s.setIntercept)
	s.mux.HandleFunc("POST /v1/projects/{id}/intercept/{holdId}", s.resolveIntercept)
	s.mux.HandleFunc("GET /v1/projects/{id}/proxy-rules", s.listProxyRules)
	s.mux.HandleFunc("POST /v1/projects/{id}/proxy-rules", s.createProxyRule)
	s.mux.HandleFunc("PUT /v1/proxy-rules/{ruleId}", s.updateProxyRule)
	s.mux.HandleFunc("DELETE /v1/proxy-rules/{ruleId}", s.deleteProxyRule)
	s.mux.HandleFunc("GET /v1/projects/{id}/exchanges", s.listExchanges)
	s.mux.HandleFunc("POST /v1/projects/{id}/exchanges", s.createExchange)
	s.mux.HandleFunc("GET /v1/exchanges/{id}", s.getExchange)
	s.mux.HandleFunc("POST /v1/exchanges/{id}/send", s.sendExchange)
	s.mux.HandleFunc("POST /v1/exchanges/{id}/evidence", s.exchangeEvidence)
	s.mux.HandleFunc("GET /v1/projects/{id}/sessions", s.listSessions)
	s.mux.HandleFunc("POST /v1/projects/{id}/sessions", s.createSession)
	s.mux.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	s.mux.HandleFunc("GET /v1/sessions/{id}/ws", s.sessionWS)
	s.mux.HandleFunc("POST /v1/sessions/{id}/close", s.closeSession)
	s.mux.HandleFunc("POST /v1/sessions/{id}/evidence", s.sessionEvidence)
	s.mux.HandleFunc("GET /v1/proxy/ca", s.proxyCACert)
	s.mux.HandleFunc("GET /v1/projects/{id}/proxy", s.getProxy)
	s.mux.HandleFunc("POST /v1/projects/{id}/proxy/start", s.startProxy)
	s.mux.HandleFunc("POST /v1/projects/{id}/proxy/stop", s.stopProxy)
	s.mux.HandleFunc("GET /v1/applications/{id}/assets", s.listAssets)
	s.mux.HandleFunc("POST /v1/applications/{id}/assets", s.createAsset)
	s.mux.HandleFunc("GET /v1/assets/{id}", s.getAsset)

	s.mux.HandleFunc("GET /v1/capabilities", s.listCapabilities)
	s.mux.HandleFunc("GET /v1/tasks", s.listTasks)
	s.mux.HandleFunc("POST /v1/tasks", s.runTask)
	s.mux.HandleFunc("GET /v1/tasks/{id}", s.getTask)
	s.mux.HandleFunc("POST /v1/tasks/{id}/cancel", s.cancelTask)
	s.mux.HandleFunc("GET /v1/tasks/{id}/artifacts", s.getTaskArtifacts)
	s.mux.HandleFunc("GET /v1/artifacts/{id}/content", s.getArtifactContent)

	s.mux.HandleFunc("GET /v1/playbooks", s.listPlaybooks)
	s.mux.HandleFunc("POST /v1/playbooks/{id}/run", s.runPlaybook)
	s.mux.HandleFunc("GET /v1/playbook-runs", s.listPlaybookRuns)
	s.mux.HandleFunc("GET /v1/playbook-runs/{id}", s.getPlaybookRun)

	s.mux.HandleFunc("GET /v1/tasks/{id}/observations", s.getTaskObservations)
	s.mux.HandleFunc("POST /v1/observations/{id}/review", s.reviewObservation)

	s.mux.HandleFunc("GET /v1/findings", s.listFindings)
	s.mux.HandleFunc("POST /v1/findings", s.createFinding)
	s.mux.HandleFunc("GET /v1/findings/{id}", s.getFinding)
	s.mux.HandleFunc("GET /v1/integrations", s.listIntegrations)
	s.mux.HandleFunc("GET /v1/findings/{id}/links", s.listFindingLinks)
	s.mux.HandleFunc("POST /v1/findings/{id}/push", s.pushFinding)

	s.mux.HandleFunc("POST /v1/analyst/ask", s.analystAsk)

	s.mux.HandleFunc("GET /v1/threads", s.listThreads)
	s.mux.HandleFunc("POST /v1/threads", s.createThread)
	s.mux.HandleFunc("GET /v1/threads/{id}", s.getThread)
	s.mux.HandleFunc("POST /v1/threads/{id}/messages", s.sendMessage)
	s.mux.HandleFunc("POST /v1/threads/{id}/fork", s.forkThread)

	s.mux.HandleFunc("GET /v1/approvals", s.listApprovals)
	s.mux.HandleFunc("POST /v1/approvals/{id}/decide", s.decideApproval)
}

func (s *Server) analystService() *analyst.Service {
	svc := analyst.NewService(s.store, s.engine, s.cas, s.workspaceDir, s.guardedProvider())
	svc.Audit = func(action, detail string) {
		s.record(context.Background(), "thread:analyst", "analyst."+action, detail, nil)
	}
	// The active policy profile governs data egress (ADR-0006).
	svc.SetEgressStrict(!s.activePolicy().AllowExternalForPrivate)
	return svc
}

// activePolicy returns the currently selected governance profile (default: conservative).
func (s *Server) activePolicy() policy.Profile {
	name := policy.Default
	if v, err := s.store.GetSetting(context.Background(), "active_policy_profile"); err == nil && v != "" {
		name = v
	}
	return policy.Get(name)
}

// guardedProvider wraps the LLM provider with DLP inspection of outbound content (ADR-0011): vault
// secrets and canaries are blocked on external providers; every hit is recorded and audited.
func (s *Server) guardedProvider() llm.Provider {
	p := s.llmProvider()
	if p == nil {
		return nil
	}
	external := !llm.IsLocal(p)
	load := func(ctx context.Context) (map[string]string, map[string]string) {
		var secrets map[string]string
		if s.vault != nil {
			secrets, _ = s.store.SecretValueMap(ctx, s.vault.Open)
		}
		canaries, _ := s.store.CanaryMap(ctx)
		return secrets, canaries
	}
	onHit := func(ctx context.Context, h dlp.Hit, blocked bool) {
		_ = s.store.RecordDLPEvent(ctx, model.DLPEvent{
			Kind: h.Kind, Label: h.Label, Action: h.Action, Blocked: blocked, Location: "llm:" + p.Name(),
		})
		s.record(ctx, "system", "dlp."+h.Kind, h.Label, map[string]any{"blocked": blocked, "action": h.Action})
		if h.Kind == dlp.KindCanary {
			s.notify(ctx, model.NotifyInfo, "Canary tripped", "Canary "+h.Label+" appeared at an LLM egress", nil, "")
		}
	}
	return dlp.Guard(p, external, load, onHit)
}

func (s *Server) providerName() string {
	p := s.llmProvider()
	if p == nil {
		return ""
	}
	return p.Name()
}

// analystAsk is a convenience: create a thread and send one message.
func (s *Server) analystAsk(w http.ResponseWriter, r *http.Request) {
	if s.llmProvider() == nil {
		writeErr(w, http.StatusServiceUnavailable, "no LLM provider configured (set OSB_LLM_PROVIDER)")
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Message == "" {
		writeErr(w, http.StatusBadRequest, "message is required")
		return
	}
	th, err := s.store.CreateThread(r.Context(), store.NewThread{Title: "ask", Provider: s.providerName()})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := s.analystService().Send(r.Context(), th.ID, req.Message)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordUsage(r.Context(), res)
	s.notifyIfPending(r.Context(), res)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	ts, err := s.store.ListThreads(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID *string `json:"project_id"`
		Title     string  `json:"title"`
		AgentType string  `json:"agent_type"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	th, err := s.store.CreateThread(r.Context(), store.NewThread{ProjectID: req.ProjectID, Title: req.Title, AgentType: req.AgentType, Provider: s.providerName()})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, th)
}

// agentPlaybookStep / agentPlaybookView are the API shapes for an agent playbook (built-in or saved).
type agentPlaybookStep struct {
	Key         string   `json:"key"`
	Profile     string   `json:"profile"`
	Instruction string   `json:"instruction"`
	DependsOn   []string `json:"depends_on"`
}
type agentPlaybookView struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Goal        string              `json:"goal"`
	Steps       []agentPlaybookStep `json:"steps"`
	Builtin     bool                `json:"builtin"`
	Source      string              `json:"source,omitempty"`
}

// listAgentPlaybooks returns the agent playbooks a human can trigger — built-ins plus user-saved ones
// (ADR-0019). Distinct from /v1/playbooks, which lists capability playbooks.
func (s *Server) listAgentPlaybooks(w http.ResponseWriter, r *http.Request) {
	out := []agentPlaybookView{}
	for _, p := range analyst.Playbooks() {
		steps := make([]agentPlaybookStep, 0, len(p.Steps))
		for _, st := range p.Steps {
			steps = append(steps, agentPlaybookStep{Key: st.Key, Profile: st.Profile, Instruction: st.Instruction, DependsOn: st.DependsOn})
		}
		out = append(out, agentPlaybookView{ID: p.ID, Name: p.Name, Description: p.Description, Goal: p.Goal, Steps: steps, Builtin: true})
	}
	if saved, err := s.store.ListSavedPlaybooks(r.Context()); err == nil {
		for _, sp := range saved {
			var steps []agentPlaybookStep
			_ = json.Unmarshal(sp.Steps, &steps)
			out = append(out, agentPlaybookView{ID: sp.ID, Name: sp.Name, Description: sp.Description, Goal: sp.Goal, Steps: steps, Builtin: false, Source: sp.Source})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"playbooks": out})
}

// createSavedPlaybook stores a user-authored agent playbook.
func (s *Server) createSavedPlaybook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Goal        string                 `json:"goal"`
		Steps       []analyst.PlaybookStep `json:"steps"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sp, err := s.analystService().SavePlaybook(r.Context(), req.Name, req.Description, req.Goal, req.Steps, "manual")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

// deleteSavedPlaybook removes a user-saved playbook (built-ins are not deletable).
func (s *Server) deleteSavedPlaybook(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteSavedPlaybook(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "saved playbook not found (built-ins can't be deleted)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// savePlanAsPlaybook records a plan's structure as a reusable playbook (record-as-playbook).
func (s *Server) savePlanAsPlaybook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sp, err := s.analystService().SavePlaybookFromPlan(r.Context(), r.PathValue("id"), req.Name, req.Description)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

// createSchedule schedules a playbook to run on a cadence for a project (ADR-0019 step 4).
func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlaybookID      string `json:"playbook_id"`
		IntervalSeconds int    `json:"interval_seconds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.IntervalSeconds <= 0 {
		writeErr(w, http.StatusBadRequest, "interval_seconds must be positive")
		return
	}
	sc, err := s.store.CreateSchedule(r.Context(), r.PathValue("id"), req.PlaybookID, req.IntervalSeconds, time.Now().UTC())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

// listSchedules returns a project's schedules.
func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	sched, err := s.store.ListSchedulesByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

// updateSchedule enables or pauses a schedule.
func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.store.SetScheduleEnabled(r.Context(), r.PathValue("id"), req.Enabled)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteSchedule removes a schedule.
func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteSchedule(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "schedule not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// startPlan triggers a playbook for a project: it creates the plan and runs it in the background.
func (s *Server) startPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlaybookID string `json:"playbook_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	plan, err := s.analystService().StartPlan(r.Context(), r.PathValue("id"), req.PlaybookID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, plan)
}

// getPlan returns a plan with its steps (poll this to watch a run's progress).
func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.store.GetPlan(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "plan not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// listPlans returns a project's plans (without steps), newest first.
func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlansByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

// getApprovalPolicy returns the sensitive tools (approve-by-default) and the current override rules
// (ADR-0019 §5). The rules promote or demote a tool [+profile] between auto and approve.
func (s *Server) getApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	raw, _ := s.store.GetSetting(r.Context(), analyst.ApprovalPolicySetting)
	rules := []analyst.Rule{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &rules)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sensitive_tools": analyst.SensitiveTools(), "rules": rules})
}

// setApprovalPolicy replaces the override rules. Scope and DLP are enforced separately and unaffected.
func (s *Server) setApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rules []analyst.Rule `json:"rules"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	for _, rl := range req.Rules {
		if rl.Tool == "" {
			writeErr(w, http.StatusBadRequest, "each rule needs a tool")
			return
		}
		if rl.Decision != analyst.DecisionAuto && rl.Decision != analyst.DecisionApprove {
			writeErr(w, http.StatusBadRequest, "each rule's decision must be 'auto' or 'approve'")
			return
		}
	}
	b, err := json.Marshal(req.Rules)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.SetSetting(r.Context(), analyst.ApprovalPolicySetting, string(b)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": req.Rules})
}

// listAgentProfiles returns the agent profiles — built-ins plus user-defined ones (ADR-0019). Used by
// the thread-creation picker and the custom-agent editor.
func (s *Server) listAgentProfiles(w http.ResponseWriter, r *http.Request) {
	type prof struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Persona     string   `json:"persona,omitempty"`
		Tools       []string `json:"tools,omitempty"`
		Builtin     bool     `json:"builtin"`
	}
	out := []prof{}
	for _, p := range analyst.Profiles() {
		out = append(out, prof{ID: p.ID, Name: p.Name, Description: p.Description, Persona: p.Persona, Tools: p.Tools, Builtin: true})
	}
	if saved, err := s.store.ListSavedProfiles(r.Context()); err == nil {
		for _, sp := range saved {
			var tools []string
			_ = json.Unmarshal(sp.Tools, &tools)
			out = append(out, prof{ID: sp.ID, Name: sp.Name, Description: sp.Description, Persona: sp.Persona, Tools: tools, Builtin: false})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

// settingsSections returns every declarative section (core + extension-declared). Extension sections are
// a future manifest capability (ADR-0021); for now this is the core set.
func (s *Server) settingsSections() []settings.Section {
	return settings.CoreSections()
}

// getSettings returns the declarative section schemas plus current values (defaults applied).
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	sections := s.settingsSections()
	values := map[string]string{}
	for _, sec := range sections {
		for _, f := range sec.Fields {
			v, err := s.store.GetSetting(r.Context(), f.Key)
			if err != nil || v == "" {
				v = f.Default
			}
			values[f.Key] = v
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sections": sections, "values": values})
}

// putSettings writes values, validating each key against a known field.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Values map[string]string `json:"values"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sections := s.settingsSections()
	for key, val := range req.Values {
		if _, ok := settings.FieldByKey(sections, key); !ok {
			writeErr(w, http.StatusBadRequest, "unknown setting "+key)
			return
		}
		if err := s.store.SetSetting(r.Context(), key, val); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// listAgentTools returns the tool catalog (name + description) for building a custom agent's allow-list.
func (s *Server) listAgentTools(w http.ResponseWriter, _ *http.Request) {
	type tool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	out := []tool{}
	for _, t := range analyst.Tools() {
		out = append(out, tool{Name: t.Name, Description: t.Description})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": out})
}

// createSavedProfile stores a user-defined agent profile.
func (s *Server) createSavedProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Persona     string   `json:"persona"`
		Tools       []string `json:"tools"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sp, err := s.analystService().SaveProfile(r.Context(), req.Name, req.Description, req.Persona, req.Tools)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

// deleteSavedProfile removes a user-defined profile (built-ins can't be deleted).
func (s *Server) deleteSavedProfile(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteSavedProfile(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "saved profile not found (built-ins can't be deleted)")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	th, err := s.store.GetThread(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "thread not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	msgs, err := s.store.ListMessages(r.Context(), th.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": th, "messages": msgs})
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	if s.llmProvider() == nil {
		writeErr(w, http.StatusServiceUnavailable, "no LLM provider configured")
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Message == "" {
		writeErr(w, http.StatusBadRequest, "message is required")
		return
	}
	res, err := s.analystService().Send(r.Context(), r.PathValue("id"), req.Message)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "thread not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordUsage(r.Context(), res)
	s.notifyIfPending(r.Context(), res)
	writeJSON(w, http.StatusOK, res)
}

// recordUsage persists a run's token usage tagged with the active provider/model + the run's project,
// so usage can be compared across models and vendors (the plan's usage_record).
func (s *Server) recordUsage(ctx context.Context, res analyst.SendResult) {
	info := s.activeInfo()
	projectID := ""
	if res.Thread.ProjectID != nil {
		projectID = *res.Thread.ProjectID
	}
	_ = s.store.RecordUsage(ctx, model.UsageRecord{
		ProjectID: projectID, ThreadID: res.Thread.ID,
		Provider: info.Type, Model: info.Model,
		InputTokens: res.InputTokens, OutputTokens: res.OutputTokens,
	})
}

// notifyIfPending raises an approval notification when an Analyst run pauses on a gated tool.
func (s *Server) notifyIfPending(ctx context.Context, res analyst.SendResult) {
	if res.Pending == nil {
		return
	}
	var pid *string
	if res.Thread.ProjectID != nil {
		pid = res.Thread.ProjectID
	}
	s.notify(ctx, model.NotifyApproval, "Approval needed",
		"The Analyst wants to run "+res.Pending.Tool, pid, "approval:"+res.Pending.ID)
}

func (s *Server) forkThread(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seq int `json:"seq"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	child, err := s.store.ForkThread(r.Context(), r.PathValue("id"), req.Seq)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "thread not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, child)
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	aps, err := s.store.ListPendingApprovals(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, aps)
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	if s.llmProvider() == nil {
		writeErr(w, http.StatusServiceUnavailable, "no LLM provider configured")
		return
	}
	var req struct {
		Decision string `json:"decision"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Decision != "approve" && req.Decision != "deny" {
		writeErr(w, http.StatusBadRequest, "decision must be 'approve' or 'deny'")
		return
	}
	approvalID := r.PathValue("id")
	res, err := s.analystService().Decide(r.Context(), approvalID, req.Decision)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "approval not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "approval."+req.Decision, approvalID, nil)
	s.recordUsage(r.Context(), res)
	s.notifyIfPending(r.Context(), res)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "opensecbench-control-plane",
		"version": version.Version,
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.store.PingContext(r.Context()) != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- organizations ---

func (s *Server) listOrganizations(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.store.ListOrganizations(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orgs)
}

func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	org, err := s.store.CreateOrganization(r.Context(), req.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

// --- targets ---

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.store.ListTargets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *Server) createTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string  `json:"name"`
		Description    string  `json:"description"`
		OrganizationID *string `json:"organization_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	target, err := s.store.CreateTarget(r.Context(), req.Name, req.Description, req.OrganizationID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, target)
}

// --- projects ---

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string   `json:"name"`
		OrganizationID *string  `json:"organization_id"`
		GroupID        *string  `json:"group_id"`
		TargetIDs      []string `json:"target_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	project, err := s.store.CreateProject(r.Context(), store.NewProject{
		Name:           req.Name,
		OrganizationID: req.OrganizationID,
		GroupID:        req.GroupID,
		TargetIDs:      req.TargetIDs,
	})
	if err != nil {
		// TODO(P1+): distinguish constraint violations (e.g. unknown target) as 400.
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteProject(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- search ---

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	results, err := s.store.Search(r.Context(), r.URL.Query().Get("q"), 25)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// --- templates ---

func (s *Server) listTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, template.BuiltIns())
}

func (s *Server) createProjectFromTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TemplateID string `json:"template_id"`
		Name       string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	tmpl, ok := template.Get(req.TemplateID)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown template "+req.TemplateID)
		return
	}

	proj, err := s.store.CreateProject(r.Context(), store.NewProject{Name: req.Name})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]any{"project": proj, "template": tmpl}
	if tmpl.DefaultApplication != "" {
		app, err := s.store.CreateApplication(r.Context(), proj.ID, tmpl.DefaultApplication)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp["application"] = app
	}
	writeJSON(w, http.StatusCreated, resp)
}

// --- applications & assets ---

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.ListApplicationsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	app, err := s.store.CreateApplication(r.Context(), r.PathValue("id"), req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApplication(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "application not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request) {
	assets, err := s.store.ListAssetsByApplication(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, assets)
}

func (s *Server) createAsset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type        string `json:"type"`
		Location    string `json:"location"`
		Sensitivity string `json:"sensitivity"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.store.CreateAsset(r.Context(), store.NewAsset{
		ApplicationID: r.PathValue("id"),
		Type:          req.Type,
		Location:      req.Location,
		Sensitivity:   req.Sensitivity,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, asset)
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	asset, err := s.store.GetAsset(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// --- context items ---

func (s *Server) listContext(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListContextItemsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// ingestContext stores a request body in the CAS as an input artifact and records a context item.
// Metadata comes from query params (name, type); content type is taken from the header.
func (s *Server) ingestContext(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name query parameter is required")
		return
	}
	ctype := r.URL.Query().Get("type")
	if ctype == "" {
		ctype = model.ContextDocument
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<20) // 64 MiB cap
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	digest, err := s.cas.Put(bytes.NewReader(data))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	mediaType := r.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	art, err := s.store.CreateArtifact(r.Context(), model.Artifact{
		SHA256:    digest,
		Size:      int64(len(data)),
		Kind:      model.ArtifactInput,
		Name:      name,
		MediaType: mediaType,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ci, err := s.store.CreateContextItem(r.Context(), model.ContextItem{
		ProjectID:  r.PathValue("id"),
		Type:       ctype,
		Name:       name,
		ArtifactID: art.ID,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ci)
}

// --- scope allowlist (P6) ---

func (s *Server) listScope(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ListScopeEntries(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) addScope(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	entry, err := s.store.AddScopeEntry(r.Context(), r.PathValue("id"), req.Kind, req.Value)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "scope.add", entry.ID, map[string]string{
		"project": entry.ProjectID, "kind": entry.Kind, "value": entry.Value,
	})
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) deleteScope(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.DeleteScopeEntry(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "scope entry not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "scope.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// listAudit returns recent audit events (append-only, hash-chained). ?limit=N caps the count.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	events, err := s.store.ListAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// verifyAudit recomputes the audit hash chain and reports whether it is intact (tamper detection).
func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	ok, broken, count, err := s.store.VerifyAuditChain(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"ok": ok, "events": count}
	if !ok {
		resp["broken_at_seq"] = broken
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Replay / HTTP exchanges (P7) ---

func (s *Server) listExchanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ExchangeFilter{
		Origin: q.Get("origin"),
		Method: q.Get("method"),
		Query:  q.Get("q"),
	}
	if v := q.Get("status"); v != "" {
		f.Status, _ = strconv.Atoi(v)
	}
	if v := q.Get("limit"); v != "" {
		f.Limit, _ = strconv.Atoi(v)
	}
	items, err := s.store.ListExchangesFiltered(r.Context(), r.PathValue("id"), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.annotateScope(r.Context(), r.PathValue("id"), items))
}

// exchangeView adds computed, non-stored fields to an exchange for clients.
type exchangeView struct {
	model.HTTPExchange
	InScope bool `json:"in_scope"`
}

// annotateScope marks each exchange in- or out-of-scope against the project's current allowlist
// (empty allowlist ⇒ everything is in scope). Uses pkg/scope so the UI never re-implements matching.
func (s *Server) annotateScope(ctx context.Context, projectID string, items []model.HTTPExchange) []exchangeView {
	entries, _ := s.store.ListScopeEntries(ctx, projectID)
	rules := make([]scope.Entry, 0, len(entries))
	for _, e := range entries {
		rules = append(rules, scope.Entry{Kind: e.Kind, Value: e.Value})
	}
	views := make([]exchangeView, len(items))
	for i, ex := range items {
		views[i] = exchangeView{HTTPExchange: ex, InScope: len(rules) == 0 || scope.Check(rules, ex.URL) == nil}
	}
	return views
}

func (s *Server) createExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"name"`
		Method         string `json:"method"`
		URL            string `json:"url"`
		RequestHeaders string `json:"request_headers"`
		RequestBody    string `json:"request_body"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.URL == "" {
		writeErr(w, http.StatusBadRequest, "url is required")
		return
	}
	ex, err := s.store.CreateExchange(r.Context(), model.HTTPExchange{
		ProjectID:      r.PathValue("id"),
		Name:           req.Name,
		Method:         req.Method,
		URL:            req.URL,
		RequestHeaders: req.RequestHeaders,
		RequestBody:    req.RequestBody,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ex)
}

func (s *Server) getExchange(w http.ResponseWriter, r *http.Request) {
	ex, err := s.store.GetExchange(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "exchange not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ex)
}

// sendExchange scope-guards the target, issues the request, and records the response (ADR-0007).
func (s *Server) sendExchange(w http.ResponseWriter, r *http.Request) {
	ex, err := s.store.GetExchange(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "exchange not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Scope guard: an out-of-scope target is refused before anything is sent. The draft row persists
	// as the durable record of the blocked attempt.
	entries, err := s.store.ListScopeEntries(r.Context(), ex.ProjectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(entries) > 0 {
		rules := make([]scope.Entry, len(entries))
		for i, en := range entries {
			rules[i] = scope.Entry{Kind: en.Kind, Value: en.Value}
		}
		if serr := scope.Check(rules, ex.URL); serr != nil {
			s.record(r.Context(), actorOf(r), "replay.blocked", ex.ID, map[string]string{"url": ex.URL, "reason": serr.Error()})
			writeErr(w, http.StatusForbidden, "blocked by scope guard: "+serr.Error())
			return
		}
	}

	resp, err := s.replay.Send(r.Context(), replay.Request{
		Method: ex.Method, URL: ex.URL, Headers: ex.RequestHeaders, Body: ex.RequestBody,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "send failed: "+err.Error())
		return
	}
	if err := s.store.RecordResponse(r.Context(), ex.ID, resp.Status, resp.Headers, resp.Body, resp.DurationMS); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "replay.send", ex.ID, map[string]any{
		"method": ex.Method, "url": ex.URL, "status": resp.Status,
	})
	updated, _ := s.store.GetExchange(r.Context(), ex.ID)
	s.events.Publish(events.Event{Type: "exchange", ProjectID: ex.ProjectID, Payload: updated})
	writeJSON(w, http.StatusOK, updated)
}

// exchangeEvidence promotes a sent exchange's response into the CAS as an artifact and records a
// human-origin observation (ADR-0005), so it enters the same triage → finding path as tool output.
func (s *Server) exchangeEvidence(w http.ResponseWriter, r *http.Request) {
	ex, err := s.store.GetExchange(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "exchange not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ex.SentAt == nil {
		writeErr(w, http.StatusBadRequest, "exchange has not been sent yet")
		return
	}
	var req struct {
		Note   string `json:"note"`
		ItemID string `json:"item_id"` // optional: attach this evidence to a methodology item (ADR-0015 P3b)
	}
	_ = decodeJSONOptional(r, &req)

	blob := ex.ResponseHeaders + "\n" + ex.ResponseBody
	digest, err := s.cas.Put(bytes.NewReader([]byte(blob)))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	art, err := s.store.CreateArtifact(r.Context(), model.Artifact{
		SHA256:    digest,
		Size:      int64(len(blob)),
		Kind:      model.ArtifactInput,
		Name:      "http-response",
		MediaType: "message/http",
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	title := ex.Method + " " + ex.URL
	if ex.Status != nil {
		title = title + " → " + http.StatusText(*ex.Status)
	}
	obs, err := s.store.CreateObservation(r.Context(), model.Observation{
		ArtifactID:  &art.ID,
		Origin:      model.OriginHuman,
		ReviewState: model.ReviewUnreviewed,
		Title:       title,
		Detail:      req.Note,
		Severity:    "info",
		Location:    ex.URL,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.ItemID != "" {
		if err := s.store.LinkCoverageObservation(r.Context(), ex.ProjectID, req.ItemID, obs.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.record(r.Context(), actorOf(r), "evidence.exchange", obs.ID, map[string]string{"exchange": ex.ID, "item": req.ItemID})
	} else {
		s.record(r.Context(), actorOf(r), "evidence.exchange", obs.ID, map[string]string{"exchange": ex.ID})
	}
	writeJSON(w, http.StatusCreated, obs)
}

// --- capabilities, tasks, artifacts ---

func (s *Server) listCapabilities(w http.ResponseWriter, _ *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "engine unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.engine.Registry().Manifests())
}

func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "engine unavailable")
		return
	}
	var req struct {
		CapabilityID  string            `json:"capability_id"`
		TargetDir     string            `json:"target_dir"`
		Actor         string            `json:"actor"`
		AssetID       *string           `json:"asset_id"`
		ApplicationID *string           `json:"application_id"`
		ProjectID     *string           `json:"project_id"`
		SecretRefs    map[string]string `json:"secret_refs"`
		Params        map[string]any    `json:"params"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.CapabilityID == "" {
		writeErr(w, http.StatusBadRequest, "capability_id is required")
		return
	}
	out, err := s.engine.Run(r.Context(), task.RunRequest{
		CapabilityID:  req.CapabilityID,
		TargetDir:     req.TargetDir,
		Actor:         req.Actor,
		AssetID:       req.AssetID,
		ApplicationID: req.ApplicationID,
		ProjectID:     req.ProjectID,
		SecretRefs:    req.SecretRefs,
		Params:        req.Params,
	})
	// A validation/plan error produces no task; a run failure (including a scope block) produces a
	// failed task the caller still wants to inspect.
	if err != nil && out.Task.ID == "" {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	action := "task.run"
	if errors.Is(err, task.ErrOutOfScope) {
		action = "task.blocked"
	}
	s.record(r.Context(), actorOf(r), action, out.Task.ID, map[string]any{
		"capability": req.CapabilityID, "status": out.Task.Status, "error": out.Task.Error,
	})
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks(r.Context(), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "engine unavailable")
		return
	}
	err := s.engine.Cancel(r.PathValue("id"))
	if errors.Is(err, task.ErrTaskNotRunning) {
		writeErr(w, http.StatusConflict, "task is not running")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	arts, err := s.store.ListArtifactsByTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, arts)
}

func (s *Server) getArtifactContent(w http.ResponseWriter, r *http.Request) {
	art, err := s.store.GetArtifact(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rc, err := s.cas.Open(art.SHA256)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "artifact bytes unavailable")
		return
	}
	defer func() { _ = rc.Close() }()
	w.Header().Set("Content-Type", art.MediaType)
	_, _ = io.Copy(w, rc)
}

// --- playbooks ---

func (s *Server) listPlaybooks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, playbook.BuiltIns())
}

func (s *Server) runPlaybook(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "engine unavailable")
		return
	}
	var req struct {
		AssetID string `json:"asset_id"`
		Actor   string `json:"actor"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AssetID == "" {
		writeErr(w, http.StatusBadRequest, "asset_id is required")
		return
	}
	actor := req.Actor
	if actor == "" {
		actor = "human"
	}
	playbookID := r.PathValue("id")
	res, err := playbook.NewRunner(s.engine, s.store).Run(r.Context(), playbookID, req.AssetID, actor)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actor, "playbook.run", playbookID, map[string]any{
		"asset": req.AssetID, "status": res.Run.Status, "tasks": len(res.Run.TaskIDs),
	})
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) listPlaybookRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListPlaybookRuns(r.Context(), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) getPlaybookRun(w http.ResponseWriter, r *http.Request) {
	pr, err := s.store.GetPlaybookRun(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "playbook run not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

// --- observations & findings ---

func (s *Server) getTaskObservations(w http.ResponseWriter, r *http.Request) {
	obs, err := s.store.ListObservationsByTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, obs)
}

func (s *Server) reviewObservation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.store.ReviewObservation(r.Context(), r.PathValue("id"), req.State)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "observation not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFindings(w http.ResponseWriter, r *http.Request) {
	findings, err := s.store.ListFindings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, findings)
}

func (s *Server) createFinding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApplicationID  *string  `json:"application_id"`
		Title          string   `json:"title"`
		Severity       string   `json:"severity"`
		Description    string   `json:"description"`
		CWE            string   `json:"cwe"`
		ObservationIDs []string `json:"observation_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	f, err := s.store.CreateFinding(r.Context(), store.NewFinding{
		ApplicationID:  req.ApplicationID,
		Title:          req.Title,
		Severity:       req.Severity,
		Description:    req.Description,
		CWE:            req.CWE,
		ObservationIDs: req.ObservationIDs,
	})
	if err != nil {
		// Rejected for unconfirmed/unknown observations, etc.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) getFinding(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.GetFinding(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "finding not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// --- helpers ---

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// record appends an audit event. It is best-effort: a failed write is logged but never fails the
// request that triggered it (a local single-user workbench should not lose work to an audit hiccup).
func (s *Server) record(ctx context.Context, actor, action, target string, data any) {
	var raw json.RawMessage
	if data != nil {
		raw, _ = json.Marshal(data)
	}
	if _, err := s.store.AppendAudit(ctx, actor, action, target, raw); err != nil {
		log.Printf("audit append failed (%s %s): %v", action, target, err)
	}
}

// actorOf returns the request's declared actor (X-OSB-Actor header), defaulting to "human".
func actorOf(r *http.Request) string {
	if a := r.Header.Get("X-OSB-Actor"); a != "" {
		return a
	}
	return "human"
}

// decodeJSONOptional decodes a body if present, tolerating an empty body (EOF).
func decodeJSONOptional(r *http.Request, v any) error {
	err := json.NewDecoder(r.Body).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
