package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/extension"
	"github.com/opensecbench/opensecbench/pkg/hub"
)

// trustPublisher adds a publisher's public key to the trust store (explicit, audited consent).
func (s *Server) trustPublisher(w http.ResponseWriter, r *http.Request) {
	if s.trust == nil {
		writeErr(w, http.StatusServiceUnavailable, "trust store unavailable")
		return
	}
	var req struct {
		Publisher string `json:"publisher"`
		PublicKey string `json:"public_key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Publisher == "" || req.PublicKey == "" {
		writeErr(w, http.StatusBadRequest, "publisher and public_key are required")
		return
	}
	if err := s.trust.Trust(req.Publisher, req.PublicKey); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r.Context(), actorOf(r), "extension.trust", req.Publisher, nil)
	w.WriteHeader(http.StatusNoContent)
}

// hubIndex fetches a hub's package index (a browse proxy so the UI needn't reach the hub directly).
func (s *Server) hubIndex(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" {
		writeErr(w, http.StatusBadRequest, "url query parameter is required")
		return
	}
	idx, err := s.hubCli.FetchIndex(r.Context(), url)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, idx)
}

// hubInstall downloads, verifies, extracts, and hot-registers a package from a hub (ADR-0014). Trust
// is explicit: with trust=true the entry's publisher key is trusted first; otherwise an untrusted
// package is refused unless allow_unsigned.
func (s *Server) hubInstall(w http.ResponseWriter, r *http.Request) {
	if s.trust == nil || s.extDir == "" || s.engine == nil {
		writeErr(w, http.StatusServiceUnavailable, "extension install unavailable")
		return
	}
	var req struct {
		URL           string `json:"url"`
		ID            string `json:"id"`
		Trust         bool   `json:"trust"`
		AllowUnsigned bool   `json:"allow_unsigned"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.URL == "" || req.ID == "" {
		writeErr(w, http.StatusBadRequest, "url and id are required")
		return
	}

	idx, err := s.hubCli.FetchIndex(r.Context(), req.URL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "fetch index: "+err.Error())
		return
	}
	entry, ok := idx.Find(req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "package not in hub index: "+req.ID)
		return
	}

	// Explicit trust-on-install (never trust-on-first-use).
	if req.Trust && entry.PublisherKey != "" {
		if err := s.trust.Trust(entry.Publisher, entry.PublisherKey); err != nil {
			writeErr(w, http.StatusBadRequest, "trust key: "+err.Error())
			return
		}
		s.record(r.Context(), actorOf(r), "extension.trust", entry.Publisher, nil)
	}

	archive, err := s.hubCli.DownloadArchive(r.Context(), req.URL, entry)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	dstDir := filepath.Join(s.extDir, safeDirName(entry.ID))
	_ = os.RemoveAll(dstDir)
	if err := hub.Extract(archive, dstDir); err != nil {
		writeErr(w, http.StatusInternalServerError, "extract: "+err.Error())
		return
	}

	// The loader re-verifies the manifest digest + signature against the trust store.
	loaded, err := extension.Load(dstDir, s.trust, req.AllowUnsigned)
	if err != nil {
		_ = os.RemoveAll(dstDir)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Hot-register into the live registries (no restart).
	for _, c := range loaded.CapabilityList() {
		s.engine.Registry().Register(c)
	}
	for _, m := range loaded.Manifest.Methodologies {
		s.methods.Register(m)
	}
	s.extMu.Lock()
	s.exts = append(s.exts, loaded)
	s.extMu.Unlock()

	s.record(r.Context(), actorOf(r), "extension.install", loaded.Manifest.ID, map[string]any{
		"version": loaded.Manifest.Version, "publisher": loaded.Manifest.Publisher, "trusted": loaded.Trusted,
	})
	writeJSON(w, http.StatusCreated, extensionInfo{
		ID: loaded.Manifest.ID, Name: loaded.Manifest.Name, Version: loaded.Manifest.Version,
		Publisher: loaded.Manifest.Publisher, Trusted: loaded.Trusted, Digest: loaded.Digest,
	})
}

func safeDirName(id string) string {
	return strings.NewReplacer("/", "_", "..", "_", string(filepath.Separator), "_").Replace(id)
}
