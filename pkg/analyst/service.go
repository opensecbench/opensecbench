package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/rag"
	"github.com/opensecbench/opensecbench/pkg/replay"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

const defaultTokenBudget = 200_000

// Service drives resumable Analyst runs over persisted threads and the approval queue, enforcing
// the token budget and data-egress policy.
type Service struct {
	mgr           *store.Manager
	engine        *task.Engine
	provider      llm.Provider
	resolver      func(context.Context, string) (llm.Provider, error)
	replay        *replay.Client
	casr          cas.Resolver
	workspaceRoot string
	providerLocal bool
	tokenBudget         int
	indexer             *rag.Indexer          // semantic corpus index (ADR-0039)
	methods             *methodology.Registry // catalog the save_methodology tool authors into (ADR-0055)

	// egressSender, if set, routes the send_request tool through a chosen runner's vantage (ADR-0025).
	egressSender func(context.Context, string, replay.Request) (replay.Response, error)

	// uiPublish, if set, delivers a UICommand from the "show" tool to the project's UI event stream (SSE),
	// letting a running agent take the human's workbench to the evidence it is discussing (co-driving).
	// Injected by the API layer, which owns the event bus; nil in headless runs, where "show" is a no-op.
	uiPublish func(projectID string, cmd UICommand)

	// msgPublish, if set, streams each agent message (assistant turn, tool result, stop notice) over the
	// project event stream as a turn runs, so the chat paints steps live instead of only at the end.
	// Injected by the API layer; nil in headless runs (the turn still persists normally).
	msgPublish func(projectID, threadID string, m llm.Message)

	// deltaPublish, if set, streams assistant text token-by-token over the event stream as it generates, so
	// the final answer types out live. Injected by the API layer; nil ⇒ the answer just arrives per-message.
	deltaPublish func(projectID, threadID, text string)

	// Audit, if set, records agent loop events (tool calls, gate decisions, answers).
	Audit func(action, detail string)

	// runs tracks in-flight background agent runs (delegated sub-agents like batch triage) so the
	// "Running now" surface can report them — they're neither capability tasks nor plans. It's shared
	// (injected via SetRunRegistry), because the API rebuilds a Service per request; a per-instance map
	// wouldn't be seen by the activity handler.
	runs *RunRegistry
}

// ActiveRun is one in-flight background agent run, surfaced in the "Running now" activity view.
type ActiveRun struct {
	ID        string             `json:"id"`
	ProjectID string             `json:"project_id"`
	Profile   string             `json:"profile"`
	Label     string             `json:"label"`
	StartedAt time.Time          `json:"started_at"`
	cancel    context.CancelFunc `json:"-"` // stops the run when the human cancels it
}

// RunRegistry is a process-wide record of in-flight background agent runs, shared across the
// per-request Service instances the API builds.
type RunRegistry struct {
	mu   sync.Mutex
	runs map[string]ActiveRun
}

// NewRunRegistry creates an empty registry.
func NewRunRegistry() *RunRegistry { return &RunRegistry{runs: map[string]ActiveRun{}} }

