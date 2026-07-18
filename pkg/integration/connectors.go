package integration

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// jira creates an issue via the Jira REST API. Credential is "email:api_token" (basic auth).
type jira struct{ client *http.Client }

func (jira) Name() string { return "jira" }

func (j jira) PushFinding(ctx context.Context, cfg Config, f model.Finding) (Ref, error) {
	if cfg.ProjectKey == "" {
		return Ref{}, fmt.Errorf("jira: project key required")
	}
	body := map[string]any{
		"fields": map[string]any{
			"project":     map[string]string{"key": cfg.ProjectKey},
			"summary":     f.Title,
			"description": description(f),
			"issuetype":   map[string]string{"name": "Bug"},
		},
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/rest/api/2/issue"
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Credential))
	out, code, err := postJSON(ctx, j.client, url, body, func(r *http.Request) { r.Header.Set("Authorization", auth) })
	if err != nil {
		return Ref{}, err
	}
	if code >= 300 {
		return Ref{}, fmt.Errorf("jira: create issue returned %d", code)
	}
	key := str(out, "key")
	if key == "" {
		return Ref{}, fmt.Errorf("jira: no issue key in response")
	}
	return Ref{ID: key, URL: strings.TrimRight(cfg.BaseURL, "/") + "/browse/" + key}, nil
}

// defectDojo creates a finding via the DefectDojo API v2. Credential is the API token.
type defectDojo struct{ client *http.Client }

func (defectDojo) Name() string { return "defectdojo" }

func (d defectDojo) PushFinding(ctx context.Context, cfg Config, f model.Finding) (Ref, error) {
	body := map[string]any{
		"title":       f.Title,
		"severity":    capitalize(f.Severity), // DefectDojo expects Capitalized severities
		"description": description(f),
		"active":      true,
		"verified":    f.Status == model.FindingConfirmed,
	}
	if cfg.ProjectKey != "" {
		body["test"] = cfg.ProjectKey // a DefectDojo test id
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/api/v2/findings/"
	out, code, err := postJSON(ctx, d.client, url, body, func(r *http.Request) {
		r.Header.Set("Authorization", "Token "+cfg.Credential)
	})
	if err != nil {
		return Ref{}, err
	}
	if code >= 300 {
		return Ref{}, fmt.Errorf("defectdojo: create finding returned %d", code)
	}
	id := str(out, "id")
	if id == "" {
		return Ref{}, fmt.Errorf("defectdojo: no id in response")
	}
	return Ref{ID: id, URL: strings.TrimRight(cfg.BaseURL, "/") + "/finding/" + id}, nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
