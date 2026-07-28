package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/runnerhub"
	"github.com/opensecbench/opensecbench/pkg/runnertunnel"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Remote runners (ADR-0024). Operator actions (enroll-token, list, delete) live on the trusted loopback
// API. The runner protocol (enroll, stream, result) is served by RunnerHandler on a separate,
// network-exposed listener and authenticated per-request by an ed25519 signature.

// --- operator actions (loopback API) ---

// listRunners returns enrolled runners with live online status from the hub.
func (s *Server) listRunners(w http.ResponseWriter, r *http.Request) {
	rs, err := s.global().ListRunners(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type view struct {
		model.Runner
		Online bool `json:"online"`
	}
	out := make([]view, 0, len(rs))
	for _, rn := range rs {
		out = append(out, view{Runner: rn, Online: s.runners.Online(rn.ID)})
	}
	writeJSON(w, http.StatusOK, out)
}

// mintEnrollToken issues a one-time enrollment token. The token is returned once (never stored — only its
// hash is), for the operator to hand to a runner via `osb-runner --enroll <token>`.
func (s *Server) mintEnrollToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label      string `json:"label"`
		TTLMinutes int    `json:"ttl_minutes"`
	}
	if err := decodeJSONOptional(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	ttl := 60
	if req.TTLMinutes > 0 {
		ttl = req.TTLMinutes
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(time.Duration(ttl) * time.Minute)
	if err := s.global().MintEnrollToken(r.Context(), runnerhub.TokenHash(token), req.Label, expires); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "runner.enroll_token", req.Label, nil)
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires_at": expires.UTC()})
}

// deleteRunner revokes an enrolled runner.
func (s *Server) deleteRunner(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.global().DeleteRunner(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "runner not found")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "runner.delete", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- runner protocol (network-exposed listener) ---

// RunnerHandler is the HTTP handler for the outbound-connect runner protocol. It is served on a separate
// listener (`--runner-addr`) so the main API stays loopback-only; every request except enrollment is
// authenticated by the runner's ed25519 signature.
func (s *Server) RunnerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runners/enroll", s.enrollRunner)
	mux.HandleFunc("GET /v1/runners/stream", s.runnerAuth(s.runnerStream))
	mux.HandleFunc("POST /v1/runners/result", s.runnerAuth(s.runnerResult))
	mux.HandleFunc("POST /v1/runners/http-result", s.runnerAuth(s.runnerHTTPResult))
	mux.HandleFunc("GET /v1/runners/tunnel", s.runnerAuth(s.runnerTunnel))
	return mux
}

// enrollRunner consumes a one-time token and registers the runner's public key, returning its id.
func (s *Server) enrollRunner(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token  string `json:"token"`
		Name   string `json:"name"`
		PubKey string `json:"pubkey"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Token == "" || req.Name == "" || req.PubKey == "" {
		writeErr(w, http.StatusBadRequest, "token, name and pubkey are required")
		return
	}
	ok, err := s.global().ConsumeEnrollToken(r.Context(), runnerhub.TokenHash(req.Token))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "invalid or expired enrollment token")
		return
	}
	rn, err := s.global().CreateRunner(r.Context(), req.Name, req.PubKey)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), "runner:"+rn.Name, "runner.enroll", rn.ID, nil)
	writeJSON(w, http.StatusCreated, map[string]any{"runner_id": rn.ID, "name": rn.Name})
}

type runnerCtxKey struct{}

func runnerIDFrom(r *http.Request) string {
	id, _ := r.Context().Value(runnerCtxKey{}).(string)
	return id
}

// runnerAuth verifies the ed25519 request signature against the enrolled runner's public key, stamps
// last_seen, and passes the runner id to the handler.
func (s *Server) runnerAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(runnerhub.HeaderRunnerID)
		ts := r.Header.Get(runnerhub.HeaderTime)
		sig := r.Header.Get(runnerhub.HeaderSig)
		if id == "" || ts == "" || sig == "" {
			writeErr(w, http.StatusUnauthorized, "missing runner auth headers")
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))

		rn, err := s.global().GetRunner(r.Context(), id)
		if err != nil || rn.Status != model.RunnerActive {
			writeErr(w, http.StatusUnauthorized, "unknown or revoked runner")
			return
		}
		if err := runnerhub.Verify(rn.PubKey, r.Method, r.URL.Path, ts, sig, body, time.Now()); err != nil {
			writeErr(w, http.StatusUnauthorized, "signature verification failed")
			return
		}
		_ = s.global().TouchRunner(r.Context(), id)
		next(w, r.WithContext(context.WithValue(r.Context(), runnerCtxKey{}, id)))
	}
}

// runnerStream is the runner's downstream channel: an SSE stream of dispatch/cancel messages. It marks
// the runner online for the connection's lifetime.
func (s *Server) runnerStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sub := s.runners.Register(runnerIDFrom(r))
	defer sub.Close()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.Done: // evicted by a newer connection from the same runner
			return
		case <-ping.C:
			_ = s.global().TouchRunner(r.Context(), runnerIDFrom(r))
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case d := <-sub.Ch:
			payload, err := json.Marshal(d)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", d.Kind, payload)
			flusher.Flush()
		}
	}
}

// runnerResult receives a completed task's output from the runner and hands it to the waiting dispatcher.
func (s *Server) runnerResult(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID     string `json:"task_id"`
		ExitCode   int    `json:"exit_code"`
		Stdout     []byte `json:"stdout"` // base64 (encoding/json handles []byte)
		Stderr     []byte `json:"stderr"`
		DurationMs int64  `json:"duration_ms"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// A runner may only return results for tasks assigned to it.
	t, err := s.pdb(r).GetTask(r.Context(), req.TaskID)
	if err != nil || t.RunnerTarget != runnerIDFrom(r) {
		writeErr(w, http.StatusForbidden, "task not assigned to this runner")
		return
	}
	s.runners.Deliver(req.TaskID, runner.Result{
		ExitCode: req.ExitCode,
		Stdout:   req.Stdout,
		Stderr:   req.Stderr,
		Duration: time.Duration(req.DurationMs) * time.Millisecond,
	})
	w.WriteHeader(http.StatusNoContent)
}