// List returns the in-flight runs newest first.
func (r *RunRegistry) List() []ActiveRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ActiveRun, 0, len(r.runs))
	for _, run := range r.runs {
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Cancel stops the run with the given id, returning whether it was found.
func (r *RunRegistry) Cancel(id string) bool {
	r.mu.Lock()
	run, ok := r.runs[id]
	r.mu.Unlock()
	if ok && run.cancel != nil {
		run.cancel()
	}
	return ok
}

// SetRunRegistry injects the shared registry so this Service reports into the same store the activity
// endpoint reads.
func (svc *Service) SetRunRegistry(r *RunRegistry) { svc.runs = r }

// SetMethods gives the service the methodology registry the save_methodology tool authors into (ADR-0055), so
// agent-authored packs land in the same catalog the human edits. Injected by the API layer.
func (svc *Service) SetMethods(r *methodology.Registry) { svc.methods = r }

// trackRun registers a background agent run, returning a cancelable context for it and a closure that
// deregisters it on completion. Cancel (via the registry) cancels that context, so the run stops.
func (svc *Service) trackRun(parent context.Context, projectID, profile, label string) (context.Context, func()) {
	if svc.runs == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	id := uuid.NewString()
	svc.runs.mu.Lock()
	svc.runs.runs[id] = ActiveRun{ID: id, ProjectID: projectID, Profile: profile, Label: label, StartedAt: time.Now(), cancel: cancel}
	svc.runs.mu.Unlock()
	return ctx, func() {
		cancel()
		svc.runs.mu.Lock()
		delete(svc.runs.runs, id)
		svc.runs.mu.Unlock()
	}
}

// ActiveRuns returns the currently in-flight background agent runs, newest first.
func (svc *Service) ActiveRuns() []ActiveRun {
	if svc.runs == nil {
		return nil
	}
	return svc.runs.List()
}

// SetEgressSender injects the runner-aware HTTP sender used by the send_request tool (ADR-0025). Without
// it, agent sends go out from the local host.
func (svc *Service) SetEgressSender(fn func(context.Context, string, replay.Request) (replay.Response, error)) {
	svc.egressSender = fn
}

// UICommand is a navigation instruction the Analyst sends to the workbench over the project event stream
// (SSE) so a running agent can take the human's screen to the evidence it is discussing — the "show" tool.
// The frontend applies it only when the human has enabled Analyst navigation; it never mutates data.
type UICommand struct {
	Action   string `json:"action"` // "show" (the only action today)
	Kind     string `json:"kind"`   // finding | observation | route | code | surface
	ID       string `json:"id,omitempty"`
	Location string `json:"location,omitempty"` // kind=code: "path" or "path:line" within the asset
}

// SetUIPublisher injects the sink that delivers the Analyst's navigation commands ("show" tool) to the
// workbench. Without it, "show" still succeeds (the agent keeps explaining) but moves nothing.
func (svc *Service) SetUIPublisher(fn func(projectID string, cmd UICommand)) { svc.uiPublish = fn }

// SetMessagePublisher injects the sink that streams a turn's messages live to the chat as they are
// produced. Without it, messages only appear when the whole turn finishes and the UI refreshes.
func (svc *Service) SetMessagePublisher(fn func(projectID, threadID string, m llm.Message)) {
	svc.msgPublish = fn
}

// SetDeltaPublisher injects the sink that streams assistant text token-by-token as it generates, so the
// answer types out live. Without it, text arrives whole with its message.
func (svc *Service) SetDeltaPublisher(fn func(projectID, threadID, text string)) {
	svc.deltaPublish = fn
}

// runShow handles the "show" tool: it publishes a navigation command to the project's UI stream so the
// workbench takes the human to the named evidence. It changes no data (auto-approved); the frontend applies
// it only if the human enabled Analyst navigation, so it is safe whether or not they're letting it drive.
func (svc *Service) runShow(projectID string, call agent.ToolCall) (string, error) {
	kind, _ := call.Args["kind"].(string)
	if kind == "" {
		return "", errors.New("show requires 'kind'")
	}
	id, _ := call.Args["id"].(string)
	loc, _ := call.Args["location"].(string)
	switch kind {
	case "code":
		if id == "" || loc == "" {
			return "", errors.New("show kind=code requires 'id' (the source asset) and 'location' (path or path:line)")
		}
	case "surface":
		if id == "" {
			return "", errors.New("show kind=surface requires 'id' (the surface key, e.g. findings)")
		}
	default:
		if id == "" {
			return "", fmt.Errorf("show kind=%s requires 'id'", kind)
		}
	}
	if svc.uiPublish != nil {
		svc.uiPublish(projectID, UICommand{Action: "show", Kind: kind, ID: id, Location: loc})
	}
	target := kind
	if id != "" {
		target += " " + id
	}
	return fmt.Sprintf("Requested the workbench to show %s. If the human has enabled Analyst navigation their screen is now there; otherwise tell them where to look.", target), nil
}

// NewService wires the Analyst service. Egress policy and budget are read from OSB_EGRESS_POLICY
// (default strict) and OSB_AGENT_MAX_TOKENS.
func NewService(mgr *store.Manager, engine *task.Engine, casr cas.Resolver, workspaceRoot string, provider llm.Provider) *Service {
	budget := defaultTokenBudget
	if v := os.Getenv("OSB_AGENT_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			budget = n
		}
	}
	return &Service{
		mgr:           mgr,
		engine:        engine,
		provider:      provider,
		replay:        replay.New(0),
		casr:          casr,
		workspaceRoot: workspaceRoot,
		providerLocal: provider != nil && llm.IsLocal(provider),
		tokenBudget:   budget,
		// Semantic corpus index (ADR-0039): a local embedder by default, so corpus text is embedded on-host.
		indexer: &rag.Indexer{Mgr: mgr, Casr: casr, Embed: llm.EmbedderFromEnv()},
	}
}

