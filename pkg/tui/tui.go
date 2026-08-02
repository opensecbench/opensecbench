// Package tui is the OpenSecBench terminal client (ADR-0063): a Claude-Code-style agent REPL that is a
// peer of the desktop GUI over the control-plane API. It owns no state — it presents and manipulates the
// control plane's state through pkg/client, exactly as the GUI does over the same event bus. This file
// is the Bubble Tea application: a small state machine (pick project → pick/resume thread → converse).
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
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

type stage int

const (
	stageProjects stage = iota
	stageThreads
	stageChat
)

// line is one rendered transcript entry. Keeping the transcript as flat lines (not model.Message) frees
// rendering from the message schema and unifies history with streamed events. rendered caches the
// Glamour-formatted body for assistant lines so a long transcript isn't re-rendered on every delta.
type line struct {
	role     string // "user" | "assistant" | "tool" | "system"
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
	input     textinput.Model
	md        *glamour.TermRenderer
	thread    model.Thread
	lines     []line
	streaming string // assistant text as it types out, before the turn's final message finalizes it
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
	ti.Placeholder = "Ask the Analyst… (Enter to send)"
	ti.Prompt = "› "
	ti.Focus()

	del := list.NewDefaultDelegate()
	projects := list.New(nil, del, 0, 0)
	projects.Title = "Projects"
	projects.SetShowHelp(true)
	threads := list.New(nil, del, 0, 0)
	threads.Title = "Conversations"
	threads.SetShowHelp(true)

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
		m.thread = msg.thread
		m.events = msg.events
		m.lines = m.lines[:0]
		for _, h := range msg.history {
			if h.Role == "system" {
				continue
			}
			if h.Role == "assistant" && h.Content != "" {
				m.appendAssistant(h.Content)
			} else {
				m.lines = append(m.lines, line{role: h.Role, text: h.Content})
			}
		}
		m.streaming, m.sending, m.pending, m.status = "", false, nil, ""
		m.stage = stageChat
		m.input.Focus()
		m.refreshViewport()
		return m, waitForEvent(m.events) // begin draining the live stream

	case eventMsg:
		return m.handleEvent(client.Event(msg))

	case streamClosedMsg:
		// ctx cancellation closes the stream on exit; nothing to do.
		return m, nil

	case sentMsg:
		if m.cancelSend != nil {
			m.cancelSend() // release the request context
			m.cancelSend = nil
		}
		m.sending = false
		if m.interrupting {
			m.interrupting = false
			if m.streaming != "" {
				m.appendAssistant(m.streaming + " …_[interrupted]_")
				m.streaming = ""
			}
			m.status = "⏹ interrupted"
			m.refreshViewport()
			return m, nil
		}
		if msg.err != nil {
			m.status = "send failed: " + msg.err.Error()
			return m, nil
		}
		m.status = ""
		if msg.res.Pending != nil {
			m.pending = msg.res.Pending
			m.status = "⏸ approval required: " + msg.res.Pending.Tool + "  (approve in the GUI or `osb approval` for now)"
		}
		// Reconcile the turn's end: ensure the answer is present exactly once even if the stream's final
		// message was missed (an SSH blip at turn end). The live stream normally already added it.
		if a := msg.res.Answer; a != "" {
			m.streaming = ""
			if n := len(m.lines); n == 0 || !(m.lines[n-1].role == "assistant" && m.lines[n-1].text == a) {
				m.appendAssistant(a)
			}
		}
		m.refreshViewport()
		return m, nil
	}

	// Delegate anything unclaimed to the active widget.
	return m.delegate(msg)
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
				title := ""
				id := ""
				if !it.newConv {
					id = it.t.ID
				}
				m.status = "opening…"
				return m, openThread(m.ctx, m.c, m.project.ID, id, title)
			}
		case "esc":
			m.stage = stageProjects
			return m, nil
		}
	case stageChat:
		switch k.String() {
		case "enter":
			return m.send()
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
			m.refreshViewport()
		}
	}
	return m, waitForEvent(m.events) // keep draining
}