// runnerHTTPResult receives a runner's response to a dispatched HTTP request (Replay egress, ADR-0025) and
// hands it to the waiting egressSend caller. The hub verifies the runner owns the request id.
func (s *Server) runnerHTTPResult(w http.ResponseWriter, r *http.Request) {
	var res runnerhub.HTTPResult
	if !decodeJSON(w, r, &res) {
		return
	}
	s.runners.DeliverHTTP(runnerIDFrom(r), res)
	w.WriteHeader(http.StatusNoContent)
}

// runnerUpgrader upgrades the tunnel endpoint to a WebSocket. The connection is already authenticated by
// the ed25519 middleware (not a browser), so any origin is accepted.
var runnerUpgrader = websocket.Upgrader{
	CheckOrigin:     func(*http.Request) bool { return true },
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
}

// runnerTunnel upgrades to the multiplexed streaming tunnel (ADR-0026) over which the control plane opens
// a logical stream per proxy forward. The control plane is the stream initiator; the runner accepts.
func (s *Server) runnerTunnel(w http.ResponseWriter, r *http.Request) {
	id := runnerIDFrom(r)
	conn, err := runnerUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote an error
	}
	sess := runnertunnel.New(conn, true)
	s.runners.RegisterTunnel(id, sess)
	defer s.runners.RemoveTunnel(id, sess)
	_ = s.global().TouchRunner(r.Context(), id)
	<-sess.Done() // hold the connection open until the tunnel dies
}

// tunnelForwarder is an http.RoundTripper that forwards a proxied request through a runner's streaming
// tunnel (ADR-0026): it opens a stream carrying the request line + headers, streams the request body up,
// and returns a response whose Body streams the target's response back from the runner's vantage.
type tunnelForwarder struct {
	hub      *runnerhub.Hub
	runnerID string
}

func (f *tunnelForwarder) RoundTrip(req *http.Request) (*http.Response, error) {
	sess, ok := f.hub.TunnelFor(f.runnerID)
	if !ok {
		return nil, fmt.Errorf("runner %s tunnel offline", f.runnerID)
	}
	meta, _ := json.Marshal(runnerhub.TunnelForward{
		Method: req.Method, URL: req.URL.String(), Header: req.Header,
		ContentLength: req.ContentLength, Insecure: true,
	})
	st, err := sess.Open(meta)
	if err != nil {
		return nil, err
	}
	// Stream the (already-buffered) request body up, then half-close.
	go func() {
		if req.Body != nil {
			_, _ = io.Copy(st, req.Body)
			_ = req.Body.Close()
		}
		_ = st.CloseWrite()
	}()
	// Read the response off the stream — the body streams as it arrives.
	resp, err := http.ReadResponse(bufio.NewReader(st), req)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	resp.Body = &tunnelBody{ReadCloser: resp.Body, st: st}
	return resp, nil
}

// tunnelBody closes the underlying tunnel stream when the response body is closed.
type tunnelBody struct {
	io.ReadCloser
	st *runnertunnel.Stream
}

func (b *tunnelBody) Close() error {
	err := b.ReadCloser.Close()
	_ = b.st.Close()
	return err
}
