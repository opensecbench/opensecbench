// Package api exposes the control-plane HTTP API that every client (desktop, CLI, future web)
// talks to. Domain logic lives in the control-plane packages, never in a client (ADR-0001).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/analyst"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/dlp"
	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/extension"
	"github.com/opensecbench/opensecbench/pkg/hub"
	"github.com/opensecbench/opensecbench/pkg/integration"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/llm/catalog"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/playbook"
	"github.com/opensecbench/opensecbench/pkg/policy"
	"github.com/opensecbench/opensecbench/pkg/proxy"
	"github.com/opensecbench/opensecbench/pkg/replay"
	"github.com/opensecbench/opensecbench/pkg/report"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/runnerhub"
	"github.com/opensecbench/opensecbench/pkg/scope"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/session"
	"github.com/opensecbench/opensecbench/pkg/settings"
	"github.com/opensecbench/opensecbench/pkg/srcfile"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
	"github.com/opensecbench/opensecbench/pkg/template"
	"github.com/opensecbench/opensecbench/pkg/version"
)

// Deps are the control-plane services the API exposes.
type Deps struct {
	Store        *store.Manager
	Engine       *task.Engine
	CAS          *cas.Store   // single store for tests/combined; production passes CASResolver instead
	CASResolver  cas.Resolver // per-project CAS (ADR-0049); takes precedence over CAS when set
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
	mgr            *store.Manager
	engine         *task.Engine
	casr           cas.Resolver
	providerMu     sync.RWMutex
	provider       llm.Provider
	activeProvider providerInfo
	replay         *replay.Client
	events         *events.Hub
	runners        *runnerhub.Hub
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
	runReg         *analyst.RunRegistry // shared record of in-flight background agent runs

	extMu sync.Mutex
	exts  []extension.Loaded

	sessMu   sync.Mutex
	sessions map[string]*liveSession

	selfPort string // control-plane listen port; proxy skips capturing the app's own traffic to it

	proxyMu sync.Mutex
	proxies map[string]*liveProxy

	ruleMu       sync.Mutex
	matchReplace map[string]*ruleEngine // per-project match/replace engines (ADR-0016 Step 4)
}

// errNoStore is returned when a handler needs the database but the server was built without one.
var errNoStore = errors.New("api: no store configured")

// globalAskProjectID is the reserved project that homes project-less analyst threads (the global
// assistant). Every thread must live in some project's database (ADR-0049); this is theirs. The leading
// underscore keeps it out of the UUID space, and it's never registered in the project index, so it never
// shows up as an engagement in cross-project listings.
const globalAskProjectID = "_global"

// threadProject maps an empty project id to the reserved global project so a thread always has a home.
func threadProject(projectID string) string {
	if projectID == "" {
		return globalAskProjectID
	}
	return projectID
}

// ptrIfSet returns &s, or nil when s is empty — for a nullable project_id that must stay NULL for a
// genuinely project-less (global-assistant) thread while a real project stamps its id.
func ptrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// casResolver picks the per-project CAS resolver when provided (production/split), otherwise wraps the
// single store as a fixed resolver (tests/combined). Nil when neither is set.
func casResolver(deps Deps) cas.Resolver {
	if deps.CASResolver != nil {
		return deps.CASResolver
	}
	if deps.CAS != nil {
		return cas.Fixed(deps.CAS)
	}
	return nil
}

// casFor returns the content store owning a project's blobs (ADR-0049). Returns nil if CAS is
// unconfigured or the project can't be resolved; callers treat that like any store error.
func (s *Server) casFor(projectID string) *cas.Store {
	if s.casr == nil {
		return nil
	}
	st, err := s.casr.For(projectID)
	if err != nil {
		return nil
	}
	return st
}

// global returns the instance-wide database handle (ADR-0049): org tree, targets, KB, settings,
// providers, runners, secrets, audit, and the project index. Nil-safe so partially-built test servers
// don't panic.
func (s *Server) global() *store.DB {
	if s.mgr == nil {
		return nil
	}
	return s.mgr.Global()
}

// projectFromReq resolves the active project for a request. The X-Project-Id header (which the frontend
// sets to the project being viewed) is authoritative — on a flat route like /v1/threads/{id}/messages the
// {id} is an entity, not the project. It falls back to the {id} path value only for project-nested routes
// (/v1/projects/{id}/...) used by clients that don't send the header (tests, CLI).
func projectFromReq(r *http.Request) string {
	if h := r.Header.Get("X-Project-Id"); h != "" {
		return h
	}
	// URL-embedded requests (artifact <img>/<a>) can't set a header, so they pass ?project=.
	if q := r.URL.Query().Get("project"); q != "" {
		return q
	}
	return r.PathValue("id")
}

// projectDB returns the per-project database handle for a request's active project. In the transitional
// combined backing (phase 2a) this is the same handle as global(); phase 2b routes it to projects/<id>/.
func (s *Server) projectDB(r *http.Request) (*store.DB, error) {
	if s.mgr == nil {
		return nil, errNoStore
	}
	return s.mgr.Project(projectFromReq(r))
}

// pdb resolves the request's active-project database, falling back to the global handle if the project
// can't be resolved (missing id / open error) so a handler never nil-panics. Under the combined backing
// this is always the same handle as global(); after the split a missing project id surfaces as a
// no-such-table query error, which handlers return as a 4xx/5xx.
func (s *Server) pdb(r *http.Request) *store.DB {
	db, err := s.projectDB(r)
	if err != nil || db == nil {
		return s.global()
	}
	return db
}

// pdbID resolves a project database by id, for non-HTTP contexts (goroutines, helpers) that already
// hold the project id. Same fallback semantics as pdb.
func (s *Server) pdbID(projectID string) *store.DB {
	if s.mgr == nil {
		return nil
	}
	db, err := s.mgr.Project(projectID)
	if err != nil || db == nil {
		return s.global()
	}
	return db
}

// pdbPtr resolves a project database for an optional project id (nil → global fallback), for
// best-effort writes like notifications that may not be project-scoped.
func (s *Server) pdbPtr(projectID *string) *store.DB {
	if projectID == nil || *projectID == "" {
		return s.global()
	}
	return s.pdbID(*projectID)
}

