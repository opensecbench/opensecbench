package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/session"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestSessionUnavailableWithoutManager(t *testing.T) {
	srv := newTestServer(t) // built without a SessionMgr
	// Need a project id; create one.
	var proj model.Project
	postJSON(t, srv.URL+"/v1/projects", `{"name":"p"}`, &proj)
	code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/sessions", `{}`, nil)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("create session without manager = %d, want 503", code)
	}
}

func TestTerminalSessionEndToEnd(t *testing.T) {
	if !session.Available() {
		t.Skip("docker not available")
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: db, CAS: blobs, SessionMgr: session.NewManager("")}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })

	var proj model.Project
	postJSON(t, srv.URL+"/v1/projects", `{"name":"terminal"}`, &proj)

	var sess model.Session
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/sessions", `{"actor":"human:test"}`, &sess); code != http.StatusCreated {
		t.Fatalf("create session = %d", code)
	}
	if sess.Status != model.SessionActive || sess.Container == "" {
		t.Fatalf("unexpected session: %+v", sess)
	}

	// Attach over WebSocket.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/sessions/" + sess.ID + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","rows":30,"cols":100}`))
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("echo hello-osb\n")); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	for {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		out.Write(data)
		if strings.Contains(out.String(), "hello-osb") {
			break
		}
	}
	if !strings.Contains(out.String(), "hello-osb") {
		t.Fatalf("terminal output missing command; got %q", out.String())
	}
	_ = conn.Close()

	// The session finalizes shortly after the socket closes, capturing the transcript.
	var got model.Session
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		postJSON(t, srv.URL+"/v1/sessions/"+sess.ID+"/close", ``, &got) // idempotent finalize
		if got.Status == model.SessionClosed && got.TranscriptArtifactID != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if got.Status != model.SessionClosed || got.TranscriptArtifactID == nil {
		t.Fatalf("session not finalized with transcript: %+v", got)
	}

	// The transcript is retrievable and contains the command output.
	body := getBody(t, srv.URL+"/v1/artifacts/"+*got.TranscriptArtifactID+"/content")
	if !strings.Contains(body, "hello-osb") {
		t.Fatalf("transcript artifact missing output: %q", body)
	}

	// Promote the transcript to evidence.
	var obs model.Observation
	if code := postJSON(t, srv.URL+"/v1/sessions/"+sess.ID+"/evidence", `{"note":"ran recon"}`, &obs); code != http.StatusCreated {
		t.Fatalf("session evidence = %d", code)
	}
	if obs.Origin != model.OriginHuman || obs.ArtifactID == nil || *obs.ArtifactID != *got.TranscriptArtifactID {
		t.Fatalf("evidence observation wrong: %+v", obs)
	}
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return b.String()
}
