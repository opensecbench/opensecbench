package rag

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// maxIndexBytes caps how much of a single source we index, so one huge document can't dominate the index.
const maxIndexBytes = 1 << 20 // 1 MiB

// Indexer builds and queries the semantic retrieval index (ADR-0039). It embeds via a (local, by default)
// Embedder and persists vectors in the store. A nil Embedder disables indexing/search (best-effort).
type Indexer struct {
	Mgr   *store.Manager
	Blobs *cas.Store
	Embed llm.Embedder
}

// Available reports whether an embedder is configured (indexing/search are no-ops otherwise).
func (ix *Indexer) Available() bool { return ix != nil && ix.Embed != nil }

// p resolves the project's database, falling back to global so a nil handle never panics (ADR-0049).
func (ix *Indexer) p(projectID string) *store.DB {
	if ix.Mgr == nil {
		return nil
	}
	db, err := ix.Mgr.Project(projectID)
	if err != nil || db == nil {
		return ix.Mgr.Global()
	}
	return db
}

// IndexContextItem re-indexes one corpus document: it loads the item's text from the CAS, chunks, embeds,
// and stores the vectors (replacing any prior chunks for that item). Non-text or empty content clears the
// item's chunks. Best-effort — returns an error the caller may log but need not surface.
func (ix *Indexer) IndexContextItem(ctx context.Context, projectID, itemID string) error {
	if !ix.Available() {
		return nil
	}
	ci, err := ix.p(projectID).GetContextItem(ctx, itemID)
	if err != nil {
		return err
	}
	art, err := ix.p(projectID).GetArtifact(ctx, ci.ArtifactID)
	if err != nil {
		return err
	}
	rc, err := ix.Blobs.Open(art.SHA256)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(rc, maxIndexBytes))
	text := string(raw)
	if !isProbablyText(text) {
		return ix.p(projectID).DeleteChunksForSource(ctx, projectID, "context", itemID)
	}
	return ix.indexSource(ctx, projectID, "context", itemID, ci.Name, text)
}

// IndexKBEntry re-indexes one knowledge-base entry (its title + body are inline). Best-effort.
func (ix *Indexer) IndexKBEntry(ctx context.Context, projectID string, e model.KBEntry) error {
	if !ix.Available() {
		return nil
	}
	text := strings.TrimSpace(e.Title + "\n\n" + e.Body)
	return ix.indexSource(ctx, projectID, "kb", e.ID, e.Title, text)
}

func (ix *Indexer) indexSource(ctx context.Context, projectID, kind, sourceID, name, text string) error {
	chunks := Chunk(text)
	if len(chunks) == 0 {
		return ix.p(projectID).DeleteChunksForSource(ctx, projectID, kind, sourceID)
	}
	vecs, err := ix.Embed.Embed(ctx, chunks)
	if err != nil {
		return fmt.Errorf("rag: embed %s/%s: %w", kind, sourceID, err)
	}
	cvs := make([]store.ChunkVec, len(chunks))
	for i := range chunks {
		cvs[i] = store.ChunkVec{Text: chunks[i], Embedding: vecs[i]}
	}
	return ix.p(projectID).ReplaceChunks(ctx, projectID, kind, sourceID, name, ix.Embed.EmbedName(), cvs)
}

// Reindex rebuilds the whole index for a project — every corpus item and KB entry — and returns the chunk
// count. Individual failures (e.g. an unindexable item) are skipped so one bad source can't abort the run.
func (ix *Indexer) Reindex(ctx context.Context, projectID string) (int, error) {
	if !ix.Available() {
		return 0, fmt.Errorf("rag: no embedder configured (run ollama or set OSB_EMBED_*)")
	}
	items, err := ix.p(projectID).ListContextItemsByProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	for _, ci := range items {
		_ = ix.IndexContextItem(ctx, projectID, ci.ID)
	}
	entries, err := ix.Mgr.ListKBForProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		_ = ix.IndexKBEntry(ctx, projectID, e)
	}
	return ix.p(projectID).CountChunks(ctx, projectID)
}

// Search embeds the query and returns the top-k most similar chunks (ADR-0039).
func (ix *Indexer) Search(ctx context.Context, projectID, query string, k int) ([]store.ScoredChunk, error) {
	if !ix.Available() {
		return nil, fmt.Errorf("rag: semantic search unavailable — no embedder configured (run ollama or set OSB_EMBED_*)")
	}
	vecs, err := ix.Embed.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("rag: embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	return ix.p(projectID).SearchChunks(ctx, projectID, vecs[0], k)
}

// isProbablyText rejects binary blobs (NUL bytes) so we don't embed garbage.
func isProbablyText(s string) bool {
	if s == "" {
		return false
	}
	n := len(s)
	if n > 512 {
		n = 512
	}
	return !strings.ContainsRune(s[:n], 0)
}
