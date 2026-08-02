// Package tui is the OpenSecBench terminal client (ADR-0063): a Claude-Code-style agent REPL that is a
// peer of the desktop GUI over the control-plane API. It owns no state — it presents and manipulates the
// control plane's state through pkg/client, exactly as the GUI does over the same event bus. This file
// is the Bubble Tea application: a small state machine (pick project → pick/resume thread → converse).
//
// It runs INLINE (normal buffer), not the alternate screen: finalized transcript lines are printed into
// the terminal's own scrollback via tea.Println, so native scrolling and mouse copy-paste keep working;
// Bubble Tea manages only a small live region (the streaming answer, the input, and a status line).
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/opensecbench/opensecbench/pkg/client"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// Run starts the terminal client against an already-resolved control plane and blocks until the user
// exits. The caller owns the control plane (attach or in-process spawn); the TUI only talks to pkg/client.
func Run(ctx context.Context, c *client.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the Attach stream goroutine on exit
	m := newApp(ctx, c)
	// Resolve the markdown style ONCE here, before Bubble Tea takes over stdin. Glamour's auto-style
	// queries the terminal's background over stdin; doing that inside the event loop competes with Bubble
	// Tea's input reader and can swallow keystrokes, so we detect it up front and use a fixed style.
	m.style = detectGlamourStyle()
	// No WithAltScreen: run inline so the transcript lives in native scrollback (see the package doc).
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// detectGlamourStyle picks the Glamour style from the terminal background, queried before the program
// owns input. Non-terminals resolve to a safe default without blocking.
func detectGlamourStyle() string {
	if termenv.NewOutput(os.Stdout).HasDarkBackground() {
		return "dark"
	}
	return "light"
}

// slashCommands are the in-chat commands, offered as autocomplete suggestions on the input.
var slashCommands = []string{"/help", "/new", "/threads", "/project", "/quit"}

type stage int

const (
	stageProjects stage = iota
	stageThreads
	stageChat
)

// line is one transcript entry. Keeping the transcript as flat lines (not model.Message) frees rendering
// from the message schema and unifies history with streamed events. rendered holds the Glamour-formatted
// body for assistant lines. Finalized lines are printed to scrollback; the slice is also kept as a
// journal (for the turn-end idempotency check and for tests).
type line struct {
	role     string // "user" | "assistant" | "tool" | "event" | "system"
	text     string
	rendered string
}

type app struct {
	ctx context.Context
	c   *client.Client

	stage    stage
	projects list.Model
	threads  list.Model
	project  model.Project

	// chat
	input     textinput.Model
	md        *glamour.TermRenderer
	thread    model.Thread
	lines     []line // journal of finalized lines (scrollback holds the visible copy)
	streaming string // assistant text as it types out, shown live until the turn's final message lands
	events    <-chan client.Event
	sending   bool
	pending   *model.Approval

	// cancelSend cancels the in-flight send's HTTP request, which cancels the turn server-side (the
	// sendMessage handler runs on r.Context()). interrupting distinguishes a user Esc from a real error.
	cancelSend   context.CancelFunc
	interrupting bool

	style         string // Glamour style name, resolved once in Run
	width, height int
	status        string
	quitArmed     bool
}

func newApp(ctx context.Context, c *client.Client) app {
	ti := textinput.New()
	ti.Placeholder = "Ask the Analyst…  (Enter to send · / for commands)"
	ti.Prompt = "› "
	ti.Focus()
	ti.ShowSuggestions = true
	ti.SetSuggestions(slashCommands)

	del := list.NewDefaultDelegate()
	projects := list.New(nil, del, 0, 0)
	projects.Title = "Projects"
	threads := list.New(nil, del, 0, 0)
	threads.Title = "Conversations"

	return app{ctx: ctx, c: c, projects: projects, threads: threads, input: ti, style: "dark"}
}

func (m app) Init() tea.Cmd { return loadProjects(m.ctx, m.c) }

func (m app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case errMsg:
		m.status = "error: " + msg.Error()
		return m, nil

	case projectsMsg:
		items := make([]list.Item, len(msg))
		for i, p := range msg {
			items[i] = projectItem{p}
		}
		m.projects.SetItems(items)
		return m, nil

	case threadsMsg:
		items := make([]list.Item, 0, len(msg)+1)
		items = append(items, threadItem{newConv: true}) // "＋ New conversation" always first
		for _, t := range msg {
			items = append(items, threadItem{t: t})
		}
		m.threads.SetItems(items)
		return m, nil

	case openedMsg:
		return m.openThreadView(msg)

	case eventMsg:
		return m.handleEvent(client.Event(msg))

	case streamClosedMsg:
		// ctx cancellation closes the stream on exit; nothing to do.
		return m, nil

	case sentMsg:
		return m.handleSent(msg)
	}

	// Delegate anything unclaimed to the active widget.
	return m.delegate(msg)
}