// New builds the API server with its routes registered.
func New(deps Deps) *Server {
	s := &Server{
		mux:          http.NewServeMux(),
		mgr:          deps.Store,
		engine:       deps.Engine,
		casr:         casResolver(deps),
		provider:     deps.Provider,
		replay:       replay.New(0),
		events:       events.NewHub(),
		runners:      runnerhub.New(),
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
		runReg:       analyst.NewRunRegistry(),
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
	// Reconcile playbook runs left running by a prior process (their in-flight tasks are reconciled by
	// the engine); the runs themselves would otherwise linger as ghosts (ADR-0022).
	if s.mgr != nil {
		if n, err := s.mgr.FailUnfinishedPlaybookRuns(context.Background()); err == nil && n > 0 {
			log.Printf("api: reconciled %d unfinished playbook run(s) to failed on startup", n)
		}
	}
	// Remote-runner selection (ADR-0024): resolve a task's runner_target to a hub-connected runner. A
	// revoked or offline runner errors, so the engine fails the task cleanly rather than running local.
	if s.engine != nil && s.mgr != nil {
		s.engine.SetRunnerResolver(func(id string) (runner.Runner, error) {
			r, err := s.global().GetRunner(context.Background(), id)
			if err != nil {
				return nil, err
			}
			if r.Status != model.RunnerActive {
				return nil, fmt.Errorf("runner %s is %s", r.Name, r.Status)
			}
			if !s.runners.Online(id) {
				return nil, fmt.Errorf("runner %s is offline", r.Name)
			}
			return s.runners.Runner(id, r.Name), nil
		})
	}
	s.startScheduler()
	return s
}

// startScheduler runs the playbook scheduler in the background (ADR-0019 step 4). A due schedule fires
// StartPlan; it is stopped on Close.
func (s *Server) startScheduler() {
	if s.mgr == nil {
		return
	}
	s.sched = analyst.NewScheduler(s.mgr, func(ctx context.Context, projectID, playbookID string) error {
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
			// X-Project-Id scopes requests to the active project (ADR-0049); it must be allowed here or
			// the browser's preflight blocks every project-scoped request.
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Project-Id")
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

	s.mux.HandleFunc("GET /v1/home", s.getHome)
	s.mux.HandleFunc("GET /v1/search", s.search)
	s.mux.HandleFunc("GET /v1/projects/{id}/search/code", s.searchCode)
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
	s.mux.HandleFunc("POST /v1/kb/{id}/verify", s.verifyKBEntry)
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
	s.mux.HandleFunc("GET /v1/models/catalog", s.getModelCatalog)
	s.mux.HandleFunc("GET /v1/models/routing", s.getModelRouting)
	s.mux.HandleFunc("PUT /v1/models/routing", s.setModelRouting)
	// A connection (providers row) serves many models, discovered live and enriched by the overlay (ADR-0052).
	s.mux.HandleFunc("GET /v1/connections/{id}/models", s.listConnectionModels)
	s.mux.HandleFunc("POST /v1/connections/{id}/models/refresh", s.refreshConnectionModelsHandler)
	s.mux.HandleFunc("GET /v1/analyst/approval-policy", s.getApprovalPolicy)
	s.mux.HandleFunc("PUT /v1/analyst/approval-policy", s.setApprovalPolicy)
	s.mux.HandleFunc("GET /v1/analyst/playbooks", s.listAgentPlaybooks)
	s.mux.HandleFunc("POST /v1/analyst/playbooks", s.createSavedPlaybook)
	s.mux.HandleFunc("GET /v1/analyst/playbooks/{id}", s.getAgentPlaybook)
	s.mux.HandleFunc("PUT /v1/analyst/playbooks/{id}", s.updateSavedPlaybook)
	s.mux.HandleFunc("DELETE /v1/analyst/playbooks/{id}", s.deleteSavedPlaybook)
	s.mux.HandleFunc("POST /v1/projects/{id}/plans", s.startPlan)
	s.mux.HandleFunc("GET /v1/projects/{id}/plans", s.listPlans)
	s.mux.HandleFunc("GET /v1/plans/{id}", s.getPlan)
	s.mux.HandleFunc("POST /v1/plans/{id}/cancel", s.cancelPlan)
	s.mux.HandleFunc("POST /v1/plans/{id}/steps/{stepID}/resolve", s.resolvePlanGate)
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
	s.mux.HandleFunc("DELETE /v1/reports/{id}", s.deleteReport)

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
	// PUT (not PATCH): PATCH is absent from the CORS allow-methods list, so the browser preflight would
	// reject it — the same reason the asset-update endpoint uses PUT.
	s.mux.HandleFunc("PUT /v1/context/{id}", s.updateContext)
	s.mux.HandleFunc("DELETE /v1/context/{id}", s.deleteContext)
	s.mux.HandleFunc("GET /v1/projects/{id}/scope", s.listScope)
	s.mux.HandleFunc("POST /v1/projects/{id}/scope", s.addScope)
	s.mux.HandleFunc("GET /v1/projects/{id}/engagement", s.getEngagement)
	s.mux.HandleFunc("PUT /v1/projects/{id}/engagement", s.setEngagement)
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
	s.mux.HandleFunc("PUT /v1/assets/{id}", s.updateAsset)
	s.mux.HandleFunc("GET /v1/assets/{id}/ecosystems", s.getAssetEcosystems)
	s.mux.HandleFunc("PUT /v1/assets/{id}/ecosystems", s.setAssetEcosystems)
	// Source viewer (ADR-0050): read a source_repo asset's tree/files for the in-app code viewer and
	// click-to-file from findings. Reads are path-confined to the asset root (pkg/srcfile).
	s.mux.HandleFunc("GET /v1/assets/{id}/source", s.getAssetSource)
	s.mux.HandleFunc("GET /v1/assets/{id}/tree", s.getAssetTree)

	s.mux.HandleFunc("GET /v1/capabilities", s.listCapabilities)
	s.mux.HandleFunc("GET /v1/tasks", s.listTasks)
	s.mux.HandleFunc("GET /v1/activity", s.activity)
	s.mux.HandleFunc("POST /v1/activity/agents/{id}/cancel", s.cancelAgentRun)
	s.mux.HandleFunc("GET /v1/activity/feed", s.getActivity)
	s.mux.HandleFunc("POST /v1/tasks", s.runTask)
	s.mux.HandleFunc("POST /v1/projects/{id}/scan", s.scanProject)
	s.mux.HandleFunc("POST /v1/projects/{id}/reevaluate", s.reevaluateProject)
	s.mux.HandleFunc("GET /v1/projects/{id}/routes", s.listProjectRoutes)
	s.mux.HandleFunc("GET /v1/projects/{id}/summary", s.projectSummary)
	s.mux.HandleFunc("GET /v1/projects/{id}/reachability", s.listReachability)
	s.mux.HandleFunc("POST /v1/projects/{id}/reachability", s.addReachability)

	// Remote runners — operator actions on the trusted (loopback) API (ADR-0024). The runner protocol
	// itself is served on a separate network-exposed listener; see RunnerHandler.
	s.mux.HandleFunc("GET /v1/runners", s.listRunners)
	s.mux.HandleFunc("POST /v1/runners/enroll-token", s.mintEnrollToken)
	s.mux.HandleFunc("DELETE /v1/runners/{id}", s.deleteRunner)
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
	// Human triage actions on a raw observation (Observations surface): promote to a finding, or open an
	// investigation. Dismiss/restore reuse the review endpoint above (rejected / unreviewed).
	s.mux.HandleFunc("POST /v1/observations/{id}/promote", s.promoteObservation)
	s.mux.HandleFunc("POST /v1/observations/{id}/investigate", s.investigateObservation)
	// Batch AI triage: one agent works down the given observations (or all untriaged), dismissing noise and
	// flagging real ones — the LLM first pass over the queue.
	s.mux.HandleFunc("POST /v1/projects/{id}/triage", s.startTriage)

	s.mux.HandleFunc("GET /v1/findings", s.listFindings)
	s.mux.HandleFunc("POST /v1/findings", s.createFinding)
	s.mux.HandleFunc("GET /v1/findings/{id}", s.getFinding)
	s.mux.HandleFunc("POST /v1/findings/{id}/status", s.setFindingStatus)
	s.mux.HandleFunc("GET /v1/integrations", s.listIntegrations)
	s.mux.HandleFunc("GET /v1/findings/{id}/links", s.listFindingLinks)
	s.mux.HandleFunc("POST /v1/findings/{id}/push", s.pushFinding)
	// Per-project integration configs + inbound pull (ADR-0027).
	// Global connectors (Library) + per-project bindings (ADR-0027 / IA declutter).
	s.mux.HandleFunc("GET /v1/connectors", s.listConnectors)
	s.mux.HandleFunc("POST /v1/connectors", s.createConnector)
	s.mux.HandleFunc("DELETE /v1/connectors/{id}", s.deleteConnector)
	s.mux.HandleFunc("GET /v1/projects/{id}/integrations", s.listProjectIntegrations)
	s.mux.HandleFunc("PUT /v1/projects/{id}/integrations/{connectorId}", s.setBinding)
	s.mux.HandleFunc("DELETE /v1/projects/{id}/integrations/{connectorId}", s.deleteBinding)
	s.mux.HandleFunc("POST /v1/projects/{id}/integrations/{connectorId}/pull", s.pullIntegration)
	// Post-run disposition routing + investigations (ADR-0028).
	s.mux.HandleFunc("GET /v1/projects/{id}/observations", s.listProjectObservations)
	// Knowledge dossier — consolidated "what we know" view (ADR-0042).
	s.mux.HandleFunc("GET /v1/targets/{id}/dossier", s.targetDossier)
	s.mux.HandleFunc("GET /v1/projects/{id}/dossier", s.projectDossier)
	// Semantic corpus/KB retrieval (ADR-0039).
	s.mux.HandleFunc("POST /v1/projects/{id}/reindex", s.reindexCorpus)
	s.mux.HandleFunc("GET /v1/projects/{id}/search-corpus", s.searchCorpus)
	s.mux.HandleFunc("GET /v1/projects/{id}/investigations", s.listInvestigations)
	s.mux.HandleFunc("POST /v1/investigations/{id}/run", s.runInvestigation)
	s.mux.HandleFunc("POST /v1/investigations/{id}/status", s.setInvestigationStatus)
	s.mux.HandleFunc("GET /v1/projects/{id}/dispositions", s.listDispositions)
	s.mux.HandleFunc("POST /v1/projects/{id}/dispositions", s.setDispositionRule)
	s.mux.HandleFunc("DELETE /v1/projects/{id}/dispositions/{rule}", s.deleteDispositionRule)

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
	svc := analyst.NewService(s.mgr, s.engine, s.casr, s.workspaceDir, s.guardedProvider())
	svc.SetRunRegistry(s.runReg)
	svc.Audit = func(action, detail string) {
		s.record(context.Background(), "thread:analyst", "analyst."+action, detail, nil)
	}
	// The active policy profile governs data egress (ADR-0006).
	ap := s.activePolicy()
	svc.SetEgressPolicy(ap.AllowExternalForInternal, ap.AllowExternalForPrivate)
	// Cross-provider model routing (ADR-0021): build a configured provider by registry id, DLP-guarded.
	svc.SetProviderResolver(func(ctx context.Context, id string) (llm.Provider, error) {
		p, err := s.global().GetProvider(ctx, id)
		if err != nil {
			return nil, err
		}
		built, err := s.buildProvider(p)
		if err != nil {
			return nil, err
		}
		return s.guardProvider(built), nil
	})
	// The send_request tool can egress from a chosen runner's vantage (ADR-0025).
	svc.SetEgressSender(s.egressSend)
	return svc
}

// activePolicy returns the currently selected governance profile (default: personal).
func (s *Server) activePolicy() policy.Profile {
	name := policy.Default
	if v, err := s.global().GetSetting(context.Background(), "active_policy_profile"); err == nil && v != "" {
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
	return s.guardProvider(p)
}

// guardProvider wraps any provider with the DLP egress guard (secret redaction + canaries). Used for the
// active provider and for cross-provider routing resolution (ADR-0021).
func (s *Server) guardProvider(p llm.Provider) llm.Provider {
	external := !llm.IsLocal(p)
	load := func(ctx context.Context) (map[string]string, map[string]string) {
		var secrets map[string]string
		if s.vault != nil {
			secrets, _ = s.global().SecretValueMap(ctx, s.vault.Open)
		}
		canaries, _ := s.global().CanaryMap(ctx)
		return secrets, canaries
	}
	onHit := func(ctx context.Context, h dlp.Hit, blocked bool) {
		_ = s.global().RecordDLPEvent(ctx, model.DLPEvent{
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
	// A thread lives in a project's database (ADR-0049): a real-project thread is routed to and stamped
	// with that project; a project-less "ask" (the global assistant) is homed in the reserved global db
	// but keeps a NULL project_id because it genuinely has no project. So routing id and stamp differ.
	pid := projectFromReq(r)
	routing := threadProject(pid)
	th, err := s.pdbID(routing).CreateThread(r.Context(), store.NewThread{ProjectID: ptrIfSet(pid), Title: "ask", Provider: s.providerName()})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := s.analystService().Send(r.Context(), routing, th.ID, req.Message)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordUsage(r.Context(), res)
	s.notifyIfPending(r.Context(), res)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	ts, err := s.mgr.ListAllThreads(r.Context())
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
	// Route and stamp with one project id: an explicit body project_id wins, else the active-project
	// header, else the reserved global project. A thread lives in that project's database (ADR-0049).
	pid := projectFromReq(r)
	if req.ProjectID != nil && *req.ProjectID != "" {
		pid = *req.ProjectID
	}
	th, err := s.pdbID(threadProject(pid)).CreateThread(r.Context(), store.NewThread{ProjectID: ptrIfSet(pid), Title: req.Title, AgentType: req.AgentType, Provider: s.providerName()})
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
	Gate        bool     `json:"gate,omitempty"` // a human-approval pause (ADR-0044) — so the editor can round-trip gates
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

// builtinSteps converts analyst playbook steps to the API view shape.
func builtinSteps(in []analyst.PlaybookStep) []agentPlaybookStep {
	steps := make([]agentPlaybookStep, 0, len(in))
	for _, st := range in {
		steps = append(steps, agentPlaybookStep{Key: st.Key, Profile: st.Profile, Instruction: st.Instruction, DependsOn: st.DependsOn, Gate: st.Gate})
	}
	return steps
}

// listAgentPlaybooks returns the agent playbooks a human can trigger — built-ins plus user-saved ones
// (ADR-0019). Distinct from /v1/playbooks, which lists capability playbooks.
func (s *Server) listAgentPlaybooks(w http.ResponseWriter, r *http.Request) {
	out := []agentPlaybookView{}
	for _, p := range analyst.Playbooks() {
		out = append(out, agentPlaybookView{ID: p.ID, Name: p.Name, Description: p.Description, Goal: p.Goal, Steps: builtinSteps(p.Steps), Builtin: true})
	}
	if saved, err := s.global().ListSavedPlaybooks(r.Context()); err == nil {
		for _, sp := range saved {
			var steps []agentPlaybookStep
			_ = json.Unmarshal(sp.Steps, &steps)
			out = append(out, agentPlaybookView{ID: sp.ID, Name: sp.Name, Description: sp.Description, Goal: sp.Goal, Steps: steps, Builtin: false, Source: sp.Source})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"playbooks": out})
}

// getAgentPlaybook returns one agent playbook (built-in or saved) with its steps and gates, so the editor can
// load it for editing.
func (s *Server) getAgentPlaybook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if pb, ok := analyst.PlaybookByID(id); ok {
		writeJSON(w, http.StatusOK, agentPlaybookView{ID: pb.ID, Name: pb.Name, Description: pb.Description, Goal: pb.Goal, Steps: builtinSteps(pb.Steps), Builtin: true})
		return
	}
	sp, err := s.global().GetSavedPlaybook(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "playbook not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var steps []agentPlaybookStep
	_ = json.Unmarshal(sp.Steps, &steps)
	writeJSON(w, http.StatusOK, agentPlaybookView{ID: sp.ID, Name: sp.Name, Description: sp.Description, Goal: sp.Goal, Steps: steps, Builtin: false, Source: sp.Source})
}

// updateSavedPlaybook edits a saved playbook in place (ADR-0019). Built-ins are immutable.
func (s *Server) updateSavedPlaybook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Goal        string                 `json:"goal"`
		Steps       []analyst.PlaybookStep `json:"steps"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sp, err := s.analystService().UpdatePlaybook(r.Context(), r.PathValue("id"), req.Name, req.Description, req.Goal, req.Steps)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "saved playbook not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sp)
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
	err := s.global().DeleteSavedPlaybook(r.Context(), r.PathValue("id"))
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
	sp, err := s.analystService().SavePlaybookFromPlan(r.Context(), projectFromReq(r), r.PathValue("id"), req.Name, req.Description)
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
	sc, err := s.pdb(r).CreateSchedule(r.Context(), r.PathValue("id"), req.PlaybookID, req.IntervalSeconds, time.Now().UTC())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

// listSchedules returns a project's schedules.
func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	sched, err := s.pdb(r).ListSchedulesByProject(r.Context(), r.PathValue("id"))
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
	err := s.pdb(r).SetScheduleEnabled(r.Context(), r.PathValue("id"), req.Enabled)
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
	err := s.pdb(r).DeleteSchedule(r.Context(), r.PathValue("id"))
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
	plan, err := s.pdb(r).GetPlan(r.Context(), r.PathValue("id"))
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

// cancelPlan stops a running plan: aborts its in-flight LLM call and skips unfinished steps (ADR-0019).
func (s *Server) cancelPlan(w http.ResponseWriter, r *http.Request) {
	projectID := projectFromReq(r)
	if err := s.analystService().CancelPlan(r.Context(), projectID, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "plan.cancel", r.PathValue("id"), map[string]string{"project": projectID})
	plan, _ := s.pdbID(projectID).GetPlan(r.Context(), r.PathValue("id"))
	writeJSON(w, http.StatusOK, plan)
}

// resolvePlanGate approves or denies a plan's waiting approval gate and resumes the run (ADR-0044).
func (s *Server) resolvePlanGate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approve bool   `json:"approve"`
		Note    string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	plan, err := s.analystService().ResolvePlanGate(r.Context(), projectFromReq(r), r.PathValue("id"), r.PathValue("stepID"), req.Approve, req.Note)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "plan or gate step not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	decision := "approve"
	if !req.Approve {
		decision = "deny"
	}
	s.record(r.Context(), actorOf(r), "plan.gate."+decision, r.PathValue("stepID"), map[string]string{"plan": plan.ID})
	writeJSON(w, http.StatusOK, plan)
}

// listPlans returns a project's plans (without steps), newest first.
func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.pdb(r).ListPlansByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

// getApprovalPolicy returns the sensitive tools (approve-by-default) and the current override rules
// (ADR-0019 §5). The rules promote or demote a tool [+profile] between auto and approve.
func (s *Server) getApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	raw, _ := s.global().GetSetting(r.Context(), analyst.ApprovalPolicySetting)
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
	if err := s.global().SetSetting(r.Context(), analyst.ApprovalPolicySetting, string(b)); err != nil {
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
	if saved, err := s.global().ListSavedProfiles(r.Context()); err == nil {
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
// settingsSections is the full settings surface: core sections plus any declared by loaded extensions,
// each namespaced so extension values can never collide with core keys (ADR-0021 §5).
func (s *Server) settingsSections() []settings.Section {
	secs := settings.CoreSections()
	for _, ext := range s.exts {
		secs = append(secs, settings.Namespace(ext.Manifest.ID, ext.Manifest.Settings)...)
	}
	return secs
}

// getModelCatalog returns the curated model catalog (ADR-0021) that powers the model picker + tag defaults.
func (s *Server) getModelCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": catalog.Models()})
}

// getModelRouting returns the built-in tag vocabulary + the current tag → ordered (connection, model)
// lists (ADR-0052), normalized through the legacy shape.
func (s *Server) getModelRouting(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tags":    analyst.RoutingTags(),
		"routing": s.analystService().Routing(r.Context()),
	})
}

// setModelRouting stores the routing map (a tag → {provider_id, model} object plus a default).
func (s *Server) setModelRouting(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Routing json.RawMessage `json:"routing"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Routing) == 0 || !json.Valid(req.Routing) {
		writeErr(w, http.StatusBadRequest, "routing must be a JSON object")
		return
	}
	if err := s.global().SetSetting(r.Context(), analyst.ModelRoutingSetting, string(req.Routing)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getSettings returns the declarative section schemas plus current values (defaults applied).
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	sections := s.settingsSections()
	values := map[string]string{}
	for _, sec := range sections {
		for _, f := range sec.Fields {
			v, err := s.global().GetSetting(r.Context(), f.Key)
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
		if err := s.global().SetSetting(r.Context(), key, val); err != nil {
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
		ModelTag    string   `json:"model_tag"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sp, err := s.analystService().SaveProfile(r.Context(), req.Name, req.Description, req.Persona, req.Tools, req.ModelTag)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

// deleteSavedProfile removes a user-defined profile (built-ins can't be deleted).
func (s *Server) deleteSavedProfile(w http.ResponseWriter, r *http.Request) {
	err := s.global().DeleteSavedProfile(r.Context(), r.PathValue("id"))
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
	th, err := s.pdb(r).GetThread(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "thread not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	msgs, err := s.pdb(r).ListMessages(r.Context(), th.ID)
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
	res, err := s.analystService().Send(r.Context(), projectFromReq(r), r.PathValue("id"), req.Message)
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
	// Attribute to the backend that actually ran (which, under cross-provider routing, may differ from
	// the active provider). The Service leaves these blank when the active provider ran — fill them here.
	provider, modelName := res.Provider, res.Model
	if provider == "" {
		provider = info.Type
		if modelName == "" {
			modelName = info.Model
		}
	}
	projectID := ""
	if res.Thread.ProjectID != nil {
		projectID = *res.Thread.ProjectID
	}
	_ = s.pdbID(projectID).RecordUsage(ctx, model.UsageRecord{
		ProjectID: projectID, ThreadID: res.Thread.ID, AgentType: res.AgentType,
		Provider: provider, Model: modelName,
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
	child, err := s.pdb(r).ForkThread(r.Context(), r.PathValue("id"), req.Seq)
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

// getHome is the cross-project "mission control" cockpit (plan §Global Home): what's waiting on you,
// what's running now, and each project's status at a glance.
func (s *Server) getHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	projects, _ := s.mgr.ListProjects(ctx)
	name := map[string]string{}
	for _, p := range projects {
		name[p.ID] = p.Name
	}

	// Waiting on you: pending approvals, resolved to their project.
	type apView struct {
		ID        string    `json:"id"`
		Tool      string    `json:"tool"`
		ThreadID  string    `json:"thread_id"`
		ProjectID string    `json:"project_id,omitempty"`
		Project   string    `json:"project,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}
	approvals := []apView{}
	for _, a := range must(s.mgr.ListAllPendingApprovals(ctx)) {
		v := apView{ID: a.ID, Tool: a.Tool, ThreadID: a.ThreadID, CreatedAt: a.CreatedAt}
		if th, err := s.pdb(r).GetThread(ctx, a.ThreadID); err == nil && th.ProjectID != nil {
			v.ProjectID = *th.ProjectID
			v.Project = name[*th.ProjectID]
		}
		approvals = append(approvals, v)
	}

	// Running now: in-flight (running) and queued (pending) capability tasks + active/awaiting threads.
	type taskView struct {
		ID         string `json:"id"`
		Capability string `json:"capability"`
		Status     string `json:"status"`
		ProjectID  string `json:"project_id,omitempty"`
		Project    string `json:"project,omitempty"`
	}
	runningTasks := []taskView{}
	for _, t := range must(s.mgr.ListAllTasks(ctx, 200)) {
		if t.Status != model.TaskRunning && t.Status != model.TaskPending {
			continue
		}
		tv := taskView{ID: t.ID, Capability: t.CapabilityID, Status: t.Status}
		if t.ApplicationID != nil {
			if app, err := s.pdb(r).GetApplication(ctx, *t.ApplicationID); err == nil {
				tv.ProjectID = app.ProjectID
				tv.Project = name[app.ProjectID]
			}
		}
		runningTasks = append(runningTasks, tv)
	}
	type thView struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		AgentType string `json:"agent_type"`
		ProjectID string `json:"project_id,omitempty"`
		Project   string `json:"project,omitempty"`
	}
	threads := []thView{}
	for _, th := range must(s.mgr.ListAllThreads(ctx)) {
		if th.Status != model.ThreadActive && th.Status != model.ThreadAwaitingApproval {
			continue
		}
		tv := thView{ID: th.ID, Title: th.Title, Status: th.Status, AgentType: th.AgentType}
		if th.ProjectID != nil {
			tv.ProjectID = *th.ProjectID
			tv.Project = name[*th.ProjectID]
		}
		threads = append(threads, tv)
	}

	// Projects at a glance.
	fcounts, _ := s.pdb(r).FindingCountsByProject(ctx)
	type projView struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Findings   int    `json:"findings"`
		High       int    `json:"high"`
		ToTriage   int    `json:"to_triage"`
		Adopted    int    `json:"adopted"`     // methodology items in adopted packs (0 = no coverage ring)
		CoveredPct int    `json:"covered_pct"` // covered / applicable, 0..100
	}
	pvs := []projView{}
	for _, p := range projects {
		fc := fcounts[p.ID]
		toTriage := 0
		if obs, err := s.pdb(r).ListObservationsByProject(ctx, p.ID); err == nil {
			for _, o := range obs {
				if o.ReviewState == model.ReviewUnreviewed {
					toTriage++
				}
			}
		}
		pv := projView{ID: p.ID, Name: p.Name, Status: p.Status, Findings: fc.Total, High: fc.High, ToTriage: toTriage}
		if cov, ok := s.projectCoverage(ctx, p.ID); ok {
			pv.Adopted = cov.Total
			pv.CoveredPct = cov.CoveredPct
		}
		pvs = append(pvs, pv)
	}

	// Spend: workbench-wide token usage this month + all-time (informational, no cap).
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	usage, _ := s.pdb(r).UsageSummary(ctx, monthStart, 6)

	writeJSON(w, http.StatusOK, map[string]any{
		"approvals": approvals,
		"active":    map[string]any{"tasks": runningTasks, "threads": threads},
		"projects":  pvs,
		"usage":     usage,
		"schedules": s.scheduleViews(ctx, projects, name),
	})
}

// projectCoverage rolls up a project's methodology coverage for the cockpit ring. ok is false when
// the project has adopted no packs (nothing to show a ring for).
func (s *Server) projectCoverage(ctx context.Context, projectID string) (methodology.Summary, bool) {
	adopted, err := s.pdbID(projectID).ListAdoptedMethodologies(ctx, projectID)
	if err != nil || len(adopted) == 0 {
		return methodology.Summary{}, false
	}
	entries, err := s.pdbID(projectID).ListCoverage(ctx, projectID)
	if err != nil {
		return methodology.Summary{}, false
	}
	states := make(map[string]methodology.State, len(entries))
	for _, e := range entries {
		states[e.ItemID] = methodology.State{Status: e.Status, Note: e.Note}
	}
	sum := methodology.BuildCoverage(s.methods, adopted, states).Summary
	return sum, sum.Total > 0
}

// scheduleViews lists every project's schedules (triggers/watchers) for the cockpit, resolving the
// playbook display name and owning project. Soonest next-run first.
func (s *Server) scheduleViews(ctx context.Context, projects []model.Project, name map[string]string) []map[string]any {
	type schedView struct {
		ID              string     `json:"id"`
		ProjectID       string     `json:"project_id"`
		Project         string     `json:"project,omitempty"`
		PlaybookID      string     `json:"playbook_id"`
		Playbook        string     `json:"playbook"`
		IntervalSeconds int        `json:"interval_seconds"`
		Enabled         bool       `json:"enabled"`
		NextRunAt       time.Time  `json:"next_run_at"`
		LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	}
	out := []schedView{}
	for _, p := range projects {
		for _, sc := range must(s.pdbID(p.ID).ListSchedulesByProject(ctx, p.ID)) {
			pbName := sc.PlaybookID
			if pb, ok := analyst.PlaybookByID(sc.PlaybookID); ok {
				pbName = pb.Name
			} else if sp, err := s.global().GetSavedPlaybook(ctx, sc.PlaybookID); err == nil {
				pbName = sp.Name
			}
			out = append(out, schedView{
				ID: sc.ID, ProjectID: sc.ProjectID, Project: name[sc.ProjectID],
				PlaybookID: sc.PlaybookID, Playbook: pbName, IntervalSeconds: sc.IntervalSeconds,
				Enabled: sc.Enabled, NextRunAt: sc.NextRunAt, LastRunAt: sc.LastRunAt,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(out[j].NextRunAt) })
	// Return as []map so the empty case still marshals to [] (not null) for the frontend.
	views := make([]map[string]any, 0, len(out))
	for _, v := range out {
		views = append(views, map[string]any{
			"id": v.ID, "project_id": v.ProjectID, "project": v.Project,
			"playbook_id": v.PlaybookID, "playbook": v.Playbook, "interval_seconds": v.IntervalSeconds,
			"enabled": v.Enabled, "next_run_at": v.NextRunAt, "last_run_at": v.LastRunAt,
		})
	}
	return views
}

// must returns the slice or nil (dashboard reads tolerate an empty section rather than failing the page).
func must[T any](v []T, _ error) []T { return v }

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	aps, err := s.mgr.ListAllPendingApprovals(r.Context())
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
	res, err := s.analystService().Decide(r.Context(), projectFromReq(r), approvalID, req.Decision)
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
	if s.mgr == nil || s.global().PingContext(r.Context()) != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- organizations ---

func (s *Server) listOrganizations(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.global().ListOrganizations(r.Context())
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
	org, err := s.global().CreateOrganization(r.Context(), req.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

// --- targets ---

func (s *Server) listTargets(w http.ResponseWriter, r *http.Request) {
	targets, err := s.global().ListTargets(r.Context())
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
	target, err := s.global().CreateTarget(r.Context(), req.Name, req.Description, req.OrganizationID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, target)
}

// --- projects ---

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.mgr.ListProjects(r.Context())
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
		// Optional engagement record + scope, so the setup modal creates a project with its properties in
		// one call (ADR-0051). Applied to the new project's database after creation; best-effort so a
		// property error never orphans the project — the modal can reconcile via PUT .../engagement.
		Engagement *model.Engagement `json:"engagement"`
		Scope      []struct {
			Kind        string `json:"kind"`
			Value       string `json:"value"`
			Disposition string `json:"disposition"`
		} `json:"scope"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	// Validate references up front so a bad target/org id is a clear 400, not a 500 from a downstream
	// constraint violation.
	for _, tid := range req.TargetIDs {
		if _, err := s.global().GetTarget(r.Context(), tid); err != nil {
			writeErr(w, http.StatusBadRequest, "unknown target: "+tid)
			return
		}
	}
	project, err := s.mgr.CreateProject(r.Context(), store.NewProject{
		Name:           req.Name,
		OrganizationID: req.OrganizationID,
		GroupID:        req.GroupID,
		TargetIDs:      req.TargetIDs,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	pdb := s.pdbID(project.ID)
	if req.Engagement != nil {
		req.Engagement.ProjectID = project.ID
		if _, err := pdb.SetEngagement(r.Context(), *req.Engagement); err != nil {
			log.Printf("createProject: engagement for %s not saved: %v", project.ID, err)
		}
	}
	for _, sc := range req.Scope {
		if sc.Value == "" {
			continue
		}
		if _, err := pdb.AddScopeEntry(r.Context(), project.ID, sc.Kind, sc.Value, sc.Disposition); err != nil {
			log.Printf("createProject: scope %q for %s not saved: %v", sc.Value, project.ID, err)
		}
	}
	writeJSON(w, http.StatusCreated, project)
}

// getEngagement returns a project's engagement record, or an empty one (with just the project id) when none
// has been configured, so the setup/settings editor always has something to bind to.
func (s *Server) getEngagement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	eng, err := s.pdb(r).GetEngagement(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, model.Engagement{ProjectID: id})
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, eng)
}

// setEngagement upserts a project's engagement record (the whole form).
func (s *Server) setEngagement(w http.ResponseWriter, r *http.Request) {
	var eng model.Engagement
	if !decodeJSON(w, r, &eng) {
		return
	}
	eng.ProjectID = r.PathValue("id")
	saved, err := s.pdb(r).SetEngagement(r.Context(), eng)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "engagement.set", eng.ProjectID, map[string]string{"project": eng.ProjectID})
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.mgr.GetProject(r.Context(), r.PathValue("id"))
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
	err := s.mgr.DeleteProject(r.Context(), r.PathValue("id"))
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
	results, err := s.pdb(r).Search(r.Context(), r.URL.Query().Get("q"), 25)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// codeHit is one source-content search result — where a string was found inside a repo.
type codeHit struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Text    string `json:"text"`
}

// searchCode greps the project's source_repo assets for a literal string — the content tier of search, so
// the search bar finds text inside files, not just names/metadata. Bounded across assets so it stays snappy.
func (s *Server) searchCode(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	out := []codeHit{}
	if strings.TrimSpace(q) == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	assets, err := s.pdb(r).ListAssets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	const maxTotal = 60
	for _, a := range assets {
		if a.Type != model.AssetSourceRepo || len(out) >= maxTotal {
			continue
		}
		for _, m := range srcfile.Grep(a.Location, q, maxTotal-len(out), 2<<20) {
			out = append(out, codeHit{AssetID: a.ID, Path: m.Path, Line: m.Line, Text: m.Text})
		}
	}
	writeJSON(w, http.StatusOK, out)
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

	proj, err := s.mgr.CreateProject(r.Context(), store.NewProject{Name: req.Name})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]any{"project": proj, "template": tmpl}
	if tmpl.DefaultApplication != "" {
		app, err := s.pdb(r).CreateApplication(r.Context(), proj.ID, tmpl.DefaultApplication)
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
	apps, err := s.pdb(r).ListApplicationsByProject(r.Context(), r.PathValue("id"))
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
	app, err := s.pdb(r).CreateApplication(r.Context(), r.PathValue("id"), req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	app, err := s.pdb(r).GetApplication(r.Context(), r.PathValue("id"))
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
	assets, err := s.pdb(r).ListAssetsByApplication(r.Context(), r.PathValue("id"))
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
	location := s.resolveAssetLocation(r, r.PathValue("id"), req.Location)
	asset, err := s.pdb(r).CreateAsset(r.Context(), store.NewAsset{
		ApplicationID: r.PathValue("id"),
		Type:          req.Type,
		Location:      location,
		Sensitivity:   req.Sensitivity,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, asset)
}

// resolveAssetLocation anchors a relative filesystem location under the project's engagement base path
// (ADR-0051), so an operator who set a base folder can add assets by relative path. URLs and already-absolute
// paths pass through unchanged, as does anything when no base path is set.
func (s *Server) resolveAssetLocation(r *http.Request, appID, loc string) string {
	if loc == "" || strings.Contains(loc, "://") || filepath.IsAbs(loc) {
		return loc
	}
	app, err := s.pdb(r).GetApplication(r.Context(), appID)
	if err != nil {
		return loc
	}
	eng, err := s.pdb(r).GetEngagement(r.Context(), app.ProjectID)
	if err != nil || eng.BasePath == "" {
		return loc
	}
	return filepath.Join(eng.BasePath, loc)
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request) {
	asset, err := s.pdb(r).GetAsset(r.Context(), r.PathValue("id"))
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

// updateAsset edits an existing asset's sensitivity in place (ADR-0011: sensitivity gates external
// egress, so an operator must be able to correct it after create without deleting and re-adding).
func (s *Server) updateAsset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sensitivity string `json:"sensitivity"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.pdb(r).UpdateAssetSensitivity(r.Context(), r.PathValue("id"), req.Sensitivity)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// getAssetEcosystems returns what the scanner auto-detects in the asset's repo (from marker files) and the
// operator's manual tags — the two inputs to the scan auto-run gate, so the UI can show both.
func (s *Server) getAssetEcosystems(w http.ResponseWriter, r *http.Request) {
	asset, err := s.pdb(r).GetAsset(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"detected": capability.DetectEcosystemList(asset.Location),
		"tags":     asset.Ecosystems,
	})
}

// setAssetEcosystems replaces an asset's manual ecosystem tags — the operator override that the scan gate
// unions with detection (e.g. to run a Python tool on a polyglot repo detection under-read).
func (s *Server) setAssetEcosystems(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ecosystems []string `json:"ecosystems"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	asset, err := s.pdb(r).SetAssetEcosystems(r.Context(), r.PathValue("id"), req.Ecosystems)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, asset)
}

// maxSourceViewBytes caps a single source-file response. Larger than the analyst read window because a
// human viewer wants whole files, but still bounded so a pathological file can't be streamed unbounded.
const maxSourceViewBytes = 1 << 20 // 1 MiB

// sourceAsset loads a source_repo asset from the request's project DB and verifies it is readable on disk.
// Project confinement is inherent — s.pdb(r) is the per-project database (ADR-0049), so an asset id from
// another project simply is not found here.
func (s *Server) sourceAsset(w http.ResponseWriter, r *http.Request) (model.Asset, bool) {
	asset, err := s.pdb(r).GetAsset(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "asset not found")
		return model.Asset{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return model.Asset{}, false
	}
	if asset.Type != model.AssetSourceRepo {
		writeErr(w, http.StatusBadRequest, "asset is not a source_repo")
		return model.Asset{}, false
	}
	if asset.Location == "" {
		writeErr(w, http.StatusBadRequest, "asset has no location on disk")
		return model.Asset{}, false
	}
	return asset, true
}

// getAssetSource serves one source file's contents from a source_repo asset, path-confined to the asset
// root. It backs the in-app code viewer and click-to-file jumps.
func (s *Server) getAssetSource(w http.ResponseWriter, r *http.Request) {
	asset, ok := s.sourceAsset(w, r)
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	file, err := srcfile.ReadFile(asset.Location, path, maxSourceViewBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "file not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, file)
}

// getAssetTree lists one directory of a source_repo asset (default: the root), for the source browser.
func (s *Server) getAssetTree(w http.ResponseWriter, r *http.Request) {
	asset, ok := s.sourceAsset(w, r)
	if !ok {
		return
	}
	entries, err := srcfile.ListDir(asset.Location, r.URL.Query().Get("path"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "directory not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// --- context items ---

func (s *Server) listContext(w http.ResponseWriter, r *http.Request) {
	items, err := s.pdb(r).ListContextItemsByProject(r.Context(), r.PathValue("id"))
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
	// Analyst labels (ADR-0015): free-form tags + a pin flag, both optional. A body-only "note" is just this
	// endpoint with type=note and the text as the request body — no separate route needed.
	var tags []string
	for _, t := range strings.Split(r.URL.Query().Get("tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	pinned := r.URL.Query().Get("pinned") == "true"

	r.Body = http.MaxBytesReader(w, r.Body, 64<<20) // 64 MiB cap
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	digest, err := s.casFor(projectFromReq(r)).Put(bytes.NewReader(data))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	mediaType := r.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	art, err := s.pdb(r).CreateArtifact(r.Context(), model.Artifact{
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
	ci, err := s.pdb(r).CreateContextItem(r.Context(), model.ContextItem{
		ProjectID:  r.PathValue("id"),
		Type:       ctype,
		Name:       name,
		ArtifactID: art.ID,
		Tags:       tags,
		Pinned:     pinned,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Index the ingested doc for semantic retrieval (ADR-0039). Best-effort — never fails the ingest.
	if ix := s.analystService().Indexer(); ix != nil && ix.Available() {
		_ = ix.IndexContextItem(r.Context(), ci.ProjectID, ci.ID)
	}
	writeJSON(w, http.StatusCreated, ci)
}

// updateContext edits a context item's mutable fields. Any provided field replaces the current value; a
// `body` (notes only) is re-stored in the CAS as a fresh artifact the item then points at. Metadata-only
// edits (name/tags/pinned) keep the existing artifact. Re-indexes the item best-effort.
func (s *Server) updateContext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cur, err := s.pdb(r).GetContextItem(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "context item not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req struct {
		Name   *string   `json:"name"`
		Tags   *[]string `json:"tags"`
		Pinned *bool     `json:"pinned"`
		Body   *string   `json:"body"` // notes only: the new note text
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	name, tags, pinned, artifactID := cur.Name, cur.Tags, cur.Pinned, cur.ArtifactID
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if req.Tags != nil {
		tags = nil
		for _, t := range *req.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	if req.Pinned != nil {
		pinned = *req.Pinned
	}
	// A note's body is its artifact: re-store the edited text and repoint the item at the new blob.
	if req.Body != nil {
		if cur.Type != model.ContextNote {
			writeErr(w, http.StatusBadRequest, "only notes have an editable body")
			return
		}
		data := []byte(*req.Body)
		digest, err := s.casFor(projectFromReq(r)).Put(bytes.NewReader(data))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		art, err := s.pdb(r).CreateArtifact(r.Context(), model.Artifact{
			SHA256:    digest,
			Size:      int64(len(data)),
			Kind:      model.ArtifactInput,
			Name:      name,
			MediaType: "text/plain",
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		artifactID = art.ID
	}

	ci, err := s.pdb(r).UpdateContextItem(r.Context(), id, name, tags, pinned, artifactID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "context item not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ix := s.analystService().Indexer(); ix != nil && ix.Available() {
		_ = ix.IndexContextItem(r.Context(), ci.ProjectID, ci.ID)
	}
	s.record(r.Context(), actorOf(r), "context.update", ci.ID, nil)
	writeJSON(w, http.StatusOK, ci)
}

// deleteContext removes a context item and its semantic-index chunks. The CAS blob is left in place.
func (s *Server) deleteContext(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ci, err := s.pdb(r).GetContextItem(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "context item not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.pdb(r).DeleteContextItem(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Drop its retrieval chunks so a deleted note can't resurface in semantic search. Best-effort.
	_ = s.pdb(r).DeleteChunksForSource(r.Context(), ci.ProjectID, "context", id)
	s.record(r.Context(), actorOf(r), "context.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- scope allowlist (P6) ---

func (s *Server) listScope(w http.ResponseWriter, r *http.Request) {
	entries, err := s.pdb(r).ListScopeEntries(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) addScope(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind        string `json:"kind"`
		Value       string `json:"value"`
		Disposition string `json:"disposition"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	entry, err := s.pdb(r).AddScopeEntry(r.Context(), r.PathValue("id"), req.Kind, req.Value, req.Disposition)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "scope.add", entry.ID, map[string]string{
		"project": entry.ProjectID, "kind": entry.Kind, "value": entry.Value, "disposition": entry.Disposition,
	})
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) deleteScope(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.pdb(r).DeleteScopeEntry(r.Context(), id)
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
	events, err := s.global().ListAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// verifyAudit recomputes the audit hash chain and reports whether it is intact (tamper detection).
func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	ok, broken, count, err := s.global().VerifyAuditChain(r.Context())
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
	items, err := s.pdb(r).ListExchangesFiltered(r.Context(), r.PathValue("id"), f)
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
	entries, _ := s.pdbID(projectID).ListScopeEntries(ctx, projectID)
	rules := make([]scope.Entry, 0, len(entries))
	for _, e := range entries {
		rules = append(rules, scope.Entry{Kind: e.Kind, Value: e.Value, Disposition: e.Disposition})
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
	ex, err := s.pdb(r).CreateExchange(r.Context(), model.HTTPExchange{
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
	ex, err := s.pdb(r).GetExchange(r.Context(), r.PathValue("id"))
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

// sendExchange scope-guards the target, issues the request (optionally from a chosen remote runner's
// vantage, ADR-0025), and records the response (ADR-0007).
func (s *Server) sendExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RunnerID string `json:"runner_id"`
	}
	if err := decodeJSONOptional(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	ex, err := s.pdb(r).GetExchange(r.Context(), r.PathValue("id"))
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
	entries, err := s.pdb(r).ListScopeEntries(r.Context(), ex.ProjectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(entries) > 0 {
		rules := make([]scope.Entry, len(entries))
		for i, en := range entries {
			rules[i] = scope.Entry{Kind: en.Kind, Value: en.Value, Disposition: en.Disposition}
		}
		if serr := scope.Check(rules, ex.URL); serr != nil {
			s.record(r.Context(), actorOf(r), "replay.blocked", ex.ID, map[string]string{"url": ex.URL, "reason": serr.Error()})
			writeErr(w, http.StatusForbidden, "blocked by scope guard: "+serr.Error())
			return
		}
	}

	resp, err := s.egressSend(r.Context(), req.RunnerID, replay.Request{
		Method: ex.Method, URL: ex.URL, Headers: ex.RequestHeaders, Body: ex.RequestBody,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "send failed: "+err.Error())
		return
	}
	if err := s.pdb(r).RecordResponse(r.Context(), ex.ID, resp.Status, resp.Headers, resp.Body, resp.DurationMS, req.RunnerID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "replay.send", ex.ID, map[string]any{
		"method": ex.Method, "url": ex.URL, "status": resp.Status, "egress": egressLabel(req.RunnerID),
	})
	updated, _ := s.pdb(r).GetExchange(r.Context(), ex.ID)
	s.events.Publish(events.Event{Type: "exchange", ProjectID: ex.ProjectID, Payload: updated})
	writeJSON(w, http.StatusOK, updated)
}

// egressSend issues an HTTP request either from the control-plane host (runnerID == "") or, when a runner
// is chosen, from that enrolled runner's network vantage over the runner protocol (ADR-0025). The caller
// enforces scope first; this is a pure transport selector. A chosen runner that is revoked or offline is
// an error — there is no silent fallback to the local host.
func (s *Server) egressSend(ctx context.Context, runnerID string, req replay.Request) (replay.Response, error) {
	if runnerID == "" {
		return s.replay.Send(ctx, req)
	}
	rn, err := s.global().GetRunner(ctx, runnerID)
	if err != nil {
		return replay.Response{}, fmt.Errorf("runner %s: %w", runnerID, err)
	}
	if rn.Status != model.RunnerActive {
		return replay.Response{}, fmt.Errorf("runner %s is %s", rn.Name, rn.Status)
	}
	reqID := uuid.NewString()
	ch, err := s.runners.DispatchHTTP(runnerID, runnerhub.HTTPRequest{
		ID: reqID, Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body,
	})
	if err != nil {
		return replay.Response{}, fmt.Errorf("runner %s: %w", rn.Name, err)
	}
	select {
	case res := <-ch:
		if res.Error != "" {
			return replay.Response{}, fmt.Errorf("runner %s: %s", rn.Name, res.Error)
		}
		return replay.Response{Status: res.Status, Headers: res.Headers, Body: res.Body, DurationMS: int(res.DurationMs)}, nil
	case <-ctx.Done():
		s.runners.ForgetHTTP(reqID)
		return replay.Response{}, ctx.Err()
	}
}

// egressLabel is the audit label for where a send went out: "local" or the runner id.
func egressLabel(runnerID string) string {
	if runnerID == "" {
		return "local"
	}
	return runnerID
}

// exchangeEvidence promotes a sent exchange's response into the CAS as an artifact and records a
// human-origin observation (ADR-0005), so it enters the same triage → finding path as tool output.
func (s *Server) exchangeEvidence(w http.ResponseWriter, r *http.Request) {
	ex, err := s.pdb(r).GetExchange(r.Context(), r.PathValue("id"))
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
	digest, err := s.casFor(projectFromReq(r)).Put(bytes.NewReader([]byte(blob)))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	art, err := s.pdb(r).CreateArtifact(r.Context(), model.Artifact{
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
	obs, err := s.pdb(r).CreateObservation(r.Context(), model.Observation{
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
		if err := s.pdb(r).LinkCoverageObservation(r.Context(), ex.ProjectID, req.ItemID, obs.ID); err != nil {
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
		RunnerID      string            `json:"runner_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.CapabilityID == "" {
		writeErr(w, http.StatusBadRequest, "capability_id is required")
		return
	}
	// Enqueue and return immediately (ADR-0022): the run executes on the worker pool and the client
	// polls GET /v1/tasks/{id}. A validation/plan error fails fast here with no task created.
	t, err := s.engine.Enqueue(r.Context(), task.RunRequest{
		CapabilityID:  req.CapabilityID,
		TargetDir:     req.TargetDir,
		Actor:         req.Actor,
		AssetID:       req.AssetID,
		ApplicationID: req.ApplicationID,
		ProjectID:     req.ProjectID,
		SecretRefs:    req.SecretRefs,
		Params:        req.Params,
		RunnerID:      req.RunnerID,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "task.enqueue", t.ID, map[string]any{
		"capability": req.CapabilityID, "status": t.Status,
	})
	writeJSON(w, http.StatusAccepted, t)
}

// scanProject fans out every applicable capability across the project's assets — the deterministic
// "scan everything" action. Each enqueued task runs on the worker pool and auto-triages on completion;
// the client polls GET /v1/tasks. No agent is involved.
func (s *Server) scanProject(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "task engine not available")
		return
	}
	res, err := s.engine.ScanProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "project.scan", r.PathValue("id"), map[string]any{
		"enqueued": len(res.Enqueued), "skipped": len(res.Skipped),
	})
	writeJSON(w, http.StatusAccepted, res)
}

// reevaluateProject re-runs correlation + disposition over the project's existing observations, so
// findings recorded before the data that makes them exploitable (a route, a reachability verdict) are
// upgraded retroactively. Normally fires automatically when the relevant scans finish; this is the manual
// trigger.
func (s *Server) reevaluateProject(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "task engine not available")
		return
	}
	s.engine.ReEvaluate(r.Context(), r.PathValue("id"))
	s.record(r.Context(), actorOf(r), "project.reevaluate", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

// activity returns everything currently in flight across the workbench — running/pending capability tasks
// and in-flight agent plans — for the top-bar activity menu.
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	tasks, _ := s.mgr.ListAllTasks(r.Context(), 100)
	running := make([]model.Task, 0)
	for _, t := range tasks {
		if t.Status == model.TaskPending || t.Status == model.TaskRunning {
			running = append(running, t)
		}
	}
	plans, _ := s.mgr.ListAllRunningPlans(r.Context())
	agents := s.runReg.List()
	writeJSON(w, http.StatusOK, map[string]any{"tasks": running, "plans": plans, "agents": agents})
}

// cancelAgentRun stops an in-flight background agent run (e.g. batch triage) by id.
func (s *Server) cancelAgentRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runReg.Cancel(id) {
		writeErr(w, http.StatusNotFound, "no such running agent")
		return
	}
	s.record(r.Context(), "human", "analyst.agent.cancelled", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.mgr.ListAllTasks(r.Context(), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// activityItem is one row in the unified Activity feed: a scanner task, an agent thread, an agent plan,
// or a playbook run — all reduced to a common shape so the UI can interleave them newest-first. Kind
// tells the client which detail endpoint to open (task/thread/plan/playbook).
type activityItem struct {
	Kind      string    `json:"kind"` // task | thread | plan | playbook
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle,omitempty"`
	Status    string    `json:"status"`
	Actor     string    `json:"actor,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	Project   string    `json:"project,omitempty"`
	Timestamp time.Time `json:"timestamp"` // most-recent activity time, used for the newest-first sort
}

// getActivity merges every kind of run — scanner tasks, agent threads, agent plans, and playbook runs —
// into one newest-first timeline across all projects. This is the durable "what ran" surface: an agent
// conversation persisted as a thread stays here after a restart, so nothing an agent did is lost from view.
func (s *Server) getActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	// project, when set, scopes the feed to one project's runs (the workbench passes it). Without it the
	// feed spans every project. Scoping also keeps flat-route detail fetches (a thread/task detail resolves
	// via the active project's DB, ADR-0049) from hitting a "not found" for another project's item.
	projectFilter := r.URL.Query().Get("project")

	projects, _ := s.mgr.ListProjects(ctx)
	name := map[string]string{}
	for _, p := range projects {
		name[p.ID] = p.Name
	}
	// Resolve a task's owning project via its explicit ProjectID or its application (matches getHome).
	taskProject := func(t model.Task) string {
		if t.ProjectID != nil {
			return *t.ProjectID
		}
		if t.ApplicationID != nil {
			if app, err := s.pdb(r).GetApplication(ctx, *t.ApplicationID); err == nil {
				return app.ProjectID
			}
		}
		return ""
	}
	// latest picks the most recent meaningful time for sorting: a terminal time if present, else the start.
	latest := func(times ...*time.Time) time.Time {
		var best time.Time
		for _, t := range times {
			if t != nil && t.After(best) {
				best = *t
			}
		}
		return best
	}

	var items []activityItem
	withProject := func(it *activityItem, pid string) {
		if pid != "" {
			it.ProjectID = pid
			it.Project = name[pid]
		}
	}

	for _, t := range must(s.mgr.ListAllTasks(ctx, limit)) {
		it := activityItem{
			Kind: "task", ID: t.ID, Title: t.CapabilityID, Subtitle: t.Runner,
			Status: t.Status, Actor: t.Actor, Timestamp: latest(&t.CreatedAt, t.StartedAt, t.FinishedAt),
		}
		withProject(&it, taskProject(t))
		items = append(items, it)
	}
	for _, th := range must(s.mgr.ListAllThreads(ctx)) {
		it := activityItem{
			Kind: "thread", ID: th.ID, Title: th.Title, Subtitle: th.AgentType,
			Status: th.Status, Timestamp: latest(&th.CreatedAt, &th.UpdatedAt),
		}
		if th.ProjectID != nil {
			withProject(&it, *th.ProjectID)
		}
		items = append(items, it)
	}
	for _, p := range must(s.mgr.ListAllPlans(ctx, limit)) {
		title := p.Goal
		if title == "" {
			title = p.PlaybookID
		}
		it := activityItem{
			Kind: "plan", ID: p.ID, Title: title, Subtitle: "plan",
			Status: p.Status, Timestamp: latest(&p.CreatedAt, &p.UpdatedAt),
		}
		withProject(&it, p.ProjectID)
		items = append(items, it)
	}
	for _, pr := range must(s.mgr.ListAllPlaybookRuns(ctx, limit)) {
		it := activityItem{
			Kind: "playbook", ID: pr.ID, Title: pr.PlaybookID, Subtitle: "playbook",
			Status: pr.Status, Actor: pr.Actor, Timestamp: latest(&pr.CreatedAt, pr.FinishedAt),
		}
		// A playbook run links to a project only via its asset; resolve it against the scoped project's DB
		// when filtering (so it isn't silently dropped from a project's feed).
		if projectFilter != "" && pr.AssetID != nil {
			if a, err := s.pdbID(projectFilter).GetAsset(ctx, *pr.AssetID); err == nil {
				if app, err := s.pdbID(projectFilter).GetApplication(ctx, a.ApplicationID); err == nil {
					withProject(&it, app.ProjectID)
				}
			}
		}
		items = append(items, it)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Timestamp.After(items[j].Timestamp) })
	if projectFilter != "" {
		kept := items[:0]
		for _, it := range items {
			if it.ProjectID == projectFilter {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.pdb(r).GetTask(r.Context(), r.PathValue("id"))
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
	arts, err := s.pdb(r).ListArtifactsByTask(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, arts)
}

func (s *Server) getArtifactContent(w http.ResponseWriter, r *http.Request) {
	art, err := s.pdb(r).GetArtifact(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rc, err := s.casFor(projectFromReq(r)).Open(art.SHA256)
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
	// Enqueue the playbook and return immediately (ADR-0022); steps run in the background on the task
	// engine and the client polls GET /v1/playbook-runs/{id}. A bad playbook fails fast with no run.
	run, err := playbook.NewRunner(s.engine, s.mgr).Start(r.Context(), projectFromReq(r), playbookID, req.AssetID, actor)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actor, "playbook.start", playbookID, map[string]any{
		"asset": req.AssetID, "run": run.ID,
	})
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) listPlaybookRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.mgr.ListAllPlaybookRuns(r.Context(), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) getPlaybookRun(w http.ResponseWriter, r *http.Request) {
	pr, err := s.pdb(r).GetPlaybookRun(r.Context(), r.PathValue("id"))
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
	obs, err := s.pdb(r).ListObservationsByTask(r.Context(), r.PathValue("id"))
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
	err := s.pdb(r).ReviewObservation(r.Context(), r.PathValue("id"), req.State)
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
	findings, err := s.mgr.ListAllFindings(r.Context())
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
	f, err := s.pdb(r).CreateFinding(r.Context(), store.NewFinding{
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
	f, err := s.pdb(r).GetFinding(r.Context(), r.PathValue("id"))
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

// validFindingStatus is the set a finding may transition to (model constants).
var validFindingStatus = map[string]bool{
	model.FindingOpen: true, model.FindingConfirmed: true, model.FindingRemediated: true,
	model.FindingAccepted: true, model.FindingFalsePositive: true,
}

// setFindingStatus advances a finding through its lifecycle (open → confirmed → remediated / accepted /
// false_positive). Findings were write-once before this — the store method existed but had no route.
func (s *Server) setFindingStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validFindingStatus[req.Status] {
		writeErr(w, http.StatusBadRequest, "invalid status (open|confirmed|remediated|accepted|false_positive)")
		return
	}
	id := r.PathValue("id")
	if err := s.pdb(r).SetFindingStatus(r.Context(), id, req.Status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "finding not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "finding.status", id, map[string]string{"status": req.Status})
	f, err := s.pdb(r).GetFinding(r.Context(), id)
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
	if _, err := s.global().AppendAudit(ctx, actor, action, target, raw); err != nil {
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
