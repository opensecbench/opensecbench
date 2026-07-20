package api

import (
	"crypto/rand"
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

func TestSecretsNeverLeakPlaintext(t *testing.T) {
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

	const value = "SUPERSECRETVALUE12345"

	// Store a secret.
	var meta model.Secret
	if code := postJSON(t, srv.URL+"/v1/secrets", `{"name":"jira_token","value":"`+value+`"}`, &meta); code != http.StatusCreated {
		t.Fatalf("set secret = %d", code)
	}
	if meta.Name != "jira_token" {
		t.Fatalf("meta wrong: %+v", meta)
	}

	// The list endpoint returns names but never the value.
	body := getBody(t, srv.URL+"/v1/secrets")
	if !strings.Contains(body, "jira_token") {
		t.Fatal("secret name should be listed")
	}
	if strings.Contains(body, value) {
		t.Fatal("SECRET VALUE LEAKED in list response")
	}

	// Neither does the audit trail (only metadata was recorded).
	audit := getBody(t, srv.URL+"/v1/audit")
	if strings.Contains(audit, value) {
		t.Fatal("SECRET VALUE LEAKED in audit trail")
	}

	// The stored value is actually sealed (round-trips only via the vault).
	sealed, err := db.GetSealed(t.Context(), "jira_token")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, value) {
		t.Fatal("stored value is not encrypted")
	}
	opened, err := vault.Open(sealed)
	if err != nil || string(opened) != value {
		t.Fatalf("vault cannot reopen sealed value: %v", err)
	}

	// Delete works.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/secrets/jira_token", nil)
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
}

func TestSetSecretUnavailableWithoutVault(t *testing.T) {
	srv := newTestServer(t) // no vault
	if code := postJSON(t, srv.URL+"/v1/secrets", `{"name":"x","value":"y"}`, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("set without vault = %d, want 503", code)
	}
}
