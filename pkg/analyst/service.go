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
	"github.com/opensecbench/opensecbench/pkg/replay"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

const defaultTokenBudget = 200_000

// Service drives resumable Analyst runs over persisted threads and the approval queue, enforcing
// the token budget and data-egress policy.
type Service struct {
	store         *store.DB
	engine        *task.Engine
	provider      llm.Provider
	replay        *replay.Client
	blobs         *cas.Store
	workspaceRoot string
	egressStrict  bool
	providerLocal bool
	tokenBudget   int

	// Audit, if set, records agent loop events (tool calls, gate decisions, answers).
	Audit func(action, detail string)
}

// NewService wires the Analyst service. Egress policy and budget are read from OSB_EGRESS_POLICY
// (default strict) and OSB_AGENT_MAX_TOKENS.
func NewService(st *store.DB, engine *task.Engine, blobs *cas.Store, workspaceRoot string, provider llm.Provider) *Service {
	budget := defaultTokenBudget
	if v := os.Getenv("OSB_AGENT_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			budget = n
		}
	}
	return &Service{
		store:         st,
		engine:        engine,
		provider:      provider,
		replay:        replay.New(0),
		blobs:         blobs,
		workspaceRoot: workspaceRoot,
		egressStrict:  os.Getenv("OSB_EGRESS_POLICY") != "open", // default: strict
		providerLocal: provider != nil && llm.IsLocal(provider),
		tokenBudget:   budget,
	}
}

// Available reports whether an LLM provider is configured.
func (svc *Service) Available() bool { return svc.provider != nil }

// SetEgressStrict overrides the data-egress posture (e.g. from the active policy profile): when
// strict, capability output for a private asset is not sent to an external provider.
func (svc *Service) SetEgressStrict(strict bool) { svc.egressStrict = strict }

