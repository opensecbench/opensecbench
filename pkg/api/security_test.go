package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler is a trivial next-handler that records it was reached.
func okHandler(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

func TestLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:7373": true,
		"localhost:7373": true,
		"[::1]:7373":     true,
		"127.0.0.1":      true,
		"[::1]":          true,
		"evil.com:7373":  false,
		"192.168.1.5:80": false,
		"10.0.0.1":       false,
		"":               false,
	}
	for host, want := range cases {
		if got := loopbackHost(host); got != want {
			t.Errorf("loopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestAllowedOrigin(t *testing.T) {
	cases := map[string]bool{
		"":                       true, // absent (non-browser / same-origin)
		"null":                   true, // opaque origin
		"http://127.0.0.1:5173":  true, // vite dev
		"http://localhost:5173":  true,
		"http://[::1]:7373":      true,
		"wails://wails":          true, // webview (macOS)
		"http://wails.localhost": true, // webview (windows/webview2)
		"https://evil.com":       false,
		"http://192.168.1.5":     false,
	}
	for origin, want := range cases {
		if got := allowedOrigin(origin); got != want {
			t.Errorf("allowedOrigin(%q) = %v, want %v", origin, got, want)
		}
	}
}

func TestRequestToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/x", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	if got := requestToken(r); got != "abc123" {
		t.Errorf("bearer token = %q, want abc123", got)
	}
	// WebSocket handshakes carry the token as the second Sec-WebSocket-Protocol value.
	r2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/sessions/x/ws", nil)
	r2.Header.Set("Sec-WebSocket-Protocol", "osb.bearer, q9")
	if got := requestToken(r2); got != "q9" {
		t.Errorf("ws subprotocol token = %q, want q9", got)
	}

	// A URL query token is NOT accepted (ADR-0061: header-only).
	r3 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/x?token=nope", nil)
	if got := requestToken(r3); got != "" {
		t.Errorf("query token must be ignored, got %q", got)
	}
}

// TestSecurityEnforced covers the token + Host gate when a token is configured.
func TestSecurityEnforced(t *testing.T) {
	s := &Server{authToken: "s3cret"}
	h := s.withSecurity(http.HandlerFunc(okHandler))

	do := func(method, url, authHeader, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, nil)
		if authHeader != "" {
			r.Header.Set("Authorization", authHeader)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	t.Run("no token → 401", func(t *testing.T) {
		if w := do(http.MethodGet, "http://127.0.0.1:7373/v1/capabilities", "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
	})
	t.Run("wrong token → 401", func(t *testing.T) {
		if w := do(http.MethodGet, "http://127.0.0.1:7373/v1/capabilities", "Bearer nope", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401", w.Code)
		}
	})
	t.Run("correct bearer → 200", func(t *testing.T) {
		if w := do(http.MethodGet, "http://127.0.0.1:7373/v1/capabilities", "Bearer s3cret", ""); w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
	})
	t.Run("URL ?token= is rejected → 401", func(t *testing.T) {
		if w := do(http.MethodGet, "http://127.0.0.1:7373/v1/capabilities?token=s3cret", "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d, want 401 (query token must not authenticate)", w.Code)
		}
	})
	t.Run("WebSocket subprotocol token → 200", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7373/v1/sessions/x/ws", nil)
		r.Header.Set("Sec-WebSocket-Protocol", "osb.bearer, s3cret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
	})
	t.Run("non-loopback Host → 403 even with token", func(t *testing.T) {
		if w := do(http.MethodGet, "http://evil.com/v1/capabilities", "Bearer s3cret", ""); w.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", w.Code)
		}
	})
	t.Run("healthz is public (no token)", func(t *testing.T) {
		if w := do(http.MethodGet, "http://127.0.0.1:7373/healthz", "", ""); w.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", w.Code)
		}
	})
	t.Run("OPTIONS preflight bypasses token, reflects allowed origin", func(t *testing.T) {
		w := do(http.MethodOptions, "http://127.0.0.1:7373/v1/capabilities", "", "http://localhost:5173")
		if w.Code != http.StatusNoContent {
			t.Fatalf("code = %d, want 204", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
			t.Fatalf("ACAO = %q, want the loopback origin reflected", got)
		}
	})
	t.Run("disallowed origin gets no ACAO", func(t *testing.T) {
		w := do(http.MethodOptions, "http://127.0.0.1:7373/v1/capabilities", "", "https://evil.com")
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("ACAO = %q, want empty for a disallowed origin", got)
		}
	})
}

// TestSecurityDisabled confirms that with no token configured (unit-test mode) neither the token nor
// the Host check is enforced, preserving existing api-package tests.
func TestSecurityDisabled(t *testing.T) {
	s := &Server{authToken: ""}
	h := s.withSecurity(http.HandlerFunc(okHandler))
	r := httptest.NewRequest(http.MethodGet, "http://evil.com/v1/capabilities", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (auth disabled)", w.Code)
	}
}