// Indexer exposes the semantic corpus index (ADR-0039) so the API can drive reindex/search directly.
func (svc *Service) Indexer() *rag.Indexer { return svc.indexer }

// Available reports whether an LLM provider is configured.
func (svc *Service) Available() bool { return svc.provider != nil }

// g is the instance-wide database (settings, providers, saved playbooks/profiles). p is a project's
// database, resolved by id, falling back to global so a nil handle never panics (ADR-0049).
func (svc *Service) g() *store.DB {
	if svc.mgr == nil {
		return nil
	}
	return svc.mgr.Global()
}

// casFor returns the content store owning a project's blobs (ADR-0049), nil if unresolved.
func (svc *Service) casFor(projectID string) *cas.Store {
	if svc.casr == nil {
		return nil
	}
	st, err := svc.casr.For(projectID)
	if err != nil {
		return nil
	}
	return st
}

func (svc *Service) p(projectID string) *store.DB {
	if svc.mgr == nil {
		return nil
	}
	db, err := svc.mgr.Project(projectID)
	if err != nil || db == nil {
		return svc.mgr.Global()
	}
	return db
}

// SetProviderResolver injects a function that builds a configured provider by its registry id, enabling
// cross-provider model routing (ADR-0021). Without it, routing falls back to the active provider.
func (svc *Service) SetProviderResolver(fn func(context.Context, string) (llm.Provider, error)) {
	svc.resolver = fn
}

// ModelRoutingSetting is the settings key holding the tag → (provider, model) routing map (ADR-0021).
const ModelRoutingSetting = "model_routing"

// RoutingTags is the built-in routing vocabulary (ADR-0021 / ADR-0052). These drive the built-in task
// profiles; users may also define arbitrary custom tags, which resolve the same way.
func RoutingTags() []string {
	return []string{"default", "cheap", "fast", "reasoning", "long-context"}
}

type modelRef struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

// modelRouting maps a tag to an ordered priority list of (connection, model) — index 0 is used first,
// later entries are fall-through candidates (ADR-0052). "default" is a tag like any other.
type modelRouting struct {
	Tags map[string][]modelRef
}

