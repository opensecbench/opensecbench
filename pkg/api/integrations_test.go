package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
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

	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	key := make([]byte, secret.KeySize)
	_, _ = rand.Read(key)
	vault, _ := secret.NewVault(key)
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs, Vault: vault}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	// A finding to push, a vault credential for Jira basic auth, a global Jira connector, and a per-project
	// binding that supplies the Jira project key.
	proj := mustProject(t, db)
	app, _ := db.CreateApplication(ctx, proj.ID, "Storefront")
	finding, _ := db.CreateFinding(ctx, store.NewFinding{ApplicationID: &app.ID, Title: "Auth bypass", Severity: "high"})
	sealed, _ := vault.Seal([]byte("bot@acme.com:token123"))
	if _, err := db.SetSecret(ctx, "jira_cred", sealed); err != nil {
		t.Fatal(err)
	}
	conn, err := db.CreateConnector(ctx, model.Connector{Name: "Jira", Type: "jira", BaseURL: jira.URL, Credential: "jira_cred"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetBinding(ctx, model.IntegrationBinding{ProjectID: proj.ID, ConnectorID: conn.ID, ProjectKey: "SEC"}); err != nil {
		t.Fatal(err)
	}

	// The push resolves the binding's project key from the active project (X-Project-Id, as the UI sends).
	push := func() (int, model.ExternalLink) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/findings/"+finding.ID+"/push", strings.NewReader(`{"connector_id":"`+conn.ID+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Project-Id", proj.ID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var l model.ExternalLink
		if resp.StatusCode < http.StatusBadRequest {
			_ = json.NewDecoder(resp.Body).Decode(&l)
		}
		return resp.StatusCode, l
	}

	code, link := push()
	if code != http.StatusCreated {
		t.Fatalf("push = %d", code)
	}
	if link.ExternalID != "SEC-42" || !strings.Contains(link.ExternalURL, "/browse/SEC-42") {
		t.Fatalf("unexpected link: %+v", link)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("expected basic auth from vault credential, got %q", gotAuth)
	}

	// Re-push is idempotent: returns the existing link, does not create a second issue.
	if code, _ := push(); code != http.StatusOK {
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
