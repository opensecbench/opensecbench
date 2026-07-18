package analyst

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// Service drives resumable Analyst runs over persisted threads and the approval queue.
type Service struct {
	store    *store.DB
	engine   *task.Engine
	provider llm.Provider
}

// NewService wires the Analyst service.
func NewService(st *store.DB, engine *task.Engine, provider llm.Provider) *Service {
	return &Service{store: st, engine: engine, provider: provider}
}

// Available reports whether an LLM provider is configured.
func (svc *Service) Available() bool { return svc.provider != nil }

func (svc *Service) session() *agent.Session {
	return &agent.Session{
		Provider: svc.provider,
		Tools:    Tools(),
		Gate:     func(c agent.ToolCall) bool { return gatedTools[c.Tool] },
		Execute:  Executor(svc.store, svc.engine),
		MaxSteps: 8,
	}
}

// SendResult is the outcome of sending a message or deciding an approval.
type SendResult struct {
	Thread      model.Thread    `json:"thread"`
	NewMessages []model.Message `json:"new_messages"`
	Answer      string          `json:"answer,omitempty"`
	Pending     *model.Approval `json:"pending_approval,omitempty"`
}

// Send appends a user message to a thread and advances the run until an answer or a gated tool
// call (which pauses, creating a pending approval).
func (svc *Service) Send(ctx context.Context, threadID, userMessage string) (SendResult, error) {
	if svc.provider == nil {
		return SendResult{}, errors.New("no LLM provider configured")
	}
	if _, err := svc.store.GetThread(ctx, threadID); err != nil {
		return SendResult{}, err
	}
	sess := svc.session()

	existing, err := svc.store.ListMessages(ctx, threadID)
	if err != nil {
		return SendResult{}, err
	}
	if len(existing) == 0 {
		if _, err := svc.store.AppendMessage(ctx, threadID, llm.RoleSystem, sess.SystemPrompt()); err != nil {
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

	prior, err := svc.loadMessages(ctx, ap.ThreadID)
	if err != nil {
		return SendResult{}, err
	}
	var args map[string]any
	_ = json.Unmarshal(ap.Args, &args)
	call := agent.ToolCall{Tool: ap.Tool, Args: args}

	out, err := svc.session().Resume(ctx, prior, call, approved)
	if err != nil {
		_ = svc.store.UpdateThreadStatus(ctx, ap.ThreadID, model.ThreadError)
		return SendResult{}, err
	}
	return svc.finish(ctx, ap.ThreadID, len(prior), out)
}

// finish persists the messages produced this advance and updates thread status / approvals.
func (svc *Service) finish(ctx context.Context, threadID string, priorLen int, out agent.Outcome) (SendResult, error) {
	var res SendResult
	for _, m := range out.Messages[priorLen:] {
		saved, err := svc.store.AppendMessage(ctx, threadID, m.Role, m.Content)
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
		msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
	}
	return msgs, nil
}
