package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/extension"
	"github.com/opensecbench/opensecbench/pkg/settings"
	"github.com/opensecbench/opensecbench/pkg/store"
)

type settingsResp struct {
	Sections []struct {
		ID     string `json:"id"`
		Source string `json:"source"`
		Fields []struct {
			Key string `json:"key"`
		} `json:"fields"`
	} `json:"sections"`
	Values map[string]string `json:"values"`
}

func getSettingsResp(t *testing.T, url string) settingsResp {
	t.Helper()
	resp, err := http.Get(url + "/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out settingsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSettingsGetPut(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	// GET exposes the appearance section with its defaults applied.
	got := getSettingsResp(t, srv.URL)
	hasAppearance := false
	for _, s := range got.Sections {
		if s.ID == "appearance" {
			hasAppearance = true
		}
	}
	if !hasAppearance {
		t.Fatal("appearance section missing")
	}
	if got.Values["appearance.theme"] != "dark" {
		t.Fatalf("default theme = %q, want dark", got.Values["appearance.theme"])
	}

	// PUT a valid value; GET reflects it.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/settings", strings.NewReader(`{"values":{"appearance.theme":"light"}}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %d", resp.StatusCode)
	}
	if getSettingsResp(t, srv.URL).Values["appearance.theme"] != "light" {
		t.Fatal("theme not persisted")
	}

	// An unknown key is rejected.
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/settings", strings.NewReader(`{"values":{"bogus.key":"x"}}`))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown key should be rejected, status = %d", resp2.StatusCode)
	}
}

// A server carrying one extension that declares a settings section (ADR-0021 §5).
func newSettingsExtServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	ext := extension.Loaded{Manifest: extension.Manifest{
		ID: "acme.scanner",
		Settings: []settings.Section{{
			ID:    "scanner",
			Title: "Scanner",
			Order: 60,
			Fields: []settings.Field{
				{Key: "scanner.depth", Label: "Depth", Type: settings.TypeNumber, Default: "3"},
			},
		}},
	}}
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), Extensions: []extension.Loaded{ext}}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	return srv
}

func TestSettingsSurfacesExtensionSection(t *testing.T) {
	srv := newSettingsExtServer(t)
	const key = "ext.acme.scanner.scanner.depth"

	got := getSettingsResp(t, srv.URL)
	var found bool
	for _, s := range got.Sections {
		if s.ID == "ext.acme.scanner.scanner" {
			found = true
			if s.Source != "ext:acme.scanner" {
				t.Fatalf("section source = %q, want ext:acme.scanner", s.Source)
			}
		}
	}
	if !found {
		t.Fatalf("extension section not surfaced; sections = %+v", got.Sections)
	}
	if got.Values[key] != "3" {
		t.Fatalf("default for %s = %q, want 3", key, got.Values[key])
	}
}

func TestSettingsExtensionWriteNamespacing(t *testing.T) {
	srv := newSettingsExtServer(t)
	const key = "ext.acme.scanner.scanner.depth"

	put := func(body string) int {
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/settings", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// The namespaced key is a valid write target and persists.
	if code := put(`{"values":{"` + key + `":"7"}}`); code != http.StatusNoContent {
		t.Fatalf("PUT namespaced key = %d, want 204", code)
	}
	if getSettingsResp(t, srv.URL).Values[key] != "7" {
		t.Fatal("namespaced value not persisted")
	}

	// The bare key an extension author wrote must NOT resolve — only the namespaced form does.
	if code := put(`{"values":{"scanner.depth":"9"}}`); code != http.StatusBadRequest {
		t.Fatalf("PUT bare (un-namespaced) key = %d, want 400", code)
	}
}