// openThreadView enters a thread: it prints a separator and the thread's history into scrollback, then
// begins draining the live stream.
func (m app) openThreadView(msg openedMsg) (tea.Model, tea.Cmd) {
	m.thread = msg.thread
	m.events = msg.events
	m.lines = m.lines[:0]
	m.streaming, m.sending, m.pending, m.status = "", false, nil, ""
	m.stage = stageChat
	m.input.Focus()

	cmds := []tea.Cmd{m.emit(line{role: "event", text: "─── " + threadLabel(msg.thread) + " ───"})}
	for _, h := range msg.history {
		if h.Role == "system" {
			continue
		}
		cmds = append(cmds, m.emit(line{role: h.Role, text: h.Content}))
	}
	cmds = append(cmds, waitForEvent(m.events))
	return m, tea.Batch(cmds...)
}

// handleSent reconciles the end of a turn: clears the in-flight flags and, for a normal completion,
// ensures the answer is committed exactly once (the live stream normally already did).
func (m app) handleSent(msg sentMsg) (tea.Model, tea.Cmd) {
	if m.cancelSend != nil {
		m.cancelSend()
		m.cancelSend = nil
	}
	m.sending = false

	if m.interrupting {
		m.interrupting = false
		m.status = "⏹ interrupted"
		if m.streaming != "" {
			cmd := m.emit(line{role: "assistant", text: m.streaming + " …_[interrupted]_"})
			m.streaming = ""
			return m, cmd
		}
		return m, nil
	}
	if msg.err != nil {
		m.status = "send failed: " + msg.err.Error()
		return m, nil
	}
	m.status = ""
	if msg.res.Pending != nil {
		m.pending = msg.res.Pending
		m.status = approvalBadge(msg.res.Pending.Tool)
	}
	if a := msg.res.Answer; a != "" {
		m.streaming = ""
		if n := len(m.lines); n == 0 || !(m.lines[n-1].role == "assistant" && m.lines[n-1].text == a) {
			return m, m.emit(line{role: "assistant", text: a})
		}
	}
	return m, nil
}

// handleKey applies app-level keys (quit) then routes to the active stage.
func (m app) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		if m.quitArmed {
			return m, tea.Quit
		}
		m.quitArmed = true
		m.status = "press Ctrl-C again to quit"
		return m, nil
	}
	m.quitArmed = false // any other key disarms the quit
	if m.status == "press Ctrl-C again to quit" {
		m.status = ""
	}

	switch m.stage {
	case stageProjects:
		if k.String() == "enter" {
			if it, ok := m.projects.SelectedItem().(projectItem); ok {
				m.project = it.p
				m.stage = stageThreads
				m.threads.Title = "Conversations · " + it.p.Name
				return m, loadThreads(m.ctx, m.c, it.p.ID)
			}
		}
	case stageThreads:
		switch k.String() {
		case "enter":
			if it, ok := m.threads.SelectedItem().(threadItem); ok {
				id := ""
				if !it.newConv {
					id = it.t.ID
				}
				m.status = "opening…"
				return m, openThread(m.ctx, m.c, m.project.ID, id, "")
			}
		case "esc":
			m.stage = stageProjects
			return m, nil
		}
	case stageChat:
		switch k.String() {
		case "enter":
			return m.submit()
		case "esc":
			// Interrupt the running turn (ADR-0063). Cancelling the send's request cancels the turn
			// server-side; a quiet chat leaves Esc as a no-op.
			if m.sending && m.cancelSend != nil {
				m.interrupting = true
				m.cancelSend()
				m.status = "interrupting…"
			}
			return m, nil
		}
	}
	return m.delegate(k)
}

// delegate forwards a message to the widget owning the current stage.
func (m app) delegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.stage {
	case stageProjects:
		m.projects, cmd = m.projects.Update(msg)
	case stageThreads:
		m.threads, cmd = m.threads.Update(msg)
	case stageChat:
		m.input, cmd = m.input.Update(msg)
	}
	return m, cmd
}