// loadRouting reads the routing setting. Canonical shape (ADR-0052) is flat: {tag: [ordered refs]}. It
// also accepts the legacy nested shape ({"default": ref, "tags": {tag: ref}}) so existing configs keep
// working — detected by a "tags" key whose value is a JSON object.
func (svc *Service) loadRouting(ctx context.Context) modelRouting {
	out := modelRouting{Tags: map[string][]modelRef{}}
	raw, err := svc.g().GetSetting(ctx, ModelRoutingSetting)
	if err != nil || raw == "" {
		return out
	}
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &top) != nil {
		return out
	}
	asList := func(m json.RawMessage) []modelRef {
		if len(m) == 0 || string(m) == "null" {
			return nil
		}
		var list []modelRef
		if json.Unmarshal(m, &list) == nil {
			return list // ordered-list shape
		}
		var one modelRef
		if json.Unmarshal(m, &one) == nil && one.ProviderID != "" {
			return []modelRef{one} // legacy single ref → 1-item list
		}
		return nil
	}
	// Legacy nested shape: a "tags" key holding an object of per-tag refs.
	if tagsRaw, ok := top["tags"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(tagsRaw, &nested) == nil {
			if l := asList(top["default"]); len(l) > 0 {
				out.Tags["default"] = l
			}
			for tag, m := range nested {
				if l := asList(m); len(l) > 0 {
					out.Tags[tag] = l
				}
			}
			return out
		}
	}
	// Canonical flat shape: every key is a tag.
	for tag, m := range top {
		if l := asList(m); len(l) > 0 {
			out.Tags[tag] = l
		}
	}
	return out
}

// runTarget is the provider a task runs on plus how to attribute its token usage. ProviderName/AttrModel
// are empty when the run falls back to the active provider — the caller (which knows the active
// provider's identity) fills those in for the usage record.
type runTarget struct {
	Provider     llm.Provider
	SessionModel string // model id handed to the session (drives the request; unchanged routing behavior)
	ProviderName string // provider type recorded for usage attribution; "" = active provider
	AttrModel    string // model recorded for usage; "" = active provider's default
	// Clearance is the effective data-clearance ceiling for this destination — the connection's clearance
	// tightened by any per-model override (model.MinClearance). "" (the fallback-provider path, which has
	// no known connection) fails safe to open-source only. The egress gate reads it in executeFor.
	Clearance string
}

// targetForTag resolves the run target for a task with the given tag (ADR-0052): the first resolvable
// entry in the tag's ordered list, falling to the "default" list, then to the active provider. Later
// entries in a list are fall-through candidates — used here only when an earlier one can't be built
// (unknown provider / vault failure); fall-through on a failed *call* is layered on separately. When
// routing resolves a distinct provider it captures that provider's type + model for usage attribution.
func (svc *Service) targetForTag(ctx context.Context, tag string) runTarget {
	if svc.resolver == nil {
		return runTarget{Provider: svc.provider}
	}
	r := svc.loadRouting(ctx)
	list := r.Tags[tag]
	if len(list) == 0 && tag != "default" {
		list = r.Tags["default"] // an unset or empty tag inherits the default list
	}
	var entries []llm.FallbackEntry
	var top runTarget
	var mode, haveTop bool
	for _, ref := range list {
		if ref.ProviderID == "" {
			continue
		}
		p, err := svc.resolver(ctx, ref.ProviderID)
		if err != nil || p == nil {
			continue // unresolvable connection — skip it in priority order
		}
		m := nativeMode(p)
		if !haveTop {
			mode, haveTop = m, true
			top = runTarget{Provider: p, SessionModel: ref.Model, AttrModel: ref.Model}
			if reg, err := svc.g().GetProvider(ctx, ref.ProviderID); err == nil {
				top.ProviderName = reg.Type
				if top.AttrModel == "" {
					top.AttrModel = reg.Model // no routed model → the connection's configured default
				}
				// Effective egress clearance for this destination: the connection's clearance, tightened by
				// any per-model override (a model pinned lower than its vendor — e.g. one with a retention
				// policy not covered by the DPA).
				top.Clearance = model.MinClearance(reg.DataClearance, svc.g().ConnectionModelClearance(ctx, ref.ProviderID, ref.Model))
			}
		} else if m != mode {
			continue // fall-through candidates must share the top's tool mode (consistent rendering)
		}
		entries = append(entries, llm.FallbackEntry{Provider: p, Model: ref.Model})
	}
	if !haveTop {
		return runTarget{Provider: svc.provider}
	}
	if len(entries) > 1 {
		// The chain tries the top first and falls through on a transient failure (ADR-0052). Usage is
		// attributed to the top entry; a fell-through call is a rare, transient event.
		top.Provider = &llm.FallbackProvider{Entries: entries}
	}
	return top
}

