package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type settingsResp struct {
	Sections []struct {
		ID     string `json:"id"`
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
