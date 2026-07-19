package api

import (
	"context"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/proxy"
	"github.com/opensecbench/opensecbench/pkg/scope"
)

// liveProxy is a running per-project intercepting proxy plus its intercept manager (holds live with
// the proxy — draining them is part of stopping it).
type liveProxy struct {
	srv       *http.Server
	port      int
	intercept *interceptManager
}

// proxyStatus is the JSON view of a project's proxy.
type proxyStatus struct {
	Running bool   `json:"running"`
	Port    int    `json:"port,omitempty"`
	CASPKI  string `json:"ca_spki_sha256,omitempty"` // for a browser's --ignore-certificate-errors-spki-list
}

func (s *Server) caSPKI() string {
	if s.proxyCA == nil {
		return ""
	}
	return s.proxyCA.SPKISHA256()
}

// proxyCACert serves the proxy CA certificate for the operator to trust in their browser/tools.
func (s *Server) proxyCACert(w http.ResponseWriter, _ *http.Request) {
	if s.proxyCA == nil {
		writeErr(w, http.StatusServiceUnavailable, "proxy CA unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="opensecbench-ca.crt"`)
	_, _ = w.Write(s.proxyCA.CertPEM())
}

func (s *Server) getProxy(w http.ResponseWriter, r *http.Request) {
	s.proxyMu.Lock()
	lp := s.proxies[r.PathValue("id")]
	s.proxyMu.Unlock()
	if lp == nil {
		writeJSON(w, http.StatusOK, proxyStatus{Running: false, CASPKI: s.caSPKI()})
		return
	}
	writeJSON(w, http.StatusOK, proxyStatus{Running: true, Port: lp.port, CASPKI: s.caSPKI()})
}

// startProxy opens an intercepting proxy bound to a project on a loopback port. Captured traffic
// becomes http_exchange rows (origin=proxy); the project scope allowlist gates hosts.
func (s *Server) startProxy(w http.ResponseWriter, r *http.Request) {
	if s.proxyCA == nil {
		writeErr(w, http.StatusServiceUnavailable, "proxy unavailable")
		return
	}
	projectID := r.PathValue("id")
	if _, err := s.store.GetProject(r.Context(), projectID); err != nil {
		writeErr(w, http.StatusBadRequest, "unknown project")
		return
	}
	var req struct {
		Port int `json:"port"`
	}
	_ = decodeJSONOptional(r, &req)

	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	if lp := s.proxies[projectID]; lp != nil {
		writeJSON(w, http.StatusOK, proxyStatus{Running: true, Port: lp.port, CASPKI: s.caSPKI()})
		return
	}

	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(req.Port))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "listen: "+err.Error())
		return
	}
	mgr := newInterceptManager(projectID, s.events)
	px := proxy.New(s.proxyCA, s.proxyCapture(projectID), s.projectAllows(projectID), mgr, s.ruleEngineFor(projectID))
	srv := &http.Server{Handler: px, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	port := ln.Addr().(*net.TCPAddr).Port
	s.proxies[projectID] = &liveProxy{srv: srv, port: port, intercept: mgr}
	s.record(r.Context(), actorOf(r), "proxy.start", projectID, map[string]int{"port": port})
	st := proxyStatus{Running: true, Port: port, CASPKI: s.caSPKI()}
	s.events.Publish(events.Event{Type: "proxy", ProjectID: projectID, Payload: st})
	writeJSON(w, http.StatusCreated, st)
}

func (s *Server) stopProxy(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	s.proxyMu.Lock()
	lp := s.proxies[projectID]
	delete(s.proxies, projectID)
	s.proxyMu.Unlock()
	if lp == nil {
		writeJSON(w, http.StatusOK, proxyStatus{Running: false})
		return
	}
	// Release any held requests before shutting the server down, or Shutdown blocks on the parked
	// handlers forever.
	lp.intercept.drain()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_ = lp.srv.Shutdown(ctx)
	s.record(r.Context(), actorOf(r), "proxy.stop", projectID, map[string]int{"port": lp.port})
	s.events.Publish(events.Event{Type: "proxy", ProjectID: projectID, Payload: proxyStatus{Running: false, CASPKI: s.caSPKI()}})
	writeJSON(w, http.StatusOK, proxyStatus{Running: false})
}

// proxyCapture persists a captured exchange (origin=proxy) for the project.
func (s *Server) proxyCapture(projectID string) func(proxy.Exchange) {
	return func(e proxy.Exchange) {
		ctx := context.Background()
		ex, err := s.store.CreateExchange(ctx, model.HTTPExchange{
			ProjectID:      projectID,
			Origin:         model.ExchangeProxy,
			Method:         e.Method,
			URL:            e.URL,
			RequestHeaders: e.RequestHeaders,
			RequestBody:    e.RequestBody,
		})
		if err != nil {
			log.Printf("proxy capture: %v", err)
			return
		}
		if err := s.store.RecordResponse(ctx, ex.ID, e.Status, e.ResponseHeaders, e.ResponseBody, e.DurationMS); err != nil {
			log.Printf("proxy capture response: %v", err)
		}
		s.publishExchange(ctx, projectID, ex.ID)
	}
}

// publishExchange emits the current, complete state of an exchange to subscribers (SSE). Best-effort:
// a fetch failure just skips the live update, which the client's next fetch reconciles.
func (s *Server) publishExchange(ctx context.Context, projectID, id string) {
	full, err := s.store.GetExchange(ctx, id)
	if err != nil {
		return
	}
	s.events.Publish(events.Event{Type: "exchange", ProjectID: projectID, Payload: full})
}

// projectAllows returns a host gate enforcing the project's scope allowlist (empty = allow all).
func (s *Server) projectAllows(projectID string) func(string) bool {
	return func(host string) bool {
		entries, err := s.store.ListScopeEntries(context.Background(), projectID)
		if err != nil || len(entries) == 0 {
			return true
		}
		rules := make([]scope.Entry, len(entries))
		for i, e := range entries {
			rules[i] = scope.Entry{Kind: e.Kind, Value: e.Value}
		}
		return scope.Check(rules, host) == nil
	}
}

// shutdownProxies stops any running proxies (called on control-plane shutdown).
func (s *Server) shutdownProxies() {
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	for id, lp := range s.proxies {
		lp.intercept.drain()
		_ = lp.srv.Close()
		delete(s.proxies, id)
	}
}

// Close releases live resources: running proxies and open terminal sessions (finalizing their
// transcripts). Called by the control plane on shutdown.
func (s *Server) Close() {
	if s.schedCancel != nil {
		s.schedCancel()
	}
	if s.engine != nil {
		s.engine.Close()
	}
	s.shutdownProxies()
	s.sessMu.Lock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.sessMu.Unlock()
	for _, id := range ids {
		s.finalizeSession(id)
	}
}