// nativeMode reports a provider's tool-use mode (false when it doesn't advertise one).
func nativeMode(p llm.Provider) bool {
	if n, ok := p.(interface{ NativeTools() bool }); ok {
		return n.NativeTools()
	}
	return false
}

// RoutingEntry is one (connection, model) in a tag's ordered priority list — the API/UI boundary type.
type RoutingEntry struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

// Routing returns the normalized tag → ordered-list routing map (ADR-0052), reading through the legacy
// single-ref shape so the UI always sees ordered lists.
func (svc *Service) Routing(ctx context.Context) map[string][]RoutingEntry {
	out := map[string][]RoutingEntry{}
	for tag, list := range svc.loadRouting(ctx).Tags {
		for _, ref := range list {
			out[tag] = append(out[tag], RoutingEntry(ref))
		}
	}
	return out
}

// DefaultRoutingRef returns the top (connection, model) of the "default" tag — what the interactive chat
// resolves to (ADR-0052) — for the UI's model indicator. ok is false when the default tag is unset.
func (svc *Service) DefaultRoutingRef(ctx context.Context) (RoutingEntry, bool) {
	for _, ref := range svc.loadRouting(ctx).Tags["default"] {
		if ref.ProviderID != "" {
			return RoutingEntry(ref), true
		}
	}
	return RoutingEntry{}, false
}

// providerModelForTag resolves the provider + session model for a tag (ADR-0021). Retained for callers
// that don't attribute usage; new attribution-aware callers use targetForTag.
func (svc *Service) providerModelForTag(ctx context.Context, tag string) (llm.Provider, string) {
	t := svc.targetForTag(ctx, tag)
	return t.Provider, t.SessionModel
}

func (svc *Service) session(projectID, threadID string, profile Profile, policy Policy, prov llm.Provider, modelID, clearance string) *agent.Session {
	sess := &agent.Session{
		Provider: prov,
		Model:    modelID,
		Tools:    profile.ToolSet(),
		Gate: func(c agent.ToolCall) bool {
			// web_fetch to a preapproved research source needs no approval; any other URL pauses (ADR-0038).
			if c.Tool == "web_fetch" && isPreapprovedSource(stringArg(c, "url")) {
				return false
			}
			return policy.NeedsApproval(c.Tool, profile.ID)
		},
		Execute:     svc.executeFor(projectID, prov, clearance),
		MaxSteps:    interactiveMaxSteps(),
		TokenBudget: svc.tokenBudget,
		Audit:       svc.Audit,
	}
	// Stream each step to the chat live as it is produced (ADR-0053) — so a multi-step investigation paints
	// as it works instead of dumping at the end. Best-effort; the turn still persists normally in finish().
	if svc.msgPublish != nil && threadID != "" {
		sess.OnMessage = func(m llm.Message) { svc.msgPublish(projectID, threadID, m) }
	}
	if svc.deltaPublish != nil && threadID != "" {
		sess.OnDelta = func(text string) { svc.deltaPublish(projectID, threadID, text) }
	}
	return sess
}

// ApprovalPolicySetting is the settings key holding the trust-curve override rules as a JSON array (ADR-0019 §5).
const ApprovalPolicySetting = "analyst_approval_rules"

// AutonomySetting is the settings key holding the autonomy envelope — the control surface (ADR-0054):
// "cautious" (default) or "trusted". It shifts the confirm line across consequence tiers.
const AutonomySetting = "analyst_autonomy"

// loadPolicy reads the configured governance policy: the consequence-tier base under the human's autonomy
// envelope, plus any trust-curve override rules. Unset/unparseable settings yield the conservative default.
func (svc *Service) loadPolicy(ctx context.Context) Policy {
	p := DefaultPolicy()
	if raw, err := svc.g().GetSetting(ctx, ApprovalPolicySetting); err == nil && raw != "" {
		var rules []Rule
		if json.Unmarshal([]byte(raw), &rules) == nil {
			p = NewPolicy(rules)
		}
	}
	if a, err := svc.g().GetSetting(ctx, AutonomySetting); err == nil && a != "" {
		p = p.WithAutonomy(Autonomy(a))
	}
	return p
}

