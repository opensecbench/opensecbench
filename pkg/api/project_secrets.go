package api

import (
	"errors"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// projectVault returns the vault whose key lives beside this project's project.db (ADR-0049), used to
// seal/open project-scoped secrets independently of the instance-wide vault. Nil provider or manager
// (tests, or a disabled vault) yields an error so handlers can report the vault unavailable.
func (s *Server) projectVault(projectID string) (*secret.Vault, error) {
	if s.vaultProv == nil || s.mgr == nil {
		return nil, errors.New("vault unavailable")
	}
	return s.vaultProv.For(s.mgr.ProjectDir(projectID))
}

// listProjectSecrets returns metadata for this project's own vault secrets — names/timestamps, never
// values, and never the inherited global ones (the UI fetches those separately, read-only). ADR-0011.
func (s *Server) listProjectSecrets(w http.ResponseWriter, r *http.Request) {
	db, err := s.projectDB(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := db.ListSecrets(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// setProjectSecret seals a value into the project vault. The plaintext is never persisted, logged, or
// echoed back; setting an existing name replaces its value.
func (s *Server) setProjectSecret(w http.ResponseWriter, r *http.Request) {
	pid := projectFromReq(r)
	vault, err := s.projectVault(pid)
	if err != nil {
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
	sealed, err := vault.Seal([]byte(req.Value))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "seal: "+err.Error())
		return
	}
	meta, err := s.pdb(r).SetSecret(r.Context(), req.Name, sealed)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Audit the metadata only — never the value. Target is scoped by project so the trail is unambiguous.
	s.record(r.Context(), actorOf(r), "secret.set", pid+"/"+req.Name, nil)
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) deleteProjectSecret(w http.ResponseWriter, r *http.Request) {
	pid := projectFromReq(r)
	name := r.PathValue("name")
	if err := s.pdb(r).DeleteSecret(r.Context(), name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "secret not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "secret.delete", pid+"/"+name, nil)
	w.WriteHeader(http.StatusNoContent)
}
