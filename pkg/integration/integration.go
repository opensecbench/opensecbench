// Package integration pushes OSB findings to external issue trackers (P10). Connectors are HTTP
// clients invoked from the control plane under audit; credentials come from the vault (never
// hardcoded). DefectDojo/DependencyTrack/Jira share one Connector contract; more follow the shape.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Config carries per-push settings. Credential is the resolved secret value (opened from the vault
// by the caller), never a reference here.
type Config struct {
	BaseURL    string
	ProjectKey string
	Credential string
}

// Ref is the created external issue.
type Ref struct {
	ID  string
	URL string
}

// Connector pushes a finding into an external tracker.
type Connector interface {
	Name() string
	PushFinding(ctx context.Context, cfg Config, f model.Finding) (Ref, error)
}

// Registry holds connectors by name.
type Registry struct{ conns map[string]Connector }

// Get returns a connector by name.
func (r *Registry) Get(name string) (Connector, bool) { c, ok := r.conns[name]; return c, ok }

// Names lists available connector names.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.conns))
	for n := range r.conns {
		out = append(out, n)
	}
	return out
}

// BuiltIns returns the first-party connectors.
func BuiltIns() *Registry {
	return &Registry{conns: map[string]Connector{
		"jira":       jira{client: httpClient()},
		"defectdojo": defectDojo{client: httpClient()},
	}}
}

func httpClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }

func postJSON(ctx context.Context, client *http.Client, url string, body any, setAuth func(*http.Request)) (map[string]any, int, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	setAuth(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out, resp.StatusCode, nil
}

func description(f model.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Severity: %s\nStatus: %s\n", f.Severity, f.Status)
	if f.CWE != "" {
		fmt.Fprintf(&b, "CWE: %s\n", f.CWE)
	}
	if f.Description != "" {
		b.WriteString("\n")
		b.WriteString(f.Description)
	}
	b.WriteString("\n\n(Pushed from OpenSecBench)")
	return b.String()
}

func str(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return fmt.Sprintf("%.0f", t)
		}
	}
	return ""
}