// executeFor builds the tool executor for a thread's project, wrapped with the data-egress gate: a tool
// that would read asset/corpus content into an EXTERNAL model is blocked unless that destination's data
// clearance covers the content's sensitivity tier. `clearance` is the effective ceiling for the routed
// destination (connection ∩ model, resolved in targetForTag); "" fails safe to open-source only.
func (svc *Service) executeFor(projectID string, prov llm.Provider, clearance string) func(context.Context, agent.ToolCall) (string, error) {
	exec := Executor(ExecDeps{Mgr: svc.mgr, Engine: svc.engine, Replay: svc.replay, Blobs: svc.casFor(projectID), Runner: runner.LocalRunner{}, WorkspaceRoot: svc.workspaceRoot, ProjectID: projectID, EgressSender: svc.egressSender, Indexer: svc.indexer, Methods: svc.methods, Narrator: svc})
	// The gate keys on the provider that will actually receive the tool output (which, with tag routing,
	// may differ per task); a local model is never an egress risk, so its clearance is irrelevant.
	external := prov != nil && !llm.IsLocal(prov)
	pname := "the external provider"
	if prov != nil {
		pname = prov.Name()
	}
	// Per-engagement tightening (ADR-0051): a "restricted" engagement clamps this destination to
	// open-source only for the project, regardless of its configured clearance — only ever tighter.
	if projectID != "" {
		if eng, err := svc.p(projectID).GetEngagement(context.Background(), projectID); err == nil && eng.DataClass == model.DataRestricted {
			clearance = model.SensitivityOpenSource
		}
	}
	return func(ctx context.Context, call agent.ToolCall) (string, error) {
		// delegate spawns a specialist sub-agent — handled at the service level (it needs the provider),
		// not the pure tool Executor.
		if call.Tool == "delegate" {
			return svc.runDelegate(ctx, projectID, call)
		}
		// show drives the human's workbench over the event bus — a service-level side effect, not a store
		// operation, so it lives here rather than in the pure Executor. It touches no assets (no egress guard).
		if call.Tool == "show" {
			return svc.runShow(projectID, call)
		}
		// Default-deny egress: sending any project content into an external model is gated by the
		// destination's clearance (ADR-0011/0020). Asset-scoped tools use the asset's own sensitivity tier;
		// every other non-safe tool is private-by-default (see egressSafeTools for what returns no content).
		if external && !egressSafeTools[call.Tool] {
			required := model.SensitivityPrivate
			detail := "returns project content treated as private"
			if assetEgressTools[call.Tool] {
				if assetID, _ := call.Args["asset"].(string); assetID != "" {
					asset, err := svc.p(projectID).GetAsset(ctx, assetID)
					if err != nil {
						// Fail closed: if we can't classify the asset we can't prove egress is permitted.
						return "", fmt.Errorf("blocked by data-egress policy: %q could not resolve asset %q to verify it is cleared for the external provider %q", call.Tool, assetID, pname)
					}
					required = asset.Sensitivity
					detail = fmt.Sprintf("would send %s asset content", model.ClearanceLabel(asset.Sensitivity))
				}
			}
			if !model.ClearanceAllows(clearance, required) {
				return "", fmt.Errorf("blocked by data-egress policy: %q %s to %q, which is cleared only for %s; use a local provider (e.g. ollama), raise the destination's clearance, or lower the asset's sensitivity", call.Tool, detail, pname, model.ClearanceLabel(clearance))
			}
		}
		return exec(ctx, call)
	}
}

