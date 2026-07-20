package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/proxy"
	"github.com/opensecbench/opensecbench/pkg/runnerhub"
	"github.com/opensecbench/opensecbench/pkg/runnertunnel"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// enrollRunnerKeys mints a token and enrolls a runner, returning its id + base64 private key (no stream).
func enrollRunnerKeys(t *testing.T, mainURL, runnerURL string) (string, string) {
	t.Helper()
	var tok struct {
		Token string `json:"token"`
	}
	if code := postJSON(t, mainURL+"/v1/runners/enroll-token", `{"label":"edge"}`, &tok); code != http.StatusCreated {
		t.Fatalf("enroll-token = %d", code)
	}
	pub, priv, err := runnerhub.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var enrolled struct {
		RunnerID string `json:"runner_id"`
	}
	if code := postJSON(t, runnerURL+"/v1/runners/enroll",
		`{"token":"`+tok.Token+`","name":"edge-1","pubkey":"`+pub+`"}`, &enrolled); code != http.StatusCreated {
		t.Fatalf("enroll = %d", code)
	}
	return enrolled.RunnerID, priv
}

// fakeTunnelAgent connects the streaming tunnel and, for each forward, performs the request and streams the
// response back — a faithful in-process stand-in for osb-runner's tunnel loop.
func fakeTunnelAgent(t *testing.T, runnerURL, id, priv string) {
	const path = "/v1/runners/tunnel"
	wsURL := strings.Replace(runnerURL, "http", "ws", 1) + path
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := runnerhub.Sign(priv, http.MethodGet, path, ts, nil)
	if err != nil {
		return
	}
	hdr := http.Header{}
	hdr.Set(runnerhub.HeaderRunnerID, id)
	hdr.Set(runnerhub.HeaderTime, ts)
	hdr.Set(runnerhub.HeaderSig, sig)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		return
	}
	sess := runnertunnel.New(conn, false)
	for {
		st, err := sess.Accept()
		if err != nil {
			return
		}
		go func(st *runnertunnel.Stream) {
			defer func() { _ = st.Close() }()
			var m runnerhub.TunnelForward
			if err := json.Unmarshal(st.Meta(), &m); err != nil {
				return
			}
			req, err := http.NewRequest(m.Method, m.URL, nil)
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer func() { _ = resp.Body.Close() }()
			_ = resp.Write(st)
			_ = st.CloseWrite()
		}(st)
	}
}

func TestProxyEgressViaRunnerStreams(t *testing.T) {
	// An upstream target that streams a body far larger than any buffered cap.
	const bigN = 8 << 20 // 8 MiB
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-From-Target", "yes")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		chunk := bytes.Repeat([]byte("x"), 32*1024)
		for written := 0; written < bigN; {
			n, _ := w.Write(chunk)
			written += n
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer target.Close()

	// Control plane with a proxy CA.
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	ca, err := proxy.LoadOrCreate(filepath.Join(t.TempDir(), "proxy-ca"))
	if err != nil {
		t.Fatal(err)
	}
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), fakeTaskRunner{})
	srvObj := New(Deps{Store: store.NewCombinedManager(db), Engine: engine, CAS: blobs, ProxyCA: ca})
	main := httptest.NewServer(srvObj.Handler())
	runnerSrv := httptest.NewServer(srvObj.RunnerHandler())
	t.Cleanup(func() { main.Close(); runnerSrv.Close(); srvObj.Close(); engine.Close(); _ = db.Close() })

	runnerID, priv := enrollRunnerKeys(t, main.URL, runnerSrv.URL)
	go fakeTunnelAgent(t, runnerSrv.URL, runnerID, priv)

	proj, _ := db.CreateProject(context.Background(), store.NewProject{Name: "p"})

	// Start the project proxy egressing via the runner — retry until the runner's tunnel has connected.
	var pstatus proxyStatus
	for i := 0; i < 300; i++ {
		var st proxyStatus
		code := postJSON(t, main.URL+"/v1/projects/"+proj.ID+"/proxy/start", `{"runner_id":"`+runnerID+`"}`, &st)
		if code == http.StatusCreated {
			pstatus = st
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pstatus.Port == 0 {
		t.Fatal("proxy never started via the runner tunnel")
	}
	if pstatus.Egress != runnerID {
		t.Fatalf("proxy egress = %q, want the runner id", pstatus.Egress)
	}

	// Drive a request through the proxy (plain-HTTP path) and read the full streamed body.
	proxyURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(pstatus.Port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(target.URL + "/big")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != bigN {
		t.Fatalf("client received %d bytes, want %d (streaming through the tunnel)", len(got), bigN)
	}
	if resp.Header.Get("X-From-Target") != "yes" {
		t.Fatal("response header from the target was lost through the tunnel")
	}

	// The captured proxy exchange records the runner as its egress vantage.
	var found bool
	for i := 0; i < 200 && !found; i++ {
		exs, _ := db.ListExchangesByProject(context.Background(), proj.ID)
		for _, ex := range exs {
			if ex.Origin == "proxy" && ex.Egress == runnerID {
				found = true
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("no proxy exchange recorded with egress = the runner")
	}
}

func TestProxyStartViaOfflineRunnerRejected(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	ca, _ := proxy.LoadOrCreate(filepath.Join(t.TempDir(), "proxy-ca"))
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), fakeTaskRunner{})
	srvObj := New(Deps{Store: store.NewCombinedManager(db), Engine: engine, CAS: blobs, ProxyCA: ca})
	main := httptest.NewServer(srvObj.Handler())
	t.Cleanup(func() { main.Close(); srvObj.Close(); engine.Close(); _ = db.Close() })

	// An enrolled runner with no tunnel connected can't be an egress vantage.
	rn, _ := db.CreateRunner(context.Background(), "edge", "cHVia2V5")
	proj, _ := db.CreateProject(context.Background(), store.NewProject{Name: "p"})
	var body map[string]any
	if code := postJSON(t, main.URL+"/v1/projects/"+proj.ID+"/proxy/start", `{"runner_id":"`+rn.ID+`"}`, &body); code != http.StatusBadGateway {
		t.Fatalf("start via offline runner = %d, want 502", code)
	}
}
