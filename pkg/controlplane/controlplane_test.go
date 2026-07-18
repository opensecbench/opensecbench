package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestExtensionLoadedAtStartup boots the control plane with an unsigned extension package in the
// data dir and confirms its capability is registered and served.
func TestExtensionLoadedAtStartup(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "osb.db")

	// Drop an unsigned extension package under <data>/extensions/mytool.
	pkgDir := filepath.Join(dataDir, "extensions", "mytool")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "id":"acme.mytool","name":"My Tool","version":"1.0.0","publisher":"acme",
	  "capabilities":[{"id":"mytool","version":"1.0.0","title":"My Tool","image":"alpine:3",
	    "cmd":["echo","hi"],"output_name":"out.txt","output_media_type":"text/plain"}]
	}`
	if err := os.WriteFile(filepath.Join(pkgDir, "extension.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unsigned packages load only with the override.
	t.Setenv("OSB_ALLOW_UNSIGNED_EXTENSIONS", "1")

	cp, err := Start(Options{Addr: "127.0.0.1:0", DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cp.Shutdown(context.Background()) }()

	// The extension's capability is registered (served by /v1/capabilities).
	caps := getJSON(t, cp.BaseURL+"/v1/capabilities")
	if !containsID(caps, "mytool") {
		t.Fatalf("extension capability 'mytool' not registered: %v", caps)
	}

	// And it is listed as a loaded (untrusted, since unsigned) extension.
	exts := getJSON(t, cp.BaseURL+"/v1/extensions")
	if len(exts) != 1 || exts[0]["id"] != "acme.mytool" || exts[0]["trusted"] != false {
		t.Fatalf("extension not listed as loaded/untrusted: %v", exts)
	}
}

func getJSON(t *testing.T, url string) []map[string]any {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func containsID(items []map[string]any, id string) bool {
	for _, m := range items {
		if m["id"] == id {
			return true
		}
	}
	return false
}
