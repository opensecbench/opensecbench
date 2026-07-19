package store

import (
	"context"
	"reflect"
	"testing"
)

func TestVectorEncodingAndCosine(t *testing.T) {
	v := []float32{1.5, -2, 0, 3.25}
	if got := bytesToFloat32(float32ToBytes(v)); !reflect.DeepEqual(got, v) {
		t.Fatalf("round-trip = %v, want %v", got, v)
	}
	if c := cosine([]float32{1, 0}, []float32{1, 0}); c < 0.999 {
		t.Fatalf("identical vectors cosine = %f, want ~1", c)
	}
	if c := cosine([]float32{1, 0}, []float32{0, 1}); c != 0 {
		t.Fatalf("orthogonal cosine = %f, want 0", c)
	}
	if c := cosine([]float32{0, 0}, []float32{1, 1}); c != 0 {
		t.Fatalf("zero-magnitude cosine = %f, want 0", c)
	}
}

func TestReplaceAndSearchChunks(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})

	err := db.ReplaceChunks(ctx, proj.ID, "context", "src1", "doc1", "mock", []ChunkVec{
		{Text: "apple orchard", Embedding: []float32{1, 0, 0}},
		{Text: "banana grove", Embedding: []float32{0, 1, 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A query near the first vector ranks that chunk top.
	hits, err := db.SearchChunks(ctx, proj.ID, []float32{0.9, 0.1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Text != "apple orchard" {
		t.Fatalf("nearest chunk should rank first: %+v", hits)
	}
	if hits[0].Score < hits[1].Score {
		t.Fatal("results must be sorted by descending score")
	}
	// Re-indexing the same source replaces (not appends) — idempotent.
	_ = db.ReplaceChunks(ctx, proj.ID, "context", "src1", "doc1", "mock", []ChunkVec{{Text: "cherry", Embedding: []float32{0, 0, 1}}})
	if n, _ := db.CountChunks(ctx, proj.ID); n != 1 {
		t.Fatalf("replace should be idempotent, count = %d want 1", n)
	}
	// A query of a different dimension is skipped, not spuriously matched.
	if h, _ := db.SearchChunks(ctx, proj.ID, []float32{1, 0}, 5); len(h) != 0 {
		t.Fatalf("mismatched-dimension query should match nothing, got %d", len(h))
	}
}
