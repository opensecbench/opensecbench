package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/httpfilter"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/proxy"
)

// heldItem is one request/response paused by a "hold" rule awaiting an operator decision.
type heldItem struct {
	id     string
	seq    int
	held   proxy.Held
	decide chan proxy.Decision
}

// compiledTrafficRule is a project rule ready to run on the proxy hot path: a CEL match plus one action.
type compiledTrafficRule struct {
	enabled bool
	phase   string
	filter  *httpfilter.Filter
	action  string
	params  model.TrafficRuleParams
	re      *regexp.Regexp // compiled Params.Pattern, for replace_body
}

// compileTrafficRules validates + compiles a rule list (CEL match, and the regexp for replace_body),
// returning a per-rule error suitable for the operator. Used both to validate a PUT and to arm the proxy.
func compileTrafficRules(rules []model.TrafficRule) ([]compiledTrafficRule, error) {
	out := make([]compiledTrafficRule, 0, len(rules))
	for i, r := range rules {
		f, err := httpfilter.Compile(r.Match)
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		cr := compiledTrafficRule{enabled: r.Enabled, phase: r.Phase, filter: f, action: r.Action, params: r.Params}
		if r.Action == model.ActionReplaceBody {
			re, err := regexp.Compile(r.Params.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule %d: bad find pattern: %w", i+1, err)
			}
			cr.re = re
		}
		out = append(out, cr)
	}
	return out, nil
}

// interceptManager runs a project's traffic rules on the proxy and queues "hold" matches for the
// operator. It implements proxy.Interceptor. Rules are compiled config; the held queue is in-memory
// (in-flight traffic, never persisted). A forwarded request still becomes an exchange via capture.
type interceptManager struct {
	projectID string
	hub       *events.Hub

	mu    sync.Mutex
	rules []compiledTrafficRule
	seq   int
	holds map[string]*heldItem
	done  chan struct{}
}

func newInterceptManager(projectID string, hub *events.Hub) *interceptManager {
	return &interceptManager{projectID: projectID, hub: hub, holds: map[string]*heldItem{}, done: make(chan struct{})}
}

func (m *interceptManager) setRules(rules []compiledTrafficRule) {
	m.mu.Lock()
	m.rules = rules
	m.mu.Unlock()
}

// Enabled implements proxy.Interceptor: report whether any enabled rule targets the request and/or
// response phase, so the proxy engages the (buffering) hold path only when rules can act.
func (m *interceptManager) Enabled() (requests, responses bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rules {
		if !r.enabled {
			continue
		}
		if r.phase == model.RulePhaseRequest || r.phase == model.RulePhaseBoth {
			requests = true
		}
		if r.phase == model.RulePhaseResponse || r.phase == model.RulePhaseBoth {
			responses = true
		}
	}
	return requests, responses
}

// Hold implements proxy.Interceptor: evaluate the ordered rules for this phase against the message,
// applying modify actions in place and acting on the first terminal action (drop / hold). If no
// terminal rule matches, forward the (possibly modified) message unchanged.
func (m *interceptManager) Hold(ctx context.Context, h proxy.Held) proxy.Decision {
	phase := string(h.Phase)
	work := h
	m.mu.Lock()
	rules := m.rules
	m.mu.Unlock()

	for _, r := range rules {
		if !r.enabled || !phaseApplies(r.phase, phase) {
			continue
		}
		if !r.filter.Match(inputFrom(work, phase)) {
			continue
		}
		switch r.action {
		case model.ActionDrop:
			return proxy.Decision{Drop: true}
		case model.ActionHold:
			return m.blockHold(ctx, work) // terminal: pause for manual edit
		case model.ActionSetHeader:
			work = setHeader(work, phase, r.params.HeaderName, r.params.HeaderValue)
		case model.ActionRemoveHeader:
			work = setHeader(work, phase, r.params.HeaderName, "\x00delete")
		case model.ActionReplaceBody:
			if r.re != nil {
				work = replaceBody(work, phase, r.re, r.params.Replacement)
			}
		case model.ActionSetStatus:
			if phase == model.RulePhaseResponse {
				work.Status = r.params.Status
			}
		}
	}
	return decisionFrom(work)
}

