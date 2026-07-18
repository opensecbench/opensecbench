package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostWebhook(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := PostWebhook(context.Background(), srv.URL, "Approval needed", "run semgrep"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got["text"], "Approval needed") || !strings.Contains(got["text"], "run semgrep") {
		t.Fatalf("webhook payload wrong: %v", got)
	}
}

func TestPostWebhookErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := PostWebhook(context.Background(), srv.URL, "t", "b"); err == nil {
		t.Fatal("expected error on 500")
	}
}
