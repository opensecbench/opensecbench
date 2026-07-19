package store

import (
	"context"
	"encoding/binary"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// ChunkVec is one text chunk and its embedding, for indexing (ADR-0039).
type ChunkVec struct {
	Text      string
	Embedding []float32
}

// ScoredChunk is a retrieved chunk with its cosine similarity to the query.
type ScoredChunk struct {
	SourceKind string  `json:"source_kind"`
	SourceID   string  `json:"source_id"`
	SourceName string  `json:"source_name"`
	ChunkIndex int     `json:"chunk_index"`
	Text       string  `json:"text"`
	Score      float32 `json:"score"`
}

// ReplaceChunks re-indexes a single source (a corpus item or KB entry): it deletes that source's existing
// chunks and inserts the new ones, so re-indexing is idempotent (ADR-0039). Empty `chunks` just clears it.
func (db *DB) ReplaceChunks(ctx context.Context, projectID, kind, sourceID, sourceName, model string, chunks []ChunkVec) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM corpus_chunks WHERE project_id = ? AND source_kind = ? AND source_id = ?`,
		projectID, kind, sourceID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(timeLayout)
	for i, c := range chunks {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO corpus_chunks (id, project_id, source_kind, source_id, source_name, chunk_index, text, embedding, dim, model, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), projectID, kind, sourceID, sourceName, i, c.Text,
			float32ToBytes(c.Embedding), len(c.Embedding), model, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteChunksForSource removes a source's chunks (e.g. when its corpus item is deleted).
func (db *DB) DeleteChunksForSource(ctx context.Context, projectID, kind, sourceID string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM corpus_chunks WHERE project_id = ? AND source_kind = ? AND source_id = ?`,
		projectID, kind, sourceID)
	return err
}

// CountChunks returns how many chunks are indexed for a project.
func (db *DB) CountChunks(ctx context.Context, projectID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM corpus_chunks WHERE project_id = ?`, projectID).Scan(&n)
	return n, err
}

// SearchChunks ranks a project's chunks by cosine similarity to the query vector and returns the top k
// (ADR-0039). Brute-force in Go — modernc SQLite has no vector extension. Query dimensions that don't match
// a stored chunk's are skipped (a stale index from a different embedding model won't match spuriously).
func (db *DB) SearchChunks(ctx context.Context, projectID string, query []float32, k int) ([]ScoredChunk, error) {
	if k <= 0 {
		k = 5
	}
	rows, err := db.QueryContext(ctx,
		`SELECT source_kind, source_id, source_name, chunk_index, text, embedding, dim
		 FROM corpus_chunks WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var scored []ScoredChunk
	for rows.Next() {
		var sc ScoredChunk
		var emb []byte
		var dim int
		if err := rows.Scan(&sc.SourceKind, &sc.SourceID, &sc.SourceName, &sc.ChunkIndex, &sc.Text, &emb, &dim); err != nil {
			return nil, err
		}
		if dim != len(query) {
			continue
		}
		sc.Score = cosine(query, bytesToFloat32(emb))
		scored = append(scored, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

func float32ToBytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func bytesToFloat32(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// cosine similarity of two equal-length vectors; 0 when either has no magnitude.
func cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