// clearedForPrivate reports whether a routed destination may receive private-by-default project content
// (findings, observations, report data) sent to it by a direct completion — i.e. the bounded LLM calls
// (narration, batch triage) that don't flow through the tool gate. A local provider is never a risk; an
// external one must be cleared for private, honoring the per-engagement restricted clamp (ADR-0051).
func (svc *Service) clearedForPrivate(ctx context.Context, projectID string, tgt runTarget) bool {
	if tgt.Provider == nil || llm.IsLocal(tgt.Provider) {
		return true
	}
	clearance := tgt.Clearance
	if projectID != "" {
		if eng, err := svc.p(projectID).GetEngagement(ctx, projectID); err == nil && eng.DataClass == model.DataRestricted {
			clearance = model.SensitivityOpenSource
		}
	}
	return model.ClearanceAllows(clearance, model.SensitivityPrivate)
}

// projectOf returns a thread's project id, or "" if it is a project-less thread.
func projectOf(th model.Thread) string {
	if th.ProjectID == nil {
		return ""
	}
	return *th.ProjectID
}

// SendResult is the outcome of sending a message or deciding an approval. Provider/Model/AgentType
// describe the backend and profile that actually ran this advance, for accurate usage attribution;
// Provider/Model are empty when the active provider ran (the caller fills those in).
type SendResult struct {
	Thread       model.Thread    `json:"thread"`
	NewMessages  []model.Message `json:"new_messages"`
	Answer       string          `json:"answer,omitempty"`
	Pending      *model.Approval `json:"pending_approval,omitempty"`
	InputTokens  int             `json:"input_tokens"`
	OutputTokens int             `json:"output_tokens"`
	Provider     string          `json:"provider,omitempty"`
	Model        string          `json:"model,omitempty"`
	AgentType    string          `json:"agent_type,omitempty"`
}

