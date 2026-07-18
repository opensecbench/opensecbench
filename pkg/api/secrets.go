package api

import (
	"errors"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/store"
)

// listSecrets returns secret metadata (names only) — never values (ADR-0011).
func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSecrets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// setSecret seals a value into the vault. The plaintext is never persisted, logged, or echoed back.
func (s *Server) setSecret(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeErr(w, http.StatusServiceUnavailable, "vault unavailable")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.Value == "" {
		writeErr(w, http.StatusBadRequest, "name and value are required")
		return
	}
	sealed, err := s.vault.Seal([]byte(req.Value))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "seal: "+err.Error())
		return
	}
	meta, err := s.store.SetSecret(r.Context(), req.Name, sealed)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Audit the metadata only — never the value.
	s.record(r.Context(), actorOf(r), "secret.set", req.Name, nil)
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.store.DeleteSecret(r.Context(), name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "secret not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "secret.delete", name, nil)
	w.WriteHeader(http.StatusNoContent)
}