// blockHold registers a held item, notifies subscribers, and blocks until the operator resolves it, the
// client disconnects (ctx), or the proxy drains.
func (m *interceptManager) blockHold(ctx context.Context, h proxy.Held) proxy.Decision {
	m.mu.Lock()
	m.seq++
	item := &heldItem{id: "h" + strconv.Itoa(m.seq), seq: m.seq, held: h, decide: make(chan proxy.Decision, 1)}
	m.holds[item.id] = item
	m.mu.Unlock()

	m.hub.Publish(events.Event{Type: "intercept.held", ProjectID: m.projectID, Payload: toHeldView(item)})

	var d proxy.Decision
	select {
	case d = <-item.decide:
	case <-ctx.Done():
		d = proxy.Decision{Drop: true}
	case <-m.done:
		d = proxy.Decision{Drop: true}
	}
	m.mu.Lock()
	delete(m.holds, item.id)
	m.mu.Unlock()
	m.hub.Publish(events.Event{Type: "intercept.resolved", ProjectID: m.projectID, Payload: map[string]string{"id": item.id}})
	return d
}

// resolve delivers a decision to a waiting hold. False if the id is unknown or already resolving.
func (m *interceptManager) resolve(id string, d proxy.Decision) bool {
	m.mu.Lock()
	item := m.holds[id]
	m.mu.Unlock()
	if item == nil {
		return false
	}
	select {
	case item.decide <- d:
		return true
	default:
		return false
	}
}

// drain drops every held item — called when the proxy stops so no Hold goroutine is left blocked.
func (m *interceptManager) drain() {
	m.mu.Lock()
	select {
	case <-m.done:
	default:
		close(m.done)
	}
	m.mu.Unlock()
}

func phaseApplies(rulePhase, actual string) bool {
	return rulePhase == model.RulePhaseBoth || rulePhase == actual
}

// inputFrom builds a filter input from the phase-appropriate side of the message.
func inputFrom(h proxy.Held, phase string) httpfilter.Input {
	in := httpfilter.Input{Phase: phase, Method: h.Method, URL: h.URL, Status: h.Status}
	if phase == model.RulePhaseResponse {
		in.Headers, in.Body = h.ResponseHeaders, h.ResponseBody
	} else {
		in.Headers, in.Body = h.RequestHeaders, h.RequestBody
	}
	return in
}

// decisionFrom forwards the (possibly modified) message unchanged.
func decisionFrom(h proxy.Held) proxy.Decision {
	return proxy.Decision{
		Method: h.Method, URL: h.URL,
		RequestHeaders: h.RequestHeaders, RequestBody: h.RequestBody,
		Status: h.Status, ResponseHeaders: h.ResponseHeaders, ResponseBody: h.ResponseBody,
	}
}

// setHeader adds/replaces (or, with the sentinel value, removes) a header on the phase-appropriate side.
func setHeader(h proxy.Held, phase, name, value string) proxy.Held {
	if phase == model.RulePhaseResponse {
		h.ResponseHeaders = editHeaderText(h.ResponseHeaders, name, value)
	} else {
		h.RequestHeaders = editHeaderText(h.RequestHeaders, name, value)
	}
	return h
}

func replaceBody(h proxy.Held, phase string, re *regexp.Regexp, repl string) proxy.Held {
	if phase == model.RulePhaseResponse {
		h.ResponseBody = re.ReplaceAllString(h.ResponseBody, repl)
	} else {
		h.RequestBody = re.ReplaceAllString(h.RequestBody, repl)
	}
	return h
}

// editHeaderText sets a header in "Key: value\n" text (case-insensitive, de-duplicating). The sentinel
// value "\x00delete" removes the header instead.
func editHeaderText(text, name, value string) string {
	remove := value == "\x00delete"
	var b strings.Builder
	found := false
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, _, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			if !found && !remove {
				b.WriteString(name + ": " + value + "\n")
				found = true
			}
			continue // drop the original (and any duplicates)
		}
		b.WriteString(line + "\n")
	}
	if !found && !remove {
		b.WriteString(name + ": " + value + "\n")
	}
	return b.String()
}

// --- JSON views ---

