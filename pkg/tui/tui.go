// Package tui is the OpenSecBench terminal client (ADR-0063): a Claude-Code-style agent REPL that is a
// peer of the desktop GUI over the control-plane API. It owns no state — it presents and manipulates the
// control plane's state through pkg/client, exactly as the GUI does over the same event bus. This file
// is the Bubble Tea application: a small state machine (pick project → pick/resume thread → converse).
//
// The chat uses a bottom-anchored full-screen layout: a scrolling transcript fills the top, a divider,
// and a wrapping input pinned at the bottom with a status line below it (like Claude Code's CLI).
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
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
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
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

// slashCommands are the in-chat commands (typed at the input, e.g. "/help").
var slashCommands = []string{"/help", "/new", "/threads", "/project", "/quit"}

const maxInputRows = 6 // the input grows with wrapped content up to this many rows

type stage int

const (
	stageProjects stage = iota
	stageThreads
	stageChat
)

// line is one transcript entry. Keeping the transcript as flat lines (not model.Message) frees rendering
// from the message schema and unifies history with streamed events. rendered holds the Glamour-formatted
// body for assistant lines so a long transcript isn't re-rendered on every delta.
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
	vp        viewport.Model
	input     textarea.Model
	md        *glamour.TermRenderer
	thread    model.Thread
	lines     []line
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
	ta := textarea.New()
	ta.Placeholder = "Ask the Analyst…  (Enter to send · / for commands)"
	ta.Prompt = "› "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.Focus()
	// Enter is intercepted for send (see handleKey), so the textarea's newline binding never fires; the
	// textarea is here for its word-wrapping of long input, which a single-line field can't do.

	del := list.NewDefaultDelegate()
	projects := list.New(nil, del, 0, 0)
	projects.Title = "Projects"
	threads := list.New(nil, del, 0, 0)
	threads.Title = "Conversations"

	return app{ctx: ctx, c: c, projects: projects, threads: threads, input: ta, style: "dark"}
}

func (m app) Init() tea.Cmd { return loadProjects(m.ctx, m.c) }

func (m app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.md = newRenderer(m.width, m.style)
		m.projects.SetSize(m.width, m.height)
		m.threads.SetSize(m.width, m.height)
		m.resizeChat()
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
		m.thread = msg.thread
		m.events = msg.events
		m.lines = m.lines[:0]
		m.emit(line{role: "event", text: "─── " + threadLabel(msg.thread) + " ───"})
		for _, h := range msg.history {
			if h.Role == "system" {
				continue
			}
			m.emit(line{role: h.Role, text: h.Content})
		}
		m.streaming, m.sending, m.pending, m.status = "", false, nil, ""
		m.stage = stageChat
		m.resizeChat()
		m.vp.GotoBottom()
		return m, tea.Batch(m.input.Focus(), waitForEvent(m.events))

	case eventMsg:
		return m.handleEvent(client.Event(msg))

	case streamClosedMsg:
		return m, nil

	case sentMsg:
		return m.handleSent(msg)
	}

	return m.delegate(msg)
}

// handleSent reconciles the end of a turn: clears the in-flight flags and, for a normal completion,
// ensures the answer is present exactly once (the live stream normally already added it).
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
			m.emit(line{role: "assistant", text: m.streaming + " …_[interrupted]_"})
			m.streaming = ""
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
			m.emit(line{role: "assistant", text: a})
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
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(k)
			return m, cmd
		}
	}
	return m.delegate(k)
}

// delegate forwards a message to the widget owning the current stage; in chat, a keystroke may grow the
// wrapped input, so re-size afterward.
func (m app) delegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.stage {
	case stageProjects:
		m.projects, cmd = m.projects.Update(msg)
	case stageThreads:
		m.threads, cmd = m.threads.Update(msg)
	case stageChat:
		m.input, cmd = m.input.Update(msg)
		m.resizeChat()
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
	m.emit(line{role: "user", text: text})
	m.input.SetValue("")
	m.sending = true
	m.streaming = ""
	m.status = "Analyst is working…  (Esc to interrupt)"
	m.resizeChat()
	return m, sendMessage(sendCtx, m.c, m.project.ID, m.thread.ID, text)
}

// runCommand executes an in-chat slash command.
func (m app) runCommand(text string) (tea.Model, tea.Cmd) {
	m.input.SetValue("")
	m.resizeChat()
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
		m.emit(line{role: "event", text: helpText()})
		return m, nil
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
		"Keys: Enter send · Esc interrupt turn · PgUp/PgDn scroll · Ctrl-C twice quit",
	}, "\n")
}

