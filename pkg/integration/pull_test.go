package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefectDojoPull(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[
			{"id":101,"title":"SQLi in login","severity":"High","description":"param id","verified":true},
			{"id":102,"title":"Verbose error","severity":"Low","description":"stack trace","verified":false}
		]}`))
	}))
	defer srv.Close()

	conn, ok := BuiltIns().Get("defectdojo")
	if !ok {
		t.Fatal("no defectdojo connector")
	}
	puller, ok := conn.(Puller)
	if !ok {
		t.Fatal("defectdojo should implement Puller")
	}

	got, err := puller.Pull(context.Background(), Config{BaseURL: srv.URL, ProjectKey: "42", Credential: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Token tok" {
		t.Fatalf("auth header = %q, want Token tok", gotAuth)
	}
	if gotPath != "/api/v2/findings/" || gotQuery == "" {
		t.Fatalf("request path=%q query=%q", gotPath, gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("pulled %d findings, want 2", len(got))
	}
	if got[0].ExternalID != "101" || got[0].Severity != "high" || !got[0].Confirmed {
		t.Fatalf("finding[0] = %+v, want id 101 / high / confirmed", got[0])
	}
	if got[1].Severity != "low" || got[1].Confirmed {
		t.Fatalf("finding[1] = %+v, want low / unconfirmed", got[1])
	}
	if got[0].URL != srv.URL+"/finding/101" {
		t.Fatalf("url = %q", got[0].URL)
	}
}

// Jira is push-only — it must not satisfy Puller.
func TestJiraIsPushOnly(t *testing.T) {
	conn, _ := BuiltIns().Get("jira")
	if _, ok := conn.(Puller); ok {
		t.Fatal("jira should not implement Puller (push-only)")
	}
}
