package api

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestPushFindingToJiraIsIdempotent(t *testing.T) {
	// A minimal Jira stub that records requests and returns an issue key.
	var created int
	var gotAuth string
	jira := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/rest/api/2/issue") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Auth bypass") {
			t.Errorf("issue body missing finding title: %s", body)
		}
		created++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"key":"SEC-42"}`))
	}))
	defer jira.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	key := make([]byte, secret.KeySize)
	_, _ = rand.Read(key)
	vault, _ := secret.NewVault(key)
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs, Vault: vault}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	// A finding to push, and a vault credential for Jira basic auth.
	app, _ := db.CreateApplication(ctx, mustProject(t, db).ID, "Storefront")
	finding, _ := db.CreateFinding(ctx, store.NewFinding{ApplicationID: &app.ID, Title: "Auth bypass", Severity: "high"})
	sealed, _ := vault.Seal([]byte("bot@acme.com:token123"))
	if _, err := db.SetSecret(ctx, "jira_cred", sealed); err != nil {
		t.Fatal(err)
	}

	push := `{"integration":"jira","base_url":"` + jira.URL + `","project_key":"SEC","credential":"jira_cred"}`

	var link model.ExternalLink
	if code := postJSON(t, srv.URL+"/v1/findings/"+finding.ID+"/push", push, &link); code != http.StatusCreated {
		t.Fatalf("push = %d", code)
	}
	if link.ExternalID != "SEC-42" || !strings.Contains(link.ExternalURL, "/browse/SEC-42") {
		t.Fatalf("unexpected link: %+v", link)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("expected basic auth from vault credential, got %q", gotAuth)
	}

	// Re-push is idempotent: returns the existing link, does not create a second issue.
	if code := postJSON(t, srv.URL+"/v1/findings/"+finding.ID+"/push", push, &link); code != http.StatusOK {
		t.Fatalf("re-push = %d, want 200", code)
	}
	if created != 1 {
		t.Fatalf("Jira issue created %d times, want 1 (idempotency broken)", created)
	}
}

func mustProject(t *testing.T, db *store.DB) model.Project {
	t.Helper()
	p, err := db.CreateProject(context.Background(), store.NewProject{Name: "engagement"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
