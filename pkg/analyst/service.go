package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
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
	egressStrict  bool
	providerLocal bool
	tokenBudget   int
	indexer       *rag.Indexer // semantic corpus index (ADR-0039)

	// egressSender, if set, routes the send_request tool through a chosen runner's vantage (ADR-0025).
	egressSender func(context.Context, string, replay.Request) (replay.Response, error)

	// Audit, if set, records agent loop events (tool calls, gate decisions, answers).
	Audit func(action, detail string)
}

// SetEgressSender injects the runner-aware HTTP sender used by the send_request tool (ADR-0025). Without
// it, agent sends go out from the local host.
func (svc *Service) SetEgressSender(fn func(context.Context, string, replay.Request) (replay.Response, error)) {
	svc.egressSender = fn
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
		egressStrict:  os.Getenv("OSB_EGRESS_POLICY") != "open", // default: strict
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

// SetEgressStrict overrides the data-egress posture (e.g. from the active policy profile): when
// strict, capability output for a private asset is not sent to an external provider.
func (svc *Service) SetEgressStrict(strict bool) { svc.egressStrict = strict }

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
			out[tag] = append(out[tag], RoutingEntry{ProviderID: ref.ProviderID, Model: ref.Model})
		}
	}
	return out
}

// DefaultRoutingRef returns the top (connection, model) of the "default" tag — what the interactive chat
// resolves to (ADR-0052) — for the UI's model indicator. ok is false when the default tag is unset.
func (svc *Service) DefaultRoutingRef(ctx context.Context) (RoutingEntry, bool) {
	for _, ref := range svc.loadRouting(ctx).Tags["default"] {
		if ref.ProviderID != "" {
			return RoutingEntry{ProviderID: ref.ProviderID, Model: ref.Model}, true
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

func (svc *Service) session(projectID string, profile Profile, policy Policy, prov llm.Provider, modelID string) *agent.Session {
	return &agent.Session{
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
		Execute:     svc.executeFor(projectID, prov),
		MaxSteps:    8,
		TokenBudget: svc.tokenBudget,
		Audit:       svc.Audit,
	}
}

// ApprovalPolicySetting is the settings key holding the trust-curve override rules as a JSON array (ADR-0019 §5).
const ApprovalPolicySetting = "analyst_approval_rules"

// loadPolicy reads the configured approval policy; an unset or unparseable setting yields the
// conservative default (every sensitive tool asks).
func (svc *Service) loadPolicy(ctx context.Context) Policy {
	raw, err := svc.g().GetSetting(ctx, ApprovalPolicySetting)
	if err != nil || raw == "" {
		return DefaultPolicy()
	}
	var rules []Rule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return DefaultPolicy()
	}
	return NewPolicy(rules)
}

// executeFor builds the tool executor for a thread's project, wrapped with the data-egress policy:
// under a strict policy with an external LLM provider, running a capability on a private asset is
// blocked, because its output would be summarized by the external model.
func (svc *Service) executeFor(projectID string, prov llm.Provider) func(context.Context, agent.ToolCall) (string, error) {
	exec := Executor(ExecDeps{Mgr: svc.mgr, Engine: svc.engine, Replay: svc.replay, Blobs: svc.casFor(projectID), Runner: runner.LocalRunner{}, WorkspaceRoot: svc.workspaceRoot, ProjectID: projectID, EgressSender: svc.egressSender, Indexer: svc.indexer, Narrator: svc})
	// The egress guard keys on the provider that will actually receive the tool output (which, with tag
	// routing, may differ per task); a local model is never an egress risk.
	external := prov != nil && !llm.IsLocal(prov)
	pname := "the external provider"
	if prov != nil {
		pname = prov.Name()
	}
	// Per-engagement tightening (ADR-0051): a project whose engagement data class is "restricted" forces
	// strict egress for that project, regardless of the global posture — OR-ed with the global flag, so it
	// is never looser than global, only tighter. Resolved once here, not per tool call.
	strict := svc.egressStrict
	if !strict && projectID != "" {
		if eng, err := svc.p(projectID).GetEngagement(context.Background(), projectID); err == nil && eng.DataClass == model.DataRestricted {
			strict = true
		}
	}
	return func(ctx context.Context, call agent.ToolCall) (string, error) {
		// delegate spawns a specialist sub-agent — handled at the service level (it needs the provider),
		// not the pure tool Executor.
		if call.Tool == "delegate" {
			return svc.runDelegate(ctx, projectID, call)
		}
		if strict && external {
			// Reading a private asset's contents into an external model is data egress (ADR-0011/0020).
			if assetEgressTools[call.Tool] {
				if assetID, _ := call.Args["asset"].(string); assetID != "" {
					if asset, err := svc.p(projectID).GetAsset(ctx, assetID); err == nil && asset.Sensitivity == model.SensitivityPrivate {
						return "", fmt.Errorf("blocked by data-egress policy: %q would send a private asset's contents to the external provider %q; use a local provider (e.g. ollama) or set OSB_EGRESS_POLICY=open", call.Tool, pname)
					}
				}
			}
			// Ingested corpus (documents, emails, chat) has no per-item sensitivity flag, so it is treated
			// as private by default: its content does not leave to an external provider under a strict policy.
			if call.Tool == "read_context" {
				return "", fmt.Errorf("blocked by data-egress policy: read_context would send ingested document/correspondence content to the external provider %q; use a local provider (e.g. ollama) or set OSB_EGRESS_POLICY=open", pname)
			}
			// search_corpus returns corpus/KB chunk text to the model — same egress class as read_context.
			if call.Tool == "search_corpus" {
				return "", fmt.Errorf("blocked by data-egress policy: search_corpus would send corpus/KB content to the external provider %q; use a local provider (e.g. ollama) or set OSB_EGRESS_POLICY=open", pname)
			}
		}
		return exec(ctx, call)
	}
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
func (svc *Service) Send(ctx context.Context, projectID, threadID, userMessage string) (SendResult, error) {
	if svc.provider == nil {
		return SendResult{}, errors.New("no LLM provider configured")
	}
	th, err := svc.p(projectID).GetThread(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	profile := svc.resolveProfile(ctx, th.AgentType)
	tgt := svc.targetForTag(ctx, profile.ModelTag)
	sess := svc.session(projectOf(th), profile, svc.loadPolicy(ctx), tgt.Provider, tgt.SessionModel)

	existing, err := svc.p(projectID).ListMessages(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	if len(existing) == 0 {
		if _, err := svc.p(projectID).AppendMessage(ctx, threadID, llm.RoleSystem, profile.SystemPrompt()); err != nil {
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
	out, err := svc.session(projectOf(th), profile, svc.loadPolicy(ctx), tgt.Provider, tgt.SessionModel).Resume(ctx, prior, call, approved)
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