type heldView struct {
	ID              string `json:"id"`
	Phase           string `json:"phase"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	RequestHeaders  string `json:"request_headers"`
	RequestBody     string `json:"request_body"`
	Status          int    `json:"status,omitempty"`
	ResponseHeaders string `json:"response_headers,omitempty"`
	ResponseBody    string `json:"response_body,omitempty"`
}

func toHeldView(it *heldItem) heldView {
	h := it.held
	return heldView{
		ID: it.id, Phase: string(h.Phase), Method: h.Method, URL: h.URL,
		RequestHeaders: h.RequestHeaders, RequestBody: h.RequestBody,
		Status: h.Status, ResponseHeaders: h.ResponseHeaders, ResponseBody: h.ResponseBody,
	}
}

type interceptState struct {
	Held []heldView `json:"held"`
}

func (m *interceptManager) stateView() interceptState {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*heldItem, 0, len(m.holds))
	for _, it := range m.holds {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].seq < items[j].seq })
	held := make([]heldView, 0, len(items))
	for _, it := range items {
		held = append(held, toHeldView(it))
	}
	return interceptState{Held: held}
}

// --- HTTP handlers ---

func (s *Server) interceptManagerFor(projectID string) *interceptManager {
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	if lp := s.proxies[projectID]; lp != nil {
		return lp.intercept
	}
	return nil
}

// getIntercept returns the held queue (empty when the proxy is not running). Arming is gone — a project's
// rules (below) decide what holds.
func (s *Server) getIntercept(w http.ResponseWriter, r *http.Request) {
	m := s.interceptManagerFor(r.PathValue("id"))
	if m == nil {
		writeJSON(w, http.StatusOK, interceptState{Held: []heldView{}})
		return
	}
	writeJSON(w, http.StatusOK, m.stateView())
}

// resolveIntercept forwards (optionally edited) or drops one held item.
func (s *Server) resolveIntercept(w http.ResponseWriter, r *http.Request) {
	m := s.interceptManagerFor(r.PathValue("id"))
	if m == nil {
		writeErr(w, http.StatusConflict, "proxy is not running")
		return
	}
	var req struct {
		Action          string `json:"action"` // "forward" | "drop"
		Method          string `json:"method"`
		URL             string `json:"url"`
		RequestHeaders  string `json:"request_headers"`
		RequestBody     string `json:"request_body"`
		Status          int    `json:"status"`
		ResponseHeaders string `json:"response_headers"`
		ResponseBody    string `json:"response_body"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	d := proxy.Decision{Drop: req.Action == "drop"}
	if !d.Drop {
		d.Method, d.URL = req.Method, req.URL
		d.RequestHeaders, d.RequestBody = req.RequestHeaders, req.RequestBody
		d.Status, d.ResponseHeaders, d.ResponseBody = req.Status, req.ResponseHeaders, req.ResponseBody
	}
	if !m.resolve(r.PathValue("holdId"), d) {
		writeErr(w, http.StatusNotFound, "no such held item (already resolved?)")
		return
	}
	action := "forward"
	if d.Drop {
		action = "drop"
	}
	s.record(r.Context(), actorOf(r), "intercept."+action, r.PathValue("holdId"), map[string]string{"url": req.URL})
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

// listTrafficRules returns a project's persisted rules (durable, independent of the proxy running).
func (s *Server) listTrafficRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.pdb(r).ListTrafficRules(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []model.TrafficRule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

// putTrafficRules validates + replaces a project's whole ordered rule list, and re-arms a running proxy.
func (s *Server) putTrafficRules(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var rules []model.TrafficRule
	if !decodeJSON(w, r, &rules) {
		return
	}
	// Validate CEL + regexp before persisting so a bad rule is a 400, not a broken proxy.
	compiled, err := compileTrafficRules(rules)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.pdb(r).ReplaceTrafficRules(r.Context(), projectID, rules)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if m := s.interceptManagerFor(projectID); m != nil {
		m.setRules(compiled) // live re-arm
	}
	s.record(r.Context(), actorOf(r), "traffic_rules.set", projectID, map[string]int{"count": len(saved)})
	writeJSON(w, http.StatusOK, saved)
}
