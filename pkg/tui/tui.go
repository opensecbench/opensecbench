// Package tui is the OpenSecBench terminal client (ADR-0063): a Claude-Code-style agent REPL that is a
// peer of the desktop GUI over the control-plane API. It owns no state — it presents and manipulates the
// control plane's state through pkg/client, exactly as the GUI does over the same event bus. This file
// is the Bubble Tea application: a small state machine (pick project → pick/resume thread → converse).
//
// The chat runs INLINE (normal buffer), not the alternate screen: finalized transcript lines are printed
// into the terminal's own scrollback via tea.Println, so native scrolling and mouse copy keep working.
// Bubble Tea manages only a small live region at the bottom (a divider, the streaming answer, the input,
// and a status line). On entering a thread we print blanks to push that region to the screen bottom, so
// the input is bottom-anchored like Claude Code rather than floating after short content.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/opensecbench/opensecbench/pkg/client"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// Run starts the terminal client against an already-resolved control plane and blocks until the user
// exits. The caller owns the control plane (attach or in-process spawn); the TUI only talks to pkg/client.
// opts carries the working-directory context (offer/open a dir-local project).
func Run(ctx context.Context, c *client.Client, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the Attach stream goroutine on exit
	m := newApp(ctx, c)
	m.opts = opts
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

// slashCommands are the in-chat commands (typed at the input, e.g. "/help").
var slashCommands = []string{"/search", "/findings", "/observations", "/help", "/new", "/threads", "/project", "/quit"}

const maxInputRows = 6 // the input grows with wrapped content up to this many rows

// liveChromeRows is the non-input height of the live region: the divider plus the status line.
const liveChromeRows = 2

type stage int

const (
	stageProjects stage = iota
	stageThreads
	stageChat
)

// line is one transcript entry. Keeping the transcript as flat lines (not model.Message) frees rendering
// from the message schema and unifies history with streamed events. rendered holds the Glamour-formatted
// body for assistant lines. Finalized lines are printed to scrollback; the slice is also kept as a
// journal (for the turn-end idempotency check, the bottom-fill measurement, and tests).
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
	input     textarea.Model
	md        *glamour.TermRenderer
	thread    model.Thread
	lines     []line // journal of finalized lines (the visible copy lives in scrollback)
	streaming string // assistant text as it types out, shown live until the turn's final message lands
	events    <-chan client.Event
	sending   bool
	pending   *model.Approval

	// cancelSend cancels the in-flight send's HTTP request, which cancels the turn server-side (the
	// sendMessage handler runs on r.Context()). interrupting distinguishes a user Esc from a real error.
	cancelSend   context.CancelFunc
	interrupting bool

	opts          Options // working-directory context (dir-local project)
	style         string  // Glamour style name, resolved once in Run
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
		m.input.SetWidth(m.width)
		m.input.SetHeight(m.inputRows())
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case errMsg:
		m.status = "error: " + msg.Error()
		return m, nil

	case projectsMsg:
		items := make([]list.Item, 0, len(msg)+1)
		if m.opts.Cwd != "" {
			items = append(items, createHereItem{cwd: m.opts.Cwd}) // "＋ New project in this directory"
		}
		for _, p := range msg {
			items = append(items, projectItem{p})
		}
		m.projects.SetItems(items)
		// A directory bound to a project (via the .opensecbench marker) opens straight into it.
		if m.opts.OpenProjectID != "" {
			for _, p := range msg {
				if p.ID == m.opts.OpenProjectID {
					m.opts.OpenProjectID = "" // consume so a re-list doesn't re-trigger
					return m.enterProject(p)
				}
			}
		}
		return m, nil

	case projectCreatedMsg:
		return m.enterProject(msg.project)

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
		return m, nil

	case sentMsg:
		return m.handleSent(msg)

	case findingsMsg:
		if msg.err != nil {
			m.status = "findings: " + msg.err.Error()
			return m, nil
		}
		return m, m.emit(line{role: "event", text: formatFindings(msg.items)})

	case observationsMsg:
		if msg.err != nil {
			m.status = "observations: " + msg.err.Error()
			return m, nil
		}
		return m, m.emit(line{role: "event", text: formatObservations(msg.items)})

	case searchMsg:
		if msg.err != nil {
			m.status = "search: " + msg.err.Error()
			return m, nil
		}
		return m, m.emit(line{role: "event", text: formatSearch(msg.query, msg.items)})
	}

	return m.delegate(msg)
}