func (svc *Service) session(projectID string, profile Profile, policy Policy) *agent.Session {
	return &agent.Session{
		Provider:    svc.provider,
		Tools:       profile.ToolSet(),
		Gate:        func(c agent.ToolCall) bool { return policy.NeedsApproval(c.Tool, profile.ID) },
		Execute:     svc.executeFor(projectID),
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
	raw, err := svc.store.GetSetting(ctx, ApprovalPolicySetting)
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
func (svc *Service) executeFor(projectID string) func(context.Context, agent.ToolCall) (string, error) {
	exec := Executor(ExecDeps{Store: svc.store, Engine: svc.engine, Replay: svc.replay, Blobs: svc.blobs, Runner: runner.LocalRunner{}, WorkspaceRoot: svc.workspaceRoot, ProjectID: projectID})
	return func(ctx context.Context, call agent.ToolCall) (string, error) {
		if svc.egressStrict && !svc.providerLocal {
			// Reading a private asset's contents into an external model is data egress (ADR-0011/0020).
			if assetEgressTools[call.Tool] {
				if assetID, _ := call.Args["asset"].(string); assetID != "" {
					if asset, err := svc.store.GetAsset(ctx, assetID); err == nil && asset.Sensitivity == model.SensitivityPrivate {
						return "", fmt.Errorf("blocked by data-egress policy: %q would send a private asset's contents to the external provider %q; use a local provider (e.g. ollama) or set OSB_EGRESS_POLICY=open", call.Tool, svc.provider.Name())
					}
				}
			}
			// Ingested corpus (documents, emails, chat) has no per-item sensitivity flag, so it is treated
			// as private by default: its content does not leave to an external provider under a strict policy.
			if call.Tool == "read_context" {
				return "", fmt.Errorf("blocked by data-egress policy: read_context would send ingested document/correspondence content to the external provider %q; use a local provider (e.g. ollama) or set OSB_EGRESS_POLICY=open", svc.provider.Name())
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

// SendResult is the outcome of sending a message or deciding an approval.
type SendResult struct {
	Thread       model.Thread    `json:"thread"`
	NewMessages  []model.Message `json:"new_messages"`
	Answer       string          `json:"answer,omitempty"`
	Pending      *model.Approval `json:"pending_approval,omitempty"`
	InputTokens  int             `json:"input_tokens"`
	OutputTokens int             `json:"output_tokens"`
}

// Send appends a user message to a thread and advances the run until an answer or a gated tool
// call (which pauses, creating a pending approval).
func (svc *Service) Send(ctx context.Context, threadID, userMessage string) (SendResult, error) {
	if svc.provider == nil {
		return SendResult{}, errors.New("no LLM provider configured")
	}
	th, err := svc.store.GetThread(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	profile := ProfileByID(th.AgentType)
	sess := svc.session(projectOf(th), profile, svc.loadPolicy(ctx))

	existing, err := svc.store.ListMessages(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	if len(existing) == 0 {
		if _, err := svc.store.AppendMessage(ctx, threadID, llm.RoleSystem, profile.SystemPrompt()); err != nil {
			return SendResult{}, err
		}
	}
	if _, err := svc.store.AppendMessage(ctx, threadID, llm.RoleUser, userMessage); err != nil {
		return SendResult{}, err
	}

	prior, err := svc.loadMessages(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	out, err := sess.Advance(ctx, prior)
	if err != nil {
		_ = svc.store.UpdateThreadStatus(ctx, threadID, model.ThreadError)
		return SendResult{}, err
	}
	return svc.finish(ctx, threadID, len(prior), out)
}

// Decide records an approve/deny decision and resumes the paused run.
func (svc *Service) Decide(ctx context.Context, approvalID, decision string) (SendResult, error) {
	if svc.provider == nil {
		return SendResult{}, errors.New("no LLM provider configured")
	}
	ap, err := svc.store.GetApproval(ctx, approvalID)
	if err != nil {
		return SendResult{}, err
	}
	approved := decision == "approve" || decision == "approved"
	status := model.ApprovalDenied
	if approved {
		status = model.ApprovalApproved
	}
	if _, err := svc.store.DecideApproval(ctx, approvalID, status); err != nil {
		return SendResult{}, err
	}

	th, err := svc.store.GetThread(ctx, ap.ThreadID)
	if err != nil {
		return SendResult{}, err
	}
	profile := ProfileByID(th.AgentType)
	prior, err := svc.loadMessages(ctx, ap.ThreadID)
	if err != nil {
		return SendResult{}, err
	}
	var args map[string]any
	_ = json.Unmarshal(ap.Args, &args)
	call := agent.ToolCall{Tool: ap.Tool, Args: args}

	out, err := svc.session(projectOf(th), profile, svc.loadPolicy(ctx)).Resume(ctx, prior, call, approved)
	if err != nil {
		_ = svc.store.UpdateThreadStatus(ctx, ap.ThreadID, model.ThreadError)
		return SendResult{}, err
	}
	return svc.finish(ctx, ap.ThreadID, len(prior), out)
}

// finish persists the messages produced this advance and updates thread status / approvals.
func (svc *Service) finish(ctx context.Context, threadID string, priorLen int, out agent.Outcome) (SendResult, error) {
	res := SendResult{InputTokens: out.InputTokens, OutputTokens: out.OutputTokens}
	for _, m := range out.Messages[priorLen:] {
		rec := model.Message{ThreadID: threadID, Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, ToolError: m.ToolError}
		if len(m.ToolCalls) > 0 {
			if b, err := json.Marshal(m.ToolCalls); err == nil {
				rec.ToolCalls = b
			}
		}
		saved, err := svc.store.AppendMessageFull(ctx, rec)
		if err != nil {
			return SendResult{}, err
		}
		res.NewMessages = append(res.NewMessages, saved)
	}

	if out.Pending != nil {
		args, _ := json.Marshal(out.Pending.Args)
		ap, err := svc.store.CreateApproval(ctx, threadID, out.Pending.Tool, args)
		if err != nil {
			return SendResult{}, err
		}
		if err := svc.store.UpdateThreadStatus(ctx, threadID, model.ThreadAwaitingApproval); err != nil {
			return SendResult{}, err
		}
		res.Pending = &ap
	} else {
		res.Answer = out.Answer
		if err := svc.store.UpdateThreadStatus(ctx, threadID, model.ThreadActive); err != nil {
			return SendResult{}, err
		}
	}

	th, err := svc.store.GetThread(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	res.Thread = th
	return res, nil
}

func (svc *Service) loadMessages(ctx context.Context, threadID string) ([]llm.Message, error) {
	stored, err := svc.store.ListMessages(ctx, threadID)
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
