package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/opensecbench/opensecbench/pkg/client"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// step applies one message and returns the concrete app for further assertions.
func step(t *testing.T, m tea.Model, msg tea.Msg) app {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(app)
}

// TestStagePickToChat walks the state machine: projects load → select → threads load → open → chat.
func TestStagePickToChat(t *testing.T) {
	a := newApp(context.Background(), nil)
	a = step(t, a, tea.WindowSizeMsg{Width: 80, Height: 24})

	a = step(t, a, projectsMsg{{ID: "p1", Name: "Acme"}})
	a = step(t, a, tea.KeyMsg{Type: tea.KeyEnter}) // select the only project
	if a.stage != stageThreads || a.project.ID != "p1" {
		t.Fatalf("after selecting project: stage=%d project=%q", a.stage, a.project.ID)
	}

	a = step(t, a, threadsMsg{{ID: "t1", Title: "old chat"}})
	a = step(t, a, openedMsg{
		thread:  model.Thread{ID: "t1", Title: "old chat"},
		history: []model.Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}, {Role: "system", Content: "ignored"}},
		events:  make(chan client.Event),
	})
	if a.stage != stageChat {
		t.Fatalf("stage = %d, want chat", a.stage)
	}
	// A thread-separator, then the user + assistant history (the system line is dropped).
	if len(a.lines) != 3 {
		t.Fatalf("lines = %d, want 3 separator+user+assistant (%+v)", len(a.lines), a.lines)
	}
	if a.lines[0].role != "event" || a.lines[1].role != "user" || a.lines[2].role != "assistant" {
		t.Fatalf("unexpected line roles: %+v", a.lines)
	}
}

// TestStreamingReconciliation is the core: deltas accumulate into the live buffer, the finalized
// assistant message supersedes it, wrong-thread events are ignored, and the send-result answer is
// idempotent (no duplicate when the stream already delivered it).
func TestStreamingReconciliation(t *testing.T) {
	a := newApp(context.Background(), nil)
	a = step(t, a, tea.WindowSizeMsg{Width: 80, Height: 24})
	a = step(t, a, openedMsg{thread: model.Thread{ID: "t1"}, events: make(chan client.Event)})

	a = step(t, a, eventMsg{Type: "analyst.delta", Payload: mustJSON(client.AnalystDelta{ThreadID: "t1", Text: "Look"})})
	a = step(t, a, eventMsg{Type: "analyst.delta", Payload: mustJSON(client.AnalystDelta{ThreadID: "t1", Text: "ing…"})})
	if a.streaming != "Looking…" {
		t.Fatalf("streaming = %q, want %q", a.streaming, "Looking…")
	}

	// A delta for a different thread must not bleed in.
	a = step(t, a, eventMsg{Type: "analyst.delta", Payload: mustJSON(client.AnalystDelta{ThreadID: "other", Text: "NOPE"})})
	if strings.Contains(a.streaming, "NOPE") {
		t.Fatalf("cross-thread delta leaked: %q", a.streaming)
	}

	// The finalized message clears the live buffer and lands as a transcript line.
	a = step(t, a, eventMsg{Type: "analyst.message", Payload: mustJSON(client.AnalystMessage{ThreadID: "t1", Role: "assistant", Content: "Found a SQLi."})})
	if a.streaming != "" {
		t.Fatalf("streaming not cleared after finalize: %q", a.streaming)
	}
	if n := len(a.lines); n == 0 || a.lines[n-1].role != "assistant" || a.lines[n-1].text != "Found a SQLi." {
		t.Fatalf("last line = %+v, want assistant 'Found a SQLi.'", a.lines)
	}

	// The blocking send returns the same answer; it must not double-append.
	before := len(a.lines)
	a = step(t, a, sentMsg{res: client.SendResult{Answer: "Found a SQLi."}})
	if len(a.lines) != before {
		t.Fatalf("duplicate answer appended: %d → %d", before, len(a.lines))
	}
	if a.sending {
		t.Fatal("sending should be cleared after sentMsg")
	}
}

// TestPendingApprovalSurfaces confirms a gated turn reports a non-blocking approval notice.
func TestPendingApprovalSurfaces(t *testing.T) {
	a := newApp(context.Background(), nil)
	a = step(t, a, openedMsg{thread: model.Thread{ID: "t1"}, events: make(chan client.Event)})
	a = step(t, a, sentMsg{res: client.SendResult{Pending: &model.Approval{Tool: "run_script"}}})
	if a.pending == nil || !strings.Contains(a.status, "approval required") {
		t.Fatalf("pending approval not surfaced: pending=%v status=%q", a.pending, a.status)
	}
}

// TestEscInterruptsTurn confirms Esc cancels the in-flight turn: it flags the interrupt and cancels the
// send request, and when the (cancelled) send returns, the partial answer is finalized as interrupted
// rather than surfaced as an error.
func TestEscInterruptsTurn(t *testing.T) {
	a := newApp(context.Background(), nil)
	a = step(t, a, tea.WindowSizeMsg{Width: 80, Height: 24})
	a = step(t, a, openedMsg{thread: model.Thread{ID: "t1"}, events: make(chan client.Event)})

	a.input.SetValue("run the full scan suite")
	a = step(t, a, tea.KeyMsg{Type: tea.KeyEnter}) // starts a send
	if !a.sending || a.cancelSend == nil {
		t.Fatalf("expected an in-flight send with a cancel (sending=%v cancel=%v)", a.sending, a.cancelSend)
	}

	a = step(t, a, eventMsg{Type: "analyst.delta", Payload: mustJSON(client.AnalystDelta{ThreadID: "t1", Text: "partial answer"})})
	a = step(t, a, tea.KeyMsg{Type: tea.KeyEsc})
	if !a.interrupting {
		t.Fatal("Esc should set interrupting")
	}

	// The cancelled request returns an error; the interrupt path must swallow it and finalize the partial.
	a = step(t, a, sentMsg{err: context.Canceled})
	if a.sending || a.interrupting {
		t.Fatalf("flags should clear after interrupt (sending=%v interrupting=%v)", a.sending, a.interrupting)
	}
	if !strings.Contains(a.status, "interrupted") {
		t.Fatalf("status = %q, want interrupted", a.status)
	}
	last := a.lines[len(a.lines)-1]
	if last.role != "assistant" || !strings.Contains(last.text, "partial answer") || !strings.Contains(last.text, "interrupted") {
		t.Fatalf("last line should be the interrupted partial, got %+v", last)
	}
}

