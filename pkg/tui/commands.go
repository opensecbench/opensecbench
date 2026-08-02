package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/opensecbench/opensecbench/pkg/client"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// Messages flowing back into the Bubble Tea update loop. Every command below resolves to one of these.

type projectsMsg []model.Project
type threadsMsg []model.Thread

// openedMsg carries a thread ready to converse in: its history plus the live event stream to reconcile
// against (the two halves of the attach(thread) primitive, ADR-0063).
type openedMsg struct {
	thread  model.Thread
	history []model.Message
	events  <-chan client.Event
}

type eventMsg client.Event
type streamClosedMsg struct{}

// findingsMsg / observationsMsg carry the results of the /findings and /observations commands — local API
// reads that never touch an LLM (so they work regardless of egress clearance).
type findingsMsg struct {
	items []model.Finding
	err   error
}
type observationsMsg struct {
	items []model.Observation
	err   error
}

func loadFindings(ctx context.Context, c *client.Client) tea.Cmd {
	return func() tea.Msg {
		items, err := c.ListFindings(ctx)
		return findingsMsg{items, err}
	}
}

func loadObservations(ctx context.Context, c *client.Client, projectID string) tea.Cmd {
	return func() tea.Msg {
		items, err := c.ProjectObservations(ctx, projectID)
		return observationsMsg{items, err}
	}
}

// sentMsg is a completed send: the turn's final result (answer or a pending approval), or an error. The
// live text arrives over the event stream; this reconciles the end of the turn.
type sentMsg struct {
	res client.SendResult
	err error
}

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

func loadProjects(ctx context.Context, c *client.Client) tea.Cmd {
	return func() tea.Msg {
		ps, err := c.ListProjects(ctx)
		if err != nil {
			return errMsg{err}
		}
		return projectsMsg(ps)
	}
}

func loadThreads(ctx context.Context, c *client.Client, projectID string) tea.Cmd {
	return func() tea.Msg {
		ts, err := c.ProjectThreads(ctx, projectID)
		if err != nil {
			return errMsg{err}
		}
		return threadsMsg(ts)
	}
}

// openThread readies a conversation: for an empty threadID it first creates a thread, then fetches
// history and attaches the project's live event stream. The stream is bound to ctx, so it stops when
// the program's context is cancelled on exit.
func openThread(ctx context.Context, c *client.Client, projectID, threadID, newTitle string) tea.Cmd {
	return func() tea.Msg {
		if threadID == "" {
			th, err := c.CreateThread(ctx, projectID, newTitle)
			if err != nil {
				return errMsg{err}
			}
			threadID = th.ID
		}
		detail, err := c.ProjectThread(ctx, projectID, threadID)
		if err != nil {
			return errMsg{err}
		}
		return openedMsg{thread: detail.Thread, history: detail.Messages, events: c.Attach(ctx, projectID)}
	}
}

// waitForEvent blocks on the next event and re-issues itself after each one, the canonical Bubble Tea
// pattern for draining a channel into the update loop.
func waitForEvent(ch <-chan client.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return eventMsg(ev)
	}
}

// sendMessage posts to the thread. It blocks until the turn ends (the live text streams over the event
// channel meanwhile), then delivers the final result for reconciliation.
func sendMessage(ctx context.Context, c *client.Client, projectID, threadID, text string) tea.Cmd {
	return func() tea.Msg {
		res, err := c.SendToThread(ctx, projectID, threadID, text)
		return sentMsg{res: res, err: err}
	}
}

// decideApproval approves/denies a gated tool and resumes the turn. Like sendMessage, the continuation
// streams over the event channel and the final result comes back as a sentMsg for reconciliation.
func decideApproval(ctx context.Context, c *client.Client, projectID, approvalID, decision string) tea.Cmd {
	return func() tea.Msg {
		res, err := c.DecideThreadApproval(ctx, projectID, approvalID, decision)
		return sentMsg{res: res, err: err}
	}
}