// submit handles Enter in the chat: a leading "/" runs a command, otherwise the text is sent as a turn.
func (m app) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.sending {
		return m, nil
	}
	if strings.HasPrefix(text, "/") {
		return m.runCommand(text)
	}
	sendCtx, cancel := context.WithCancel(m.ctx)
	m.cancelSend = cancel
	m.interrupting = false
	userCmd := m.emit(line{role: "user", text: text})
	m.input.SetValue("")
	m.sending = true
	m.streaming = ""
	m.status = "Analyst is working…  (Esc to interrupt)"
	return m, tea.Batch(userCmd, sendMessage(sendCtx, m.c, m.project.ID, m.thread.ID, text))
}

// runCommand executes an in-chat slash command.
func (m app) runCommand(text string) (tea.Model, tea.Cmd) {
	m.input.SetValue("")
	switch strings.Fields(text)[0] {
	case "/quit":
		return m, tea.Quit
	case "/new":
		m.status = "new conversation…"
		return m, openThread(m.ctx, m.c, m.project.ID, "", "")
	case "/threads":
		m.stage = stageThreads
		return m, loadThreads(m.ctx, m.c, m.project.ID)
	case "/project":
		m.stage = stageProjects
		return m, loadProjects(m.ctx, m.c)
	case "/help":
		m.status = ""
		return m, tea.Println(hintStyle.Render(helpText())) // print into scrollback so it's unmistakable
	default:
		m.status = "unknown command: " + text
		return m, nil
	}
}

// helpText is the in-chat command + key reference printed by /help.
func helpText() string {
	return strings.Join([]string{
		"Commands:",
		"  /new       start a new conversation",
		"  /threads   switch conversation",
		"  /project   switch project",
		"  /help      show this help",
		"  /quit      exit",
		"Keys: Enter send · Esc interrupt turn · Ctrl-C twice quit · Tab accept a suggestion",
	}, "\n")
}

func (m app) handleEvent(ev client.Event) (tea.Model, tea.Cmd) {
	var pr tea.Cmd
	switch ev.Type {
	case "analyst.delta":
		var d client.AnalystDelta
		if json.Unmarshal(ev.Payload, &d) == nil && d.ThreadID == m.thread.ID {
			m.streaming += d.Text // shown live in the region; committed on the finalizing message
		}
	case "analyst.message":
		var am client.AnalystMessage
		if json.Unmarshal(ev.Payload, &am) == nil && am.ThreadID == m.thread.ID {
			pr = m.applyStreamedMessage(am)
		}
	case "task.completed":
		var tc client.TaskCompleted
		if json.Unmarshal(ev.Payload, &tc) == nil {
			pr = m.emit(line{role: "event", text: fmt.Sprintf("┈ scan · %s · %s · %d observations", tc.Task.CapabilityID, tc.Task.Status, tc.ObservationCount)})
		}
	case "finding.created":
		var f model.Finding
		if json.Unmarshal(ev.Payload, &f) == nil {
			pr = m.emit(line{role: "event", text: fmt.Sprintf("⚑ finding · %s · %s", f.Title, strings.ToUpper(f.Severity))})
		}
	case "approval.requested":
		var ap client.ApprovalEvent
		if json.Unmarshal(ev.Payload, &ap) == nil {
			m.pending = &model.Approval{ID: ap.ID, Tool: ap.Tool}
			m.status = approvalBadge(ap.Tool)
		}
	case "approval.resolved":
		var ap client.ApprovalEvent
		if json.Unmarshal(ev.Payload, &ap) == nil && m.pending != nil && m.pending.ID == ap.ID {
			m.pending = nil
			m.status = "approval " + ap.Decision
		}
	}
	return m, tea.Batch(pr, waitForEvent(m.events)) // keep draining
}

// applyStreamedMessage commits one finalized turn message to the transcript, returning the print command
// (or nil). The assistant's completed text supersedes the live-typing buffer; the user echo is skipped
// (already shown locally on send).
func (m *app) applyStreamedMessage(am client.AnalystMessage) tea.Cmd {
	switch am.Role {
	case "assistant":
		if am.Content != "" {
			m.streaming = ""
			return m.emit(line{role: "assistant", text: am.Content})
		}
		if len(am.ToolCalls) > 0 {
			return m.emit(line{role: "tool", text: "calling " + toolCallSummary(am.ToolCalls)})
		}
	case "tool":
		label := "done"
		if am.ToolError {
			label = "error"
		}
		return m.emit(line{role: "tool", text: "↳ " + label})
	}
	return nil
}