// TestAmbientEventsRender confirms domain events (ADR-0063 phase 4) weave into the transcript as event
// lines: a completed scan and a new finding, with the finding's title and severity.
func TestAmbientEventsRender(t *testing.T) {
	a := newApp(context.Background(), nil)
	a = step(t, a, tea.WindowSizeMsg{Width: 80, Height: 24})
	a = step(t, a, openedMsg{thread: model.Thread{ID: "t1"}, events: make(chan client.Event)})

	a = step(t, a, eventMsg{Type: "task.completed", Payload: mustJSON(client.TaskCompleted{
		Task: model.Task{CapabilityID: "grype", Status: "succeeded"}, ObservationCount: 4,
	})})
	a = step(t, a, eventMsg{Type: "finding.created", Payload: mustJSON(model.Finding{
		Title: "SQL injection in orders handler", Severity: "high",
	})})

	var joined string
	for _, ln := range a.lines {
		joined += ln.text + "\n"
	}
	if !strings.Contains(joined, "grype") || !strings.Contains(joined, "4 observations") {
		t.Fatalf("task.completed line missing: %q", joined)
	}
	if !strings.Contains(joined, "SQL injection in orders handler") || !strings.Contains(joined, "HIGH") {
		t.Fatalf("finding.created line missing title/severity: %q", joined)
	}
}

// TestApprovalBadgeViaBus confirms an approval.requested event raises the non-blocking badge and a
// matching approval.resolved clears it — the live cross-client approval flow.
func TestApprovalBadgeViaBus(t *testing.T) {
	a := newApp(context.Background(), nil)
	a = step(t, a, openedMsg{thread: model.Thread{ID: "t1"}, events: make(chan client.Event)})

	a = step(t, a, eventMsg{Type: "approval.requested", Payload: mustJSON(client.ApprovalEvent{ID: "ap1", Tool: "run_script", ThreadID: "t1"})})
	if a.pending == nil || a.pending.ID != "ap1" || !strings.Contains(a.status, "approval required") {
		t.Fatalf("approval.requested did not raise the badge: pending=%v status=%q", a.pending, a.status)
	}

	a = step(t, a, eventMsg{Type: "approval.resolved", Payload: mustJSON(client.ApprovalEvent{ID: "ap1", Decision: "approve"})})
	if a.pending != nil || !strings.Contains(a.status, "approve") {
		t.Fatalf("approval.resolved did not clear the badge: pending=%v status=%q", a.pending, a.status)
	}
}

// TestSlashCommandsAndAutocomplete confirms in-chat "/" commands route to actions instead of sending,
// and that the input is wired for autocomplete suggestions.
func TestSlashCommandsAndAutocomplete(t *testing.T) {
	a := newApp(context.Background(), nil)
	a = step(t, a, tea.WindowSizeMsg{Width: 80, Height: 24})
	a = step(t, a, openedMsg{thread: model.Thread{ID: "t1"}, events: make(chan client.Event)})

	if !a.input.ShowSuggestions || len(a.input.AvailableSuggestions()) != len(slashCommands) {
		t.Fatalf("autocomplete not wired: show=%v suggestions=%v", a.input.ShowSuggestions, a.input.AvailableSuggestions())
	}

	// /threads switches to the thread picker (does not send).
	a.input.SetValue("/threads")
	a = step(t, a, tea.KeyMsg{Type: tea.KeyEnter})
	if a.stage != stageThreads || a.sending {
		t.Fatalf("/threads: stage=%d sending=%v", a.stage, a.sending)
	}

	// /project switches to the project picker.
	a.stage = stageChat
	a.input.SetValue("/project")
	a = step(t, a, tea.KeyMsg{Type: tea.KeyEnter})
	if a.stage != stageProjects {
		t.Fatalf("/project: stage=%d, want projects", a.stage)
	}

	// /help prints a help block (returns a command) and never sends.
	a.stage = stageChat
	a.input.SetValue("/help")
	m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a = m.(app)
	if a.sending || cmd == nil {
		t.Fatalf("/help: sending=%v cmd=%v (want a print command, no send)", a.sending, cmd)
	}
	if !strings.Contains(helpText(), "/new") {
		t.Fatalf("help text missing commands: %q", helpText())
	}

	// /quit issues tea.Quit.
	a.stage = stageChat
	a.input.SetValue("/quit")
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/quit issued no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("/quit did not issue tea.Quit")
	}
}

// TestDoubleCtrlCQuits confirms the first Ctrl-C only arms the quit and the second issues tea.Quit.
func TestDoubleCtrlCQuits(t *testing.T) {
	a := newApp(context.Background(), nil)
	m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	a = m.(app)
	if !a.quitArmed || cmd != nil {
		t.Fatalf("first Ctrl-C should arm without quitting (armed=%v cmd=%v)", a.quitArmed, cmd)
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("second Ctrl-C should issue a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("second Ctrl-C command is not tea.Quit")
	}
}
