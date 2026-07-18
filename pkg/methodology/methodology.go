// Package methodology is the static catalog of assessment checklists (ADR-0009). A Methodology is a
// versioned pack of technology-specific items; per-project coverage state lives in the store. Built-
// in packs ship first-party in the shape an extension pack will later provide (ADR-0003).
package methodology

import (
	"sort"
	"sync"
)

// Item is one checklist check within a methodology.
type Item struct {
	ID                    string   `json:"id"` // pack-scoped, e.g. "web-app/idor"
	Title                 string   `json:"title"`
	Objective             string   `json:"objective"`
	Procedure             string   `json:"procedure"`
	Standards             []string `json:"standards,omitempty"`              // e.g. "OWASP ASVS V4", "CWE-639"
	SuggestedCapabilities []string `json:"suggested_capabilities,omitempty"` // capability ids that help
}

// Methodology is a pack of checklist items for a technology/domain.
type Methodology struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Tech    string `json:"tech"`
	Version string `json:"version"`
	// Keywords let the pack self-describe applicability: if any appears in a target's knowledge
	// base, the pack is suggested for adoption (ADR-0009/ADR-0010 tie-in).
	Keywords []string `json:"keywords,omitempty"`
	Items    []Item   `json:"items"`
}

// Registry holds methodologies by id. Safe for concurrent use (runtime extension registration).
type Registry struct {
	mu    sync.RWMutex
	packs map[string]Methodology
}

// Get returns a methodology by id.
func (r *Registry) Get(id string) (Methodology, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.packs[id]
	return m, ok
}

// Register adds (or replaces) a methodology pack — used to load extension-provided packs.
func (r *Registry) Register(m Methodology) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.packs[m.ID] = m
}

