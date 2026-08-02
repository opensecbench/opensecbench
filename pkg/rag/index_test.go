package rag

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

func newIndexer(t *testing.T) (*Indexer, *store.DB, *cas.Store) {
	t.Helper()
	db := storetest.New(t)
	blobs, err := cas.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Indexer{Mgr: store.NewCombinedManager(db), Casr: cas.Fixed(blobs), Embed: llm.NewMockEmbedder()}, db, blobs
}

// Index a corpus doc + a KB entry, then a semantic query returns the relevant one ranked first (ADR-0039).
func TestIndexAndSearch(t *testing.T) {
	ix, db, blobs := newIndexer(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})

	// A corpus document about Keycloak.
	doc := "Keycloak admin console must not be exposed publicly. Disable the admin console on the external listener and restrict it to an internal network."
	digest, _ := blobs.Put(strings.NewReader(doc))
	art, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: digest, MediaType: "text/plain", Size: int64(len(doc)), Name: "keycloak.md", Kind: "output"})
	ci, _ := db.CreateContextItem(ctx, model.ContextItem{ProjectID: proj.ID, Type: "document", Name: "keycloak.md", ArtifactID: art.ID})
	if err := ix.IndexContextItem(ctx, proj.ID, ci.ID); err != nil {
		t.Fatal(err)
	}

	// A KB entry about a different topic.
	tgt, _ := db.CreateTarget(ctx, "t", "", nil)
	kb, _ := db.CreateKBEntry(ctx, model.KBEntry{TargetID: tgt.ID, Kind: "gotcha", Title: "Gin trusted proxies", Body: "The gin framework trusts all proxies by default; set trusted proxies explicitly.", Origin: model.OriginHuman})
	if err := ix.IndexKBEntry(ctx, proj.ID, kb); err != nil {
		t.Fatal(err)
	}

	// A Keycloak-flavored query surfaces the Keycloak doc first (shared terms → higher cosine).
	hits, err := ix.Search(ctx, proj.ID, "keycloak admin console exposed publicly", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Text, "Keycloak") {
		t.Fatalf("keycloak query should surface the keycloak doc first: %+v", hits)
	}
	// A gin query surfaces the KB entry.
	hits2, _ := ix.Search(ctx, proj.ID, "gin framework trusted proxies", 3)
	if len(hits2) == 0 || hits2[0].SourceKind != "kb" {
		t.Fatalf("gin query should surface the KB entry: %+v", hits2)
	}
}

// With no embedder, search/reindex return a clear unavailable error rather than misbehaving.
func TestIndexerUnavailable(t *testing.T) {
	ix := &Indexer{Mgr: nil, Casr: nil, Embed: nil}
	if ix.Available() {
		t.Fatal("nil embedder should be unavailable")
	}
	if _, err := ix.Search(context.Background(), "p", "q", 3); err == nil {
		t.Fatal("search without an embedder should error")
	}
}