// emit journals a finalized line and returns a command to print it into the terminal scrollback. For
// assistant lines it renders markdown through Glamour first. A blank line precedes each speaker turn
// (user/assistant) so turns breathe; tool/event sub-lines stay tucked under their turn.
func (m *app) emit(ln line) tea.Cmd {
	if ln.role == "assistant" && ln.rendered == "" {
		ln.rendered = m.renderMarkdown(ln.text)
	}
	m.lines = append(m.lines, ln)
	out := renderLine(ln)
	if ln.role == "user" || ln.role == "assistant" {
		out = "\n" + out
	}
	return tea.Println(out)
}

// renderMarkdown formats assistant text as ANSI via Glamour, falling back to the raw text if no renderer
// is ready (before the first window-size) or rendering fails.
func (m app) renderMarkdown(s string) string {
	if m.md == nil {
		return s
	}
	out, err := m.md.Render(s)
	if err != nil {
		return s
	}
	return strings.TrimRight(out, "\n")
}

// --- view (live region only; the transcript is in scrollback) ---

func (m app) View() string {
	switch m.stage {
	case stageProjects:
		return m.projects.View()
	case stageThreads:
		return m.threads.View()
	default:
		return m.chatView()
	}
}

func (m app) chatView() string {
	var b strings.Builder
	if m.streaming != "" {
		b.WriteString(assistantStyle.Render("Analyst") + "\n" + m.streaming + "\n\n")
	}
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	return b.String()
}

// statusLine is the persistent bottom line: where you are, and either the transient status or the hint.
func (m app) statusLine() string {
	left := m.project.Name + " · " + threadLabel(m.thread)
	right := m.status
	if right == "" {
		right = "Enter send · Esc interrupt · Ctrl-C twice quit · /help"
	}
	return hintStyle.Render(left + "  —  " + right)
}

// layout sizes the widgets to the terminal and (re)builds the Glamour renderer for future prints. Lines
// already in scrollback keep their original wrap — the terminal owns them now.
func (m *app) layout() {
	m.projects.SetSize(m.width, m.height)
	m.threads.SetSize(m.width, m.height)
	m.input.Width = max(10, m.width-3)
	m.md = newRenderer(m.width, m.style)
}

// newRenderer builds a Glamour renderer that word-wraps to the terminal width using a fixed style (see
// Run for why the style isn't auto-detected here). Returns nil on failure, in which case rendering falls
// back to raw text.
func newRenderer(width int, style string) *glamour.TermRenderer {
	wrap := width - 2
	if wrap < 20 {
		wrap = 20
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(wrap))
	if err != nil {
		return nil
	}
	return r
}

// --- styles & helpers ---

var (
	hintStyle      = lipgloss.NewStyle().Faint(true)
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	toolStyle      = lipgloss.NewStyle().Faint(true)
	eventStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Faint(true)
)

func approvalBadge(tool string) string {
	return "⏸ approval required: " + tool + "  (approve in the GUI or `osb approval` for now)"
}

func renderLine(ln line) string {
	switch ln.role {
	case "user":
		return userStyle.Render("› ") + ln.text
	case "assistant":
		body := ln.rendered
		if body == "" {
			body = ln.text
		}
		return assistantStyle.Render("Analyst") + "\n" + body
	case "event": // ambient domain events (scan done, new finding) woven into the scroll
		return eventStyle.Render(ln.text)
	default: // tool / system
		return toolStyle.Render("· " + ln.text)
	}
}

func threadLabel(t model.Thread) string {
	if t.Title == "" {
		return "(untitled)"
	}
	return t.Title
}

// toolCallSummary extracts the called tool names for a terse activity line.
func toolCallSummary(raw json.RawMessage) string {
	var calls []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &calls) != nil || len(calls) == 0 {
		return "running tools"
	}
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		if c.Function.Name != "" {
			names = append(names, c.Function.Name)
		} else if c.Name != "" {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return "running tools"
	}
	return strings.Join(names, ", ")
}

// projectItem and threadItem adapt domain records to list.Item.

type projectItem struct{ p model.Project }

func (i projectItem) Title() string      { return i.p.Name }
func (i projectItem) Description() string { return i.p.Status + " · " + i.p.ID }
func (i projectItem) FilterValue() string { return i.p.Name }

type threadItem struct {
	t       model.Thread
	newConv bool
}

func (i threadItem) Title() string {
	if i.newConv {
		return "＋ New conversation"
	}
	return threadLabel(i.t)
}

func (i threadItem) Description() string {
	if i.newConv {
		return "start a fresh thread in this project"
	}
	return i.t.Status + " · updated " + i.t.UpdatedAt.Format("2006-01-02 15:04")
}

func (i threadItem) FilterValue() string { return i.Title() }
