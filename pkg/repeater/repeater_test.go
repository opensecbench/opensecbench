package repeater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendCapturesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("X-Test"); got != "yes" {
			t.Errorf("X-Test header = %q, want yes", got)
		}
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		w.Header().Set("X-Reply", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello " + string(body)))
	}))
	defer srv.Close()

	resp, err := New(0).Send(context.Background(), Request{
		Method:  "post",
		URL:     srv.URL,
		Headers: "X-Test: yes\n",
		Body:    "world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.Status)
	}
	if !strings.Contains(resp.Headers, "X-Reply: ok") {
		t.Fatalf("response headers missing X-Reply: %q", resp.Headers)
	}
	if !strings.Contains(resp.Body, "hello world") {
		t.Fatalf("body = %q, want to contain 'hello world'", resp.Body)
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()

	resp, err := New(0).Send(context.Background(), Request{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect not followed)", resp.Status)
	}
}

func TestSendRejectsBadHeaderLine(t *testing.T) {
	_, err := New(0).Send(context.Background(), Request{URL: "http://example.invalid", Headers: "no-colon-here"})
	if err == nil {
		t.Fatal("expected error for malformed header line")
	}
}