// All returns every methodology, sorted by id.
func (r *Registry) All() []Methodology {
	r.mu.RLock()
	out := make([]Methodology, 0, len(r.packs))
	for _, m := range r.packs {
		out = append(out, m)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Item looks up a single item across all packs by its (pack-scoped) id.
func (r *Registry) Item(itemID string) (Item, Methodology, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.packs {
		for _, it := range m.Items {
			if it.ID == itemID {
				return it, m, true
			}
		}
	}
	return Item{}, Methodology{}, false
}

// BuiltIns returns the first-party methodology packs.
func BuiltIns() *Registry {
	r := &Registry{packs: map[string]Methodology{}}
	for _, m := range []Methodology{webApp, restAPI, oidcOAuth} {
		r.packs[m.ID] = m
	}
	return r
}

var webApp = Methodology{
	ID: "web-app", Title: "Web Application", Tech: "web", Version: "1.0.0",
	Keywords: []string{"web", "http", "https", "browser", "cookie", "xss", "csrf", "html", "frontend", "webapp"},
	Items: []Item{
		{ID: "web-app/access-control-idor", Title: "Broken access control / IDOR",
			Objective: "Confirm object-level and function-level authorization is enforced server-side.",
			Procedure: "Enumerate resource identifiers and cross-account/role access to each endpoint; attempt horizontal and vertical privilege escalation.",
			Standards: []string{"OWASP ASVS V4", "CWE-639", "OWASP Top 10 A01"}},
		{ID: "web-app/authn-session", Title: "Authentication & session management",
			Objective: "Verify credential handling, session lifecycle, and fixation/rotation.",
			Procedure: "Test login/logout, session token entropy and rotation on privilege change, timeout, and concurrent sessions.",
			Standards: []string{"OWASP ASVS V2/V3", "CWE-287"}},
		{ID: "web-app/injection-sqli", Title: "Injection (SQL/command/template)",
			Objective:             "Find injection into interpreters.",
			Procedure:             "Fuzz parameters for SQL/OS/template injection; review data-access code paths.",
			Standards:             []string{"OWASP ASVS V5", "CWE-89"},
			SuggestedCapabilities: []string{"semgrep"}},
		{ID: "web-app/xss", Title: "Cross-site scripting",
			Objective:             "Find reflected/stored/DOM XSS.",
			Procedure:             "Inject markup into reflected and stored sinks; review output encoding.",
			Standards:             []string{"OWASP ASVS V5", "CWE-79"},
			SuggestedCapabilities: []string{"semgrep"}},
		{ID: "web-app/csrf", Title: "Cross-site request forgery",
			Objective: "Verify state-changing requests are CSRF-protected.",
			Procedure: "Check anti-CSRF tokens / SameSite cookies on state-changing endpoints.",
			Standards: []string{"OWASP ASVS V4", "CWE-352"}},
		{ID: "web-app/secrets", Title: "Hardcoded secrets & sensitive data",
			Objective:             "Ensure no secrets in source and sensitive data is protected in transit/at rest.",
			Procedure:             "Scan source for secrets; review TLS and storage of sensitive data.",
			Standards:             []string{"OWASP ASVS V6", "CWE-798"},
			SuggestedCapabilities: []string{"trufflehog", "semgrep"}},
		{ID: "web-app/security-headers", Title: "Security headers & transport",
			Objective:             "Confirm hardening headers and HTTPS enforcement.",
			Procedure:             "Check HSTS, CSP, X-Content-Type-Options, cookie flags, and redirect-to-HTTPS.",
			Standards:             []string{"OWASP ASVS V14", "CWE-693"},
			SuggestedCapabilities: []string{"http-probe"}},
	},
}

var restAPI = Methodology{
	ID: "rest-api", Title: "REST API", Tech: "api", Version: "1.0.0",
	Keywords: []string{"rest", "api", "graphql", "openapi", "swagger", "endpoint", "microservice", "grpc"},
	Items: []Item{
		{ID: "rest-api/authz-per-endpoint", Title: "Per-endpoint authorization",
			Objective: "Every endpoint enforces authentication and authorization.",
			Procedure: "Enumerate routes; call each unauthenticated and as lower-privilege roles.",
			Standards: []string{"OWASP API Top 10 API1/API5", "CWE-285"}},
		{ID: "rest-api/mass-assignment", Title: "Mass assignment",
			Objective: "Clients cannot set protected fields.",
			Procedure: "Submit extra/privileged fields to create/update endpoints and observe binding.",
			Standards: []string{"OWASP API Top 10 API6", "CWE-915"}},
		{ID: "rest-api/rate-limiting", Title: "Rate limiting & resource consumption",
			Objective: "Endpoints resist abuse and unbounded consumption.",
			Procedure: "Test throttling on auth and expensive endpoints; check pagination limits.",
			Standards: []string{"OWASP API Top 10 API4", "CWE-770"}},
		{ID: "rest-api/input-validation", Title: "Input validation & error handling",
			Objective:             "Inputs are validated and errors don't leak internals.",
			Procedure:             "Send malformed/oversized inputs; inspect error verbosity and stack traces.",
			Standards:             []string{"OWASP ASVS V5", "CWE-20"},
			SuggestedCapabilities: []string{"semgrep"}},
	},
}

var oidcOAuth = Methodology{
	ID: "oidc-oauth", Title: "OIDC / OAuth 2.0", Tech: "auth", Version: "1.0.0",
	Keywords: []string{"oauth", "oidc", "openid", "saml", "sso", "jwt", "bearer", "okta", "auth0", "keycloak", "identity provider"},
	Items: []Item{
		{ID: "oidc-oauth/redirect-uri", Title: "Redirect URI validation",
			Objective: "Authorization server strictly validates redirect_uri.",
			Procedure: "Test partial/open redirect_uri, wildcard handling, and downgrade to attacker host.",
			Standards: []string{"RFC 6749 §3.1.2", "CWE-601"}},
		{ID: "oidc-oauth/state-csrf", Title: "State parameter / CSRF",
			Objective: "The state parameter binds the request to the user session.",
			Procedure: "Verify state is present, unguessable, and validated on callback.",
			Standards: []string{"RFC 6749 §10.12", "CWE-352"}},
		{ID: "oidc-oauth/pkce", Title: "PKCE for public clients",
			Objective: "Public clients use PKCE with S256.",
			Procedure: "Confirm code_challenge/verifier flow and that plain method is rejected.",
			Standards: []string{"RFC 7636"}},
		{ID: "oidc-oauth/token-handling", Title: "Token storage & validation",
			Objective:             "Tokens are validated (sig/aud/exp) and stored safely.",
			Procedure:             "Inspect JWT validation (alg, aud, exp) and client-side token storage.",
			Standards:             []string{"RFC 8725", "CWE-347"},
			SuggestedCapabilities: []string{"semgrep"}},
	},
}