// openThreadView enters a thread: it prints a separator and the thread's history into scrollback, fills
// blanks so the input starts at the screen bottom, then begins draining the live stream. The prints run
// in sequence (blanks first) so ordering is deterministic.
func (m app) openThreadView(msg openedMsg) (tea.Model, tea.Cmd) {
	m.thread = msg.thread
	m.events = msg.events
	m.lines = m.lines[:0]
	m.streaming, m.sending, m.pending, m.status = "", false, nil, ""
	m.stage = stageChat
	m.input.SetHeight(m.inputRows())

	var prints []tea.Cmd
	prints = append(prints, m.emit(line{role: "event", text: "─── " + threadLabel(msg.thread) + " ───"}))
	for _, h := range msg.history {
		if h.Role == "system" {
			continue
		}
		prints = append(prints, m.emit(line{role: h.Role, text: h.Content}))
	}
	seq := prints
	if fill := m.fillToBottom(); fill != nil {
		seq = append([]tea.Cmd{fill}, prints...) // blanks first, so content ends at the bottom
	}
	return m, tea.Batch(tea.Sequence(seq...), m.input.Focus(), waitForEvent(m.events))
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
			cmd := m.emit(line{role: "assistant", text: a})
			return m, cmd
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
			switch it := m.projects.SelectedItem().(type) {
			case createHereItem:
				m.status = "creating project…"
				return m, createLocalProject(m.ctx, m.c, it.cwd)
			case projectItem:
				return m.enterProject(it.p)
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
		// A pending approval is modal: decide before composing anything else.
		if m.pending != nil {
			switch k.String() {
			case "y", "Y":
				return m.decide("approve")
			case "n", "N", "esc":
				return m.decide("deny")
			default:
				return m, nil // awaiting a decision; ignore other input
			}
		}
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

// enterProject selects a project and moves to its conversation picker.
func (m app) enterProject(p model.Project) (tea.Model, tea.Cmd) {
	m.project = p
	m.stage = stageThreads
	m.threads.Title = "Conversations · " + p.Name
	return m, loadThreads(m.ctx, m.c, p.ID)
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
		m.input.SetHeight(m.inputRows())
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
	m.input.SetHeight(m.inputRows())
	m.sending = true
	m.streaming = ""
	m.status = "Analyst is working…  (Esc to interrupt)"
	return m, tea.Batch(userCmd, sendMessage(sendCtx, m.c, m.project.ID, m.thread.ID, text))
}

// decide approves or denies the pending gated tool and resumes the turn from the terminal (ADR-0063), so
// an SSH-only user never has to switch to the GUI to unblock the agent.
func (m app) decide(decision string) (tea.Model, tea.Cmd) {
	if m.pending == nil {
		return m, nil
	}
	id, tool := m.pending.ID, m.pending.Tool
	m.pending = nil
	verb := "approved"
	if decision == "deny" {
		verb = "denied"
	}
	actCmd := m.emit(line{role: "tool", text: verb + " " + tool})
	sendCtx, cancel := context.WithCancel(m.ctx)
	m.cancelSend = cancel
	m.interrupting = false
	m.sending = true
	m.streaming = ""
	m.status = "Analyst is working…  (Esc to interrupt)"
	return m, tea.Batch(actCmd, decideApproval(sendCtx, m.c, m.project.ID, id, decision))
}

// runCommand executes an in-chat slash command.
func (m app) runCommand(text string) (tea.Model, tea.Cmd) {
	m.input.SetValue("")
	m.input.SetHeight(m.inputRows())
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
	case "/findings":
		return m, loadFindings(m.ctx, m.c)
	case "/observations", "/obs":
		return m, loadObservations(m.ctx, m.c, m.project.ID)
	case "/search":
		q := strings.TrimSpace(strings.TrimPrefix(text, "/search"))
		if q == "" {
			m.status = "usage: /search <query>"
			return m, nil
		}
		return m, loadSearch(m.ctx, m.c, m.project.ID, q)
	case "/help":
		return m, m.emit(line{role: "event", text: helpText()})
	default:
		m.status = "unknown command: " + text
		return m, nil
	}
}

// helpText is the in-chat command + key reference printed by /help.
func helpText() string {
	return strings.Join([]string{
		"Commands:",
		"  /search <q>    search everywhere in this project (local read — no LLM)",
		"  /findings      list findings",
		"  /observations  list this project's observations (alias /obs)",
		"  /new           start a new conversation",
		"  /threads       switch conversation",
		"  /project       switch project",
		"  /help          show this help",
		"  /quit          exit",
		"Keys: Enter send · Esc interrupt turn · Ctrl-C twice quit",
	}, "\n")
}

// formatFindings renders a findings list as a compact block for the transcript.
func formatFindings(fs []model.Finding) string {
	if len(fs) == 0 {
		return "findings: none"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "findings (%d):", len(fs))
	for _, f := range fs {
		fmt.Fprintf(&b, "\n  %-6s %-10s %s", strings.ToUpper(f.Severity), f.Status, f.Title)
	}
	return b.String()
}

// formatSearch renders omni-search hits (grouped by kind's natural order) as a compact block.
func formatSearch(query string, hits []model.SearchResult) string {
	if len(hits) == 0 {
		return fmt.Sprintf("search %q: no matches", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "search %q (%d):", query, len(hits))
	for _, h := range hits {
		detail := h.Detail
		if detail != "" {
			detail = "  " + detail
		}
		fmt.Fprintf(&b, "\n  %-12s %s%s", h.Kind, h.Title, detail)
	}
	return b.String()
}

// formatObservations renders an observations list as a compact block for the transcript.
func formatObservations(obs []model.Observation) string {
	if len(obs) == 0 {
		return "observations: none"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "observations (%d):", len(obs))
	for _, o := range obs {
		loc := o.Location
		if loc != "" {
			loc = "  " + loc
		}
		fmt.Fprintf(&b, "\n  %-6s %-11s %s%s", strings.ToUpper(o.Severity), o.ReviewState, o.Title, loc)
	}
	return b.String()
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
// assistant lines it renders markdown through Glamour first. A blank line precedes each speaker turn so
// turns breathe; tool/event sub-lines stay tucked under their turn.
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

// chatView is the bottom live region: the in-progress answer (if any) above a divider, then the input,
// then the status line. Finalized transcript lines are in scrollback above this.
func (m app) chatView() string {
	var b strings.Builder
	if m.streaming != "" {
		b.WriteString(assistantStyle.Render("Analyst") + "\n" + m.streaming + "\n\n")
	}
	if m.pending != nil {
		b.WriteString(m.approvalPrompt())
	}
	b.WriteString(hintStyle.Render(strings.Repeat("─", max(1, m.width))))
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	return b.String()
}

// approvalPrompt is the modal block shown while a gated tool awaits a decision: the tool, its args, and
// the y/n keys. Rendered above the divider so it sits right over the input.
func (m app) approvalPrompt() string {
	var b strings.Builder
	b.WriteString(approvalStyle.Render("⏸ Approve  "+m.pending.Tool+"  ?") + "\n")
	if args := compactArgs(m.pending.Args); args != "" {
		b.WriteString(hintStyle.Render("  "+truncate(args, max(1, m.width-2))) + "\n")
	}
	b.WriteString(hintStyle.Render("  [y] approve   [n] deny") + "\n")
	return b.String()
}

// compactArgs renders a tool call's args for the approval prompt, or "" when there's nothing meaningful.
func compactArgs(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "{}" || s == "null" {
		return ""
	}
	return s
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

// inputRows is the input's height: the number of wrapped rows its content needs, clamped.
func (m app) inputRows() int {
	rows := wrappedRows(m.input.Value(), max(1, m.width-2))
	if rows > maxInputRows {
		rows = maxInputRows
	}
	return rows
}

// fillToBottom returns a command that prints enough blank lines to push the live region to the bottom of
// the screen on thread open — so the input is bottom-anchored rather than floating after short content.
// nil when the content already fills the screen.
func (m app) fillToBottom() tea.Cmd {
	blanks := m.height - m.contentRows() - m.inputRows() - liveChromeRows
	if blanks <= 0 {
		return nil
	}
	return tea.Println(strings.Repeat("\n", blanks-1)) // Println adds the final newline
}

// contentRows estimates how many terminal rows the current transcript occupies, matching emit's spacing.
func (m app) contentRows() int {
	rows := 0
	for i, ln := range m.lines {
		if i > 0 && (ln.role == "user" || ln.role == "assistant") {
			rows++ // the blank line emit prepends before a turn
		}
		rows += wrappedRows(renderLine(ln), max(1, m.width))
	}
	return rows
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
	approvalStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
)

func approvalBadge(tool string) string {
	return "⏸ " + tool + " needs approval — y approve · n deny"
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

// createHereItem is the project-picker entry that creates a new dir-local project in the cwd.
type createHereItem struct{ cwd string }

func (i createHereItem) Title() string { return "＋ New project in " + filepath.Base(i.cwd) }
func (i createHereItem) Description() string {
	return "create it in " + filepath.Join(i.cwd, projectDirName)
}
func (i createHereItem) FilterValue() string { return "new project" }

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