// Send appends a user message to a thread and advances the run until an answer or a gated tool
// call (which pauses, creating a pending approval).
func (svc *Service) Send(ctx context.Context, projectID, threadID, userMessage, viewContext string) (SendResult, error) {
	if svc.provider == nil {
		return SendResult{}, errors.New("no LLM provider configured")
	}
	th, err := svc.p(projectID).GetThread(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	profile := svc.resolveProfile(ctx, th.AgentType)
	tgt := svc.targetForTag(ctx, profile.ModelTag)
	sess := svc.session(projectOf(th), threadID, profile, svc.loadPolicy(ctx), tgt.Provider, tgt.SessionModel, tgt.Clearance)

	existing, err := svc.p(projectID).ListMessages(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	if len(existing) == 0 {
		sys := svc.systemPromptFor(ctx, projectID, profile.SystemPrompt(), tgt.Provider, tgt.Clearance)
		if _, err := svc.p(projectID).AppendMessage(ctx, threadID, llm.RoleSystem, sys); err != nil {
			return SendResult{}, err
		}
	}
	if _, err := svc.p(projectID).AppendMessage(ctx, threadID, llm.RoleUser, userMessage); err != nil {
		return SendResult{}, err
	}

	prior, err := svc.loadMessages(ctx, projectID, threadID)
	if err != nil {
		return SendResult{}, err
	}
	// Awareness (ADR-0053): annotate this turn's user message with what's on screen so "explain this" or
	// "is this exploitable?" resolves to the finding/code/surface the human is looking at. The annotation is
	// LLM-context only — prior is a fresh copy, so the persisted user message stays clean.
	if vc := strings.TrimSpace(viewContext); vc != "" && len(prior) > 0 {
		last := &prior[len(prior)-1]
		if last.Role == llm.RoleUser {
			last.Content = "(On screen right now: " + vc + ". If I say \"this\" finding/route/file, I mean what's on screen.)\n\n" + last.Content
		}
	}
	out, err := sess.Advance(ctx, prior)
	if err != nil {
		_ = svc.p(projectID).UpdateThreadStatus(ctx, threadID, model.ThreadError)
		return SendResult{}, err
	}
	return svc.finish(ctx, projectID, threadID, len(prior), out, tgt, profile.ID)
}

// Decide records an approve/deny decision and resumes the paused run.
func (svc *Service) Decide(ctx context.Context, projectID, approvalID, decision string) (SendResult, error) {
	if svc.provider == nil {
		return SendResult{}, errors.New("no LLM provider configured")
	}
	ap, err := svc.p(projectID).GetApproval(ctx, approvalID)
	if err != nil {
		return SendResult{}, err
	}
	approved := decision == "approve" || decision == "approved"
	status := model.ApprovalDenied
	if approved {
		status = model.ApprovalApproved
	}
	if _, err := svc.p(projectID).DecideApproval(ctx, approvalID, status); err != nil {
		return SendResult{}, err
	}

	th, err := svc.p(projectID).GetThread(ctx, ap.ThreadID)
	if err != nil {
		return SendResult{}, err
	}
	profile := svc.resolveProfile(ctx, th.AgentType)
	prior, err := svc.loadMessages(ctx, projectID, ap.ThreadID)
	if err != nil {
		return SendResult{}, err
	}
	var args map[string]any
	_ = json.Unmarshal(ap.Args, &args)
	call := agent.ToolCall{Tool: ap.Tool, Args: args}

	tgt := svc.targetForTag(ctx, profile.ModelTag)
	out, err := svc.session(projectOf(th), ap.ThreadID, profile, svc.loadPolicy(ctx), tgt.Provider, tgt.SessionModel, tgt.Clearance).Resume(ctx, prior, call, approved)
	if err != nil {
		_ = svc.p(projectID).UpdateThreadStatus(ctx, ap.ThreadID, model.ThreadError)
		return SendResult{}, err
	}
	return svc.finish(ctx, projectID, ap.ThreadID, len(prior), out, tgt, profile.ID)
}

// finish persists the messages produced this advance and updates thread status / approvals. tgt and
// agentType carry the backend + profile that ran, for usage attribution.
func (svc *Service) finish(ctx context.Context, projectID, threadID string, priorLen int, out agent.Outcome, tgt runTarget, agentType string) (SendResult, error) {
	res := SendResult{
		InputTokens:  out.InputTokens,
		OutputTokens: out.OutputTokens,
		Provider:     tgt.ProviderName,
		Model:        tgt.AttrModel,
		AgentType:    agentType,
	}
	for _, m := range out.Messages[priorLen:] {
		rec := model.Message{ThreadID: threadID, Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, ToolError: m.ToolError}
		if len(m.ToolCalls) > 0 {
			if b, err := json.Marshal(m.ToolCalls); err == nil {
				rec.ToolCalls = b
			}
		}
		saved, err := svc.p(projectID).AppendMessageFull(ctx, rec)
		if err != nil {
			return SendResult{}, err
		}
		res.NewMessages = append(res.NewMessages, saved)
	}

	if out.Pending != nil {
		args, _ := json.Marshal(out.Pending.Args)
		ap, err := svc.p(projectID).CreateApproval(ctx, threadID, out.Pending.Tool, args)
		if err != nil {
			return SendResult{}, err
		}
		if err := svc.p(projectID).UpdateThreadStatus(ctx, threadID, model.ThreadAwaitingApproval); err != nil {
			return SendResult{}, err
		}
		res.Pending = &ap
	} else {
		res.Answer = out.Answer
		if err := svc.p(projectID).UpdateThreadStatus(ctx, threadID, model.ThreadActive); err != nil {
			return SendResult{}, err
		}
	}

	th, err := svc.p(projectID).GetThread(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	res.Thread = th
	return res, nil
}

func (svc *Service) loadMessages(ctx context.Context, projectID, threadID string) ([]llm.Message, error) {
	stored, err := svc.p(projectID).ListMessages(ctx, threadID)
	if err != nil {
		return nil, err
	}
	msgs := make([]llm.Message, 0, len(stored))
	for _, m := range stored {
		lm := llm.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, ToolError: m.ToolError}
		if len(m.ToolCalls) > 0 {
			_ = json.Unmarshal(m.ToolCalls, &lm.ToolCalls)
		}
		msgs = append(msgs, lm)
	}
	return msgs, nil
}