// applyStreamedMessage folds one finalized turn message into the transcript. The assistant's completed
// text supersedes the live-typing buffer; the user echo is skipped (already shown locally on send).
func (m *app) applyStreamedMessage(am client.AnalystMessage) {
	switch am.Role {
	case "assistant":
		if am.Content != "" {
			m.streaming = ""
			m.appendAssistant(am.Content)
		} else if len(am.ToolCalls) > 0 {
			m.lines = append(m.lines, line{role: "tool", text: "calling " + toolCallSummary(am.ToolCalls)})
		}
	case "tool":
		// A tool result landed; keep it terse in the scroll (the detail is the agent's next message).
		label := "done"
		if am.ToolError {
			label = "error"
		}
		m.lines = append(m.lines, line{role: "tool", text: "↳ " + label})
	case "user":
		// already rendered locally
	}
}

func (m app) send() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.sending {
		return m, nil
	}
	sendCtx, cancel := context.WithCancel(m.ctx)
	m.cancelSend = cancel
	m.interrupting = false
	m.lines = append(m.lines, line{role: "user", text: text})
	m.input.SetValue("")
	m.sending = true
	m.streaming = ""
	m.status = "Analyst is working… (Esc to interrupt)"
	m.refreshViewport()
	return m, sendMessage(sendCtx, m.c, m.project.ID, m.thread.ID, text)
}

// appendAssistant adds an assistant message, caching its Glamour-rendered body so the transcript isn't
// re-rendered on every subsequent delta.
func (m *app) appendAssistant(text string) {
	m.lines = append(m.lines, line{role: "assistant", text: text, rendered: m.renderMarkdown(text)})
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

func (m app) chatView() string {
	header := headerStyle.Render(fmt.Sprintf("opensecbench · %s · %s", m.project.Name, threadLabel(m.thread)))
	status := m.status
	if status == "" {
		status = hintStyle.Render("Enter send · Ctrl-C twice quit")
	} else {
		status = hintStyle.Render(status)
	}
	return strings.Join([]string{header, m.vp.View(), m.input.View(), status}, "\n")
}

func (m *app) refreshViewport() {
	m.vp.SetContent(m.transcript())
	m.vp.GotoBottom()
}

func (m app) transcript() string {
	var b strings.Builder
	for _, ln := range m.lines {
		b.WriteString(renderLine(ln))
		b.WriteString("\n\n")
	}
	if m.streaming != "" {
		b.WriteString(roleLabel("assistant"))
		b.WriteString("\n")
		b.WriteString(m.streaming)
		b.WriteString("\n\n")
	}
	return b.String()
}

// layout sizes the widgets to the terminal. Chat reserves a header, an input line, and a status line.
// A width change rebuilds the Glamour renderer and re-renders cached assistant bodies at the new wrap.
func (m *app) layout() {
	m.projects.SetSize(m.width, m.height)
	m.threads.SetSize(m.width, m.height)
	m.input.Width = m.width - 3
	if m.vp.Width == 0 {
		m.vp = viewport.New(m.width, max(1, m.height-3))
	} else {
		m.vp.Width, m.vp.Height = m.width, max(1, m.height-3)
	}
	m.md = newRenderer(m.width, m.style)
	for i := range m.lines {
		if m.lines[i].role == "assistant" {
			m.lines[i].rendered = m.renderMarkdown(m.lines[i].text)
		}
	}
	m.refreshViewport()
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
	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	hintStyle      = lipgloss.NewStyle().Faint(true)
	userStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	assistantStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	toolStyle      = lipgloss.NewStyle().Faint(true)
)

func roleLabel(role string) string {
	switch role {
	case "user":
		return userStyle.Render("›")
	case "assistant":
		return assistantStyle.Render("Analyst")
	default:
		return toolStyle.Render("·")
	}
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

func (i projectItem) Title() string       { return i.p.Name }
func (i projectItem) Description() string  { return i.p.Status + " · " + i.p.ID }
func (i projectItem) FilterValue() string  { return i.p.Name }

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
