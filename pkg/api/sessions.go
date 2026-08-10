package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/session"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// liveSession is an open terminal held in memory while its container runs.
type liveSession struct {
	h         *session.Handle
	projectID string
	attached  atomic.Bool
	once      sync.Once
}

// upgrader allows any loopback origin (the API binds to loopback only; single-user workbench). It
// advertises the wsBearerProto subprotocol so the browser's token-carrying handshake negotiates
// cleanly (ADR-0061): the client offers `[osb.bearer, <token>]`, the security middleware reads the
// token from the header, and gorilla echoes only the marker back.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
	Subprotocols:    []string{wsBearerProto},
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	items, err := s.pdb(r).ListSessionsByProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.pdb(r).GetSession(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// createSession opens a sandboxed container terminal and records the session.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	if s.sessMgr == nil {
		writeErr(w, http.StatusServiceUnavailable, "session manager unavailable (docker required)")
		return
	}
	projectID := r.PathValue("id")
	if _, err := s.mgr.GetProject(r.Context(), projectID); err != nil {
		writeErr(w, http.StatusBadRequest, "unknown project")
		return
	}
	var req struct {
		Actor string `json:"actor"`
	}
	_ = decodeJSONOptional(r, &req)

	var opts session.OpenOpts
	if eng, err := s.pdb(r).GetEngagement(r.Context(), projectID); err == nil {
		if eng.RuntimeImage != "" {
			opts.Image = eng.RuntimeImage
		}
		if eng.RuntimeNetwork != "" {
			opts.Network = eng.RuntimeNetwork
		}
	}

	id := uuid.NewString()
	container := "osb-sess-" + id
	h, err := s.sessMgr.OpenWith(r.Context(), container, opts)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "open session: "+err.Error())
		return
	}
	sess, err := s.pdb(r).CreateSession(r.Context(), model.Session{
		ID:        id,
		ProjectID: projectID,
		Container: container,
		Image:     s.sessMgr.EffectiveImage(opts),
		Runner:    "local-docker",
		Actor:     req.Actor,
	})
	if err != nil {
		_ = h.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sessMu.Lock()
	s.sessions[id] = &liveSession{h: h, projectID: projectID}
	s.sessMu.Unlock()
	s.record(r.Context(), sessionActor(req.Actor), "session.open", id, map[string]string{
		"project": projectID, "container": container,
	})
	writeJSON(w, http.StatusCreated, sess)
}

func sessionActor(actor string) string {
	if actor == "" {
		return "human"
	}
	return actor
}

// sessionWS bridges a WebSocket to the session's PTY: binary frames are terminal input, text frames
// are JSON control messages (resize). When the socket closes, the session is finalized.
func (s *Server) sessionWS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.sessMu.Lock()
	ls := s.sessions[id]
	s.sessMu.Unlock()
	if ls == nil {
		writeErr(w, http.StatusNotFound, "session not open")
		return
	}
	if !ls.attached.CompareAndSwap(false, true) {
		writeErr(w, http.StatusConflict, "session already attached")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		ls.attached.Store(false)
		return
	}

	// PTY → WebSocket: stream terminal output to the client.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ls.h.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"),
					time.Now().Add(time.Second))
				_ = conn.Close()
				return
			}
		}
	}()

	// WebSocket → PTY: forward client input and control messages until the socket closes.
bridge:
	for {
		mt, data, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		switch mt {
		case websocket.TextMessage:
			var ctrl struct {
				Type string `json:"type"`
				Rows uint16 `json:"rows"`
				Cols uint16 `json:"cols"`
			}
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" {
				_ = ls.h.Resize(ctrl.Rows, ctrl.Cols)
			}
		case websocket.BinaryMessage:
			if _, werr := ls.h.Write(data); werr != nil {
				break bridge
			}
		}
	}
	s.finalizeSession(id)
}

// closeSession finalizes a session on demand (capturing its transcript).
func (s *Server) closeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.finalizeSession(id)
	sess, err := s.pdb(r).GetSession(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// finalizeSession tears down a live session and persists its transcript to the CAS exactly once.
// Safe to call multiple times and from either the WebSocket path or the close endpoint.
func (s *Server) finalizeSession(id string) {
	s.sessMu.Lock()
	ls := s.sessions[id]
	delete(s.sessions, id)
	s.sessMu.Unlock()
	if ls == nil {
		return
	}
	ls.once.Do(func() {
		_ = ls.h.Close()
		transcript := ls.h.Transcript()
		ctx := context.Background()

		var artifactID *string
		if digest, err := s.casFor(ls.projectID).Put(bytes.NewReader(transcript)); err == nil {
			if art, aerr := s.pdbID(ls.projectID).CreateArtifact(ctx, model.Artifact{
				SHA256:    digest,
				Size:      int64(len(transcript)),
				Kind:      model.ArtifactInput,
				Name:      "session-transcript",
				MediaType: "text/plain",
			}); aerr == nil {
				artifactID = &art.ID
			}
		}
		_ = s.pdbID(ls.projectID).CloseSession(ctx, id, model.SessionClosed, artifactID, "")
		s.record(ctx, "human", "session.close", id, map[string]any{
			"transcript_artifact": artifactID, "transcript_bytes": len(transcript),
		})
	})
}

// sessionEvidence promotes a closed session's transcript into a human-origin observation.
func (s *Server) sessionEvidence(w http.ResponseWriter, r *http.Request) {
	sess, err := s.pdb(r).GetSession(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sess.TranscriptArtifactID == nil {
		writeErr(w, http.StatusBadRequest, "session has no captured transcript yet (still open?)")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = decodeJSONOptional(r, &req)

	obs, err := s.pdb(r).CreateObservation(r.Context(), model.Observation{
		ArtifactID:  sess.TranscriptArtifactID,
		Origin:      model.OriginHuman,
		ReviewState: model.ReviewUnreviewed,
		Title:       "Terminal session " + sess.Container,
		Detail:      req.Note,
		Severity:    "info",
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "evidence.session", obs.ID, map[string]string{"session": sess.ID})
	writeJSON(w, http.StatusCreated, obs)
}
