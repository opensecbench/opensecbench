package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/opensecbench/opensecbench/pkg/bundle"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// passphraseHeader carries the bundle passphrase out-of-band so it is never in a URL or audited.
const passphraseHeader = "X-OSB-Passphrase"

// exportProject returns an encrypted bundle of the project (ADR-0012). The passphrase comes from the
// X-OSB-Passphrase header and is never logged.
func (s *Server) exportProject(w http.ResponseWriter, r *http.Request) {
	pass := r.Header.Get(passphraseHeader)
	if pass == "" {
		writeErr(w, http.StatusBadRequest, "passphrase required (send the "+passphraseHeader+" header)")
		return
	}
	id := r.PathValue("id")
	data, err := bundle.Export(r.Context(), s.store, s.cas, id, pass)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "project.export", id, map[string]int{"bytes": len(data)})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="project-`+id+`.osb"`)
	_, _ = w.Write(data)
}

// importBundle imports an encrypted bundle (body = bundle bytes, X-OSB-Passphrase header) and returns
// the new project id.
func (s *Server) importBundle(w http.ResponseWriter, r *http.Request) {
	pass := r.Header.Get(passphraseHeader)
	if pass == "" {
		writeErr(w, http.StatusBadRequest, "passphrase required (send the "+passphraseHeader+" header)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20) // 256 MiB cap
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	newID, err := bundle.Import(r.Context(), s.store, s.cas, data, pass)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "project.import", newID, nil)
	writeJSON(w, http.StatusCreated, map[string]string{"project_id": newID})
}