func (m app) handleEvent(ev client.Event) (tea.Model, tea.Cmd) {
	switch ev.Type {
	case "analyst.delta":
		var d client.AnalystDelta
		if json.Unmarshal(ev.Payload, &d) == nil && d.ThreadID == m.thread.ID {
			m.streaming += d.Text
			m.refreshViewport()
		}
	case "analyst.message":
		var am client.AnalystMessage
		if json.Unmarshal(ev.Payload, &am) == nil && am.ThreadID == m.thread.ID {
			m.applyStreamedMessage(am)
		}
	case "task.completed":
		var tc client.TaskCompleted
		if json.Unmarshal(ev.Payload, &tc) == nil {
			m.emit(line{role: "event", text: fmt.Sprintf("┈ scan · %s · %s · %d observations", tc.Task.CapabilityID, tc.Task.Status, tc.ObservationCount)})
		}
	case "finding.created":
		var f model.Finding
		if json.Unmarshal(ev.Payload, &f) == nil {
			m.emit(line{role: "event", text: fmt.Sprintf("⚑ finding · %s · %s", f.Title, strings.ToUpper(f.Severity))})
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
	return m, waitForEvent(m.events) // keep draining
}

// applyStreamedMessage commits one finalized turn message to the transcript. The assistant's completed
// text supersedes the live-typing buffer; the user echo is skipped (already shown locally on send).
func (m *app) applyStreamedMessage(am client.AnalystMessage) {
	switch am.Role {
	case "assistant":
		if am.Content != "" {
			m.streaming = ""
			m.emit(line{role: "assistant", text: am.Content})
		} else if len(am.ToolCalls) > 0 {
			m.emit(line{role: "tool", text: "calling " + toolCallSummary(am.ToolCalls)})
		}
	case "tool":
		label := "done"
		if am.ToolError {
			label = "error"
		}
		m.emit(line{role: "tool", text: "↳ " + label})
	}
}

// emit appends a finalized line (rendering assistant markdown once) and refreshes the transcript view.
func (m *app) emit(ln line) {
	if ln.role == "assistant" && ln.rendered == "" {
		ln.rendered = m.renderMarkdown(ln.text)
	}
	m.lines = append(m.lines, ln)
	m.refreshViewport()
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

// --- view ---

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

// chatView stacks the scrolling transcript, a divider, the input, and the status line so the input is
// pinned at the bottom of the screen.
func (m app) chatView() string {
	divider := hintStyle.Render(strings.Repeat("─", max(1, m.width)))
	return strings.Join([]string{m.vp.View(), divider, m.input.View(), m.statusLine()}, "\n")
}

// statusLine is the persistent bottom line: where you are, and either the transient status or the hint.
func (m app) statusLine() string {
	left := m.project.Name + " · " + threadLabel(m.thread)
	right := m.status
	if right == "" {
		right = "Enter send · Esc interrupt · Ctrl-C twice quit · /help"
	}
	return hintStyle.Render(truncate(left+"  —  "+right, max(1, m.width)))
}

// resizeChat sizes the transcript and input to the terminal, growing the input with wrapped content and
// giving the rest to the viewport. Called on window resize and whenever the input changes.
func (m *app) resizeChat() {
	if m.width == 0 || m.height == 0 {
		return
	}
	ih := m.inputRows()
	m.input.SetWidth(m.width)
	m.input.SetHeight(ih)
	// height = total − input − divider(1) − status(1)
	vpH := max(1, m.height-ih-2)
	m.vp.Width = m.width
	m.vp.Height = vpH
	m.refreshViewport()
}

// inputRows is the input's height: the number of wrapped rows its content needs, clamped.
func (m app) inputRows() int {
	rows := wrappedRows(m.input.Value(), max(1, m.width-2))
	if rows < 1 {
		rows = 1
	}
	if rows > maxInputRows {
		rows = maxInputRows
	}
	return rows
}

// refreshViewport re-renders the transcript, keeping the view at the bottom if it was already there so
// live output follows along but scrolling up to read isn't yanked back down.
func (m *app) refreshViewport() {
	if m.vp.Width == 0 {
		return
	}
	atBottom := m.vp.AtBottom()
	m.vp.SetContent(m.transcript())
	if atBottom {
		m.vp.GotoBottom()
	}
}

func (m app) transcript() string {
	var b strings.Builder
	for i, ln := range m.lines {
		if i > 0 && (ln.role == "user" || ln.role == "assistant") {
			b.WriteByte('\n') // blank line before each speaker turn so turns breathe
		}
		b.WriteString(renderLine(ln))
		b.WriteByte('\n')
	}
	if m.streaming != "" {
		b.WriteString("\n" + assistantStyle.Render("Analyst") + "\n" + m.streaming + "\n")
	}
	return b.String()
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

// wrappedRows estimates how many terminal rows a string occupies when soft-wrapped to width.
func wrappedRows(s string, width int) int {
	if width < 1 {
		width = 1
	}
	rows := 0
	for _, logical := range strings.Split(s, "\n") {
		w := lipgloss.Width(logical)
		if w == 0 {
			rows++
		} else {
			rows += (w + width - 1) / width
		}
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// truncate clips a single line to width, appending an ellipsis when it overflows.
func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width || width < 1 {
		return s
	}
	if width <= 1 {
		return "…"
	}
	r := []rune(s)
	if len(r) > width-1 {
		r = r[:width-1]
	}
	return string(r) + "…"
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

func (i projectItem) Title() string       { return i.p.Name }
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
