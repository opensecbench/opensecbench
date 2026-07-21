package enrich

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		installed, latest, drift string
		behind                   bool
	}{
		{"1.0.0", "2.0.0", "major", true},
		{"1.2.0", "1.5.0", "minor", true},
		{"1.2.3", "1.2.9", "patch", true},
		{"2.0.0", "2.0.0", "", false},
		{"2.1.0", "2.0.0", "", false}, // installed ahead
		{"v0.4.0", "v0.5.0", "minor", true},
		{"1.0", "1.0.1", "patch", true},
	}
	for _, c := range cases {
		drift, behind := compareVersions(c.installed, c.latest)
		if drift != c.drift || behind != c.behind {
			t.Errorf("compareVersions(%q,%q) = (%q,%v), want (%q,%v)", c.installed, c.latest, drift, behind, c.drift, c.behind)
		}
	}
}

func TestComponentFromPURL(t *testing.T) {
	cases := []struct {
		purl           string
		sys, name, ver string
		ok             bool
	}{
		{"pkg:pypi/flask@2.0.1", "PYPI", "flask", "2.0.1", true},
		{"pkg:npm/lodash@4.17.21", "NPM", "lodash", "4.17.21", true},
		{"pkg:golang/golang.org%2Fx%2Fnet@v0.4.0", "GO", "golang.org/x/net", "v0.4.0", true},
		{"pkg:maven/org.apache.commons/commons-lang3@3.12.0", "MAVEN", "org.apache.commons:commons-lang3", "3.12.0", true},
		{"pkg:cargo/serde@1.0.130", "CARGO", "serde", "1.0.130", true},
		{"pkg:deb/debian/curl@7.68", "", "", "", false}, // unsupported ecosystem
		{"not-a-purl", "", "", "", false},
	}
	for _, c := range cases {
		got, ok := componentFromPURL(c.purl)
		if ok != c.ok {
			t.Errorf("componentFromPURL(%q) ok=%v, want %v", c.purl, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Ecosystem != c.sys || got.Name != c.name || got.Version != c.ver {
			t.Errorf("componentFromPURL(%q) = %+v, want (%s,%s,%s)", c.purl, got, c.sys, c.name, c.ver)
		}
	}
}

// mockDoer answers deps.dev package queries from a name→latest map.
type mockDoer struct{ latest map[string]string }

func (m mockDoer) Do(req *http.Request) (*http.Response, error) {
	// URL: .../packages/{name}; the name is the last path segment (URL-escaped).
	parts := strings.Split(req.URL.Path, "/packages/")
	name := parts[len(parts)-1]
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	v, ok := m.latest[name]
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}
	body := `{"versions":[{"versionKey":{"version":"` + v + `"},"isDefault":true}]}`
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestCheckFlagsOutdated(t *testing.T) {
	comps := []Component{
		{Ecosystem: "PYPI", Name: "flask", Version: "2.0.1"},
		{Ecosystem: "NPM", Name: "lodash", Version: "4.17.21"},
		{Ecosystem: "PYPI", Name: "requests", Version: "2.31.0"}, // current
	}
	mock := mockDoer{latest: map[string]string{
		"flask":    "3.0.0",   // major behind
		"lodash":   "4.17.21", // current
		"requests": "2.31.0",  // current
	}}
	got := Checker{HTTP: mock, Concurrency: 2}.Check(context.Background(), comps)
	if len(got) != 1 {
		t.Fatalf("expected 1 outdated, got %d: %+v", len(got), got)
	}
	if got[0].Name != "flask" || got[0].Latest != "3.0.0" || got[0].Drift != "major" {
		t.Fatalf("unexpected outdated result: %+v", got[0])
	}
}
