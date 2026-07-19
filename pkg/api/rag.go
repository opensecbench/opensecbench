package api

import (
	"net/http"
	"strconv"
)

// reindexCorpus rebuilds the semantic index for a project's corpus + KB (ADR-0039).
func (s *Server) reindexCorpus(w http.ResponseWriter, r *http.Request) {
	ix := s.analystService().Indexer()
	if ix == nil || !ix.Available() {
		writeErr(w, http.StatusServiceUnavailable, "no embedder configured — run an embedding server (ollama) or set OSB_EMBED_*")
		return
	}
	n, err := ix.Reindex(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"chunks": n})
}

// searchCorpus does semantic retrieval over a project's corpus + KB (ADR-0039). `q` is the query, `k` the
// number of passages.
func (s *Server) searchCorpus(w http.ResponseWriter, r *http.Request) {
	ix := s.analystService().Indexer()
	if ix == nil || !ix.Available() {
		writeErr(w, http.StatusServiceUnavailable, "no embedder configured — run an embedding server (ollama) or set OSB_EMBED_*")
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, "query 'q' is required")
		return
	}
	k, _ := strconv.Atoi(r.URL.Query().Get("k"))
	hits, err := ix.Search(r.Context(), r.PathValue("id"), q, k)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, hits)
}
