package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runnerhub"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// fakeAgent is an in-process runner: it enrolls, opens the signed SSE stream, and answers each dispatch
// with a canned result — exercising the full runner protocol without Docker.
type fakeAgent struct {
	t           *testing.T
	runnerURL   string
	id          string
	priv        string
	client      *http.Client
	gotDispatch chan string
}

func (a *fakeAgent) sign(method, path string, body []byte) (id, ts, sig, nonce string) {
	ts = strconv.FormatInt(time.Now().Unix(), 10)
	nonce, err := runnerhub.Nonce()
	if err != nil {
		a.t.Fatal(err)
	}
	s, err := runnerhub.Sign(a.priv, method, path, ts, nonce, body)
	if err != nil {
		a.t.Fatal(err)
	}
	return a.id, ts, s, nonce
}

func (a *fakeAgent) post(t *testing.T, path string, body any) *http.Response {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, a.runnerURL+path, bytes.NewReader(b))
	id, ts, sig, nonce := a.sign(http.MethodPost, path, b)
	req.Header.Set(runnerhub.HeaderRunnerID, id)
	req.Header.Set(runnerhub.HeaderTime, ts)
	req.Header.Set(runnerhub.HeaderSig, sig)
	req.Header.Set(runnerhub.HeaderNonce, nonce)
	resp, err := a.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// stream opens the SSE stream and, for each run dispatch, posts a canned success result. Runs until ctx
// is cancelled.
func (a *fakeAgent) stream(ctx context.Context) {
	const path = "/v1/runners/stream"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.runnerURL+path, nil)
	id, ts, sig, nonce := a.sign(http.MethodGet, path, nil)
	req.Header.Set(runnerhub.HeaderRunnerID, id)
	req.Header.Set(runnerhub.HeaderTime, ts)
	req.Header.Set(runnerhub.HeaderSig, sig)
	req.Header.Set(runnerhub.HeaderNonce, nonce)
	resp, err := a.client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var d runnerhub.Dispatch
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d); err != nil {
			continue
		}
		switch d.Kind {
		case runnerhub.KindRun:
			select {
			case a.gotDispatch <- d.TaskID:
			default:
			}
			// Answer with a canned result (as if the capability ran here).
			resp := a.post(a.t, "/v1/runners/result", map[string]any{
				"task_id": d.TaskID, "exit_code": 0, "stdout": []byte("scanned from the runner\n"),
			})
			_ = resp.Body.Close()
		case runnerhub.KindHTTP:
			select {
			case a.gotDispatch <- d.HTTP.ID:
			default:
			}
			// Answer with a canned HTTP response (as if the request went out from here).
			resp := a.post(a.t, "/v1/runners/http-result", runnerhub.HTTPResult{
				ID: d.HTTP.ID, Status: 200, Headers: "X-Via: runner\n", Body: "hello from the runner",
			})
			_ = resp.Body.Close()
		}
	}
}

func TestRemoteRunnerEndToEnd(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeTaskRunner{})
	srvObj := New(Deps{Store: store.NewCombinedManager(db), Engine: engine, CAS: blobs})
	main := httptest.NewServer(srvObj.Handler())
	runnerSrv := httptest.NewServer(srvObj.RunnerHandler())
	t.Cleanup(func() { main.Close(); runnerSrv.Close(); engine.Close() })

	// Operator mints an enrollment token.
	var tok struct {
		Token string `json:"token"`
	}
	if code := postJSON(t, main.URL+"/v1/runners/enroll-token", `{"label":"edge"}`, &tok); code != http.StatusCreated {
		t.Fatalf("enroll-token = %d", code)
	}

	// The runner generates a keypair and enrolls with the token.
	pub, priv, err := runnerhub.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var enrolled struct {
		RunnerID string `json:"runner_id"`
	}
	if code := postJSON(t, runnerSrv.URL+"/v1/runners/enroll",
		`{"token":"`+tok.Token+`","name":"edge-1","pubkey":"`+pub+`"}`, &enrolled); code != http.StatusCreated {
		t.Fatalf("enroll = %d", code)
	}
	if enrolled.RunnerID == "" {
		t.Fatal("no runner id returned")
	}

	// The runner connects its stream.
	agent := &fakeAgent{t: t, runnerURL: runnerSrv.URL, id: enrolled.RunnerID, priv: priv, client: runnerSrv.Client(), gotDispatch: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.stream(ctx)

	// Wait until the operator view shows the runner online.
	online := false
	for i := 0; i < 200 && !online; i++ {
		var list []struct {
			ID     string `json:"id"`
			Online bool   `json:"online"`
		}
		resp, _ := http.Get(main.URL + "/v1/runners")
		_ = json.NewDecoder(resp.Body).Decode(&list)
		_ = resp.Body.Close()
		for _, r := range list {
			if r.ID == enrolled.RunnerID && r.Online {
				online = true
			}
		}
		if !online {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !online {
		t.Fatal("runner never came online")
	}

	// Enqueue a task targeting the remote runner; it dispatches over the protocol and completes.
	tk, err := engine.Enqueue(context.Background(), task.RunRequest{
		CapabilityID: "source-inventory", TargetDir: "/repo", Actor: "human", RunnerID: enrolled.RunnerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	var done model.Task
	deadline := time.Now().Add(10 * time.Second)
	for {
		done, _ = db.GetTask(context.Background(), tk.ID)
		if done.Status == model.TaskSucceeded || done.Status == model.TaskFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never completed (status %q)", done.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if done.Status != model.TaskSucceeded {
		t.Fatalf("remote task = %s (err=%q)", done.Status, done.Error)
	}
	// done.Runner carries the provenance runner name recorded at create; we don't assert its exact value.

	// The dispatch reached the agent, and the runner's output landed as an artifact.
	select {
	case got := <-agent.gotDispatch:
		if got != tk.ID {
			t.Fatalf("agent got dispatch for %q, want %q", got, tk.ID)
		}
	default:
		t.Fatal("agent never received the dispatch")
	}
	arts, _ := db.ListArtifactsByTask(context.Background(), tk.ID)
	if len(arts) == 0 {
		t.Fatal("no artifact from remote run")
	}
	rc, _ := blobs.Open(arts[0].SHA256)
	defer func() { _ = rc.Close() }()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(rc)
	if !strings.Contains(buf.String(), "scanned from the runner") {
		t.Fatalf("artifact content = %q, want the runner's output", buf.String())
	}
}

// enrollOnlineRunner mints a token, enrolls a fake agent, opens its stream, and waits until the operator
// view shows it online. Returns the agent (with its runner id) for driving dispatches.
func enrollOnlineRunner(t *testing.T, mainURL, runnerURL string) (*fakeAgent, string) {
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
	agent := &fakeAgent{t: t, runnerURL: runnerURL, id: enrolled.RunnerID, priv: priv, client: &http.Client{}, gotDispatch: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go agent.stream(ctx)

	for i := 0; i < 200; i++ {
		var list []struct {
			ID     string `json:"id"`
			Online bool   `json:"online"`
		}
		resp, _ := http.Get(mainURL + "/v1/runners")
		_ = json.NewDecoder(resp.Body).Decode(&list)
		_ = resp.Body.Close()
		for _, r := range list {
			if r.ID == enrolled.RunnerID && r.Online {
				return agent, enrolled.RunnerID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runner never came online")
	return nil, ""
}

func TestReplayEgressViaRunner(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeTaskRunner{})
	srvObj := New(Deps{Store: store.NewCombinedManager(db), Engine: engine, CAS: blobs})
	main := httptest.NewServer(srvObj.Handler())
	runnerSrv := httptest.NewServer(srvObj.RunnerHandler())
	t.Cleanup(func() { main.Close(); runnerSrv.Close(); engine.Close() })

	_, runnerID := enrollOnlineRunner(t, main.URL, runnerSrv.URL)

	// A project + a draft exchange (no scope entries → any target allowed).
	proj, _ := db.CreateProject(context.Background(), store.NewProject{Name: "p"})
	ex, _ := db.CreateExchange(context.Background(), model.HTTPExchange{
		ProjectID: proj.ID, Origin: model.ExchangeReplay, Method: "GET", URL: "https://target.example/health",
	})

	// Send via the runner: the fake agent answers, and the response is recorded with egress = the runner.
	var sent model.HTTPExchange
	if code := postJSON(t, main.URL+"/v1/exchanges/"+ex.ID+"/send", `{"runner_id":"`+runnerID+`"}`, &sent); code != http.StatusOK {
		t.Fatalf("send via runner = %d", code)
	}
	if sent.Status == nil || *sent.Status != 200 || sent.ResponseBody != "hello from the runner" {
		t.Fatalf("runner-egress response not recorded: %+v", sent)
	}
	if sent.Egress != runnerID {
		t.Fatalf("egress = %q, want the runner id %q", sent.Egress, runnerID)
	}

	// An unknown/offline runner is rejected (no silent local fallback).
	var errBody map[string]any
	ex2, _ := db.CreateExchange(context.Background(), model.HTTPExchange{
		ProjectID: proj.ID, Origin: model.ExchangeReplay, Method: "GET", URL: "https://target.example/x",
	})
	if code := postJSON(t, main.URL+"/v1/exchanges/"+ex2.ID+"/send", `{"runner_id":"ghost"}`, &errBody); code != http.StatusBadGateway {
		t.Fatalf("send via unknown runner = %d, want 502", code)
	}
}

func TestRunnerAuthRejectsBadSignature(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeTaskRunner{})
	srvObj := New(Deps{Store: store.NewCombinedManager(db), Engine: engine, CAS: blobs})
	runnerSrv := httptest.NewServer(srvObj.RunnerHandler())
	t.Cleanup(func() { runnerSrv.Close(); engine.Close() })

	pub, _, _ := runnerhub.GenerateKeyPair()
	r, err := db.CreateRunner(context.Background(), "edge", pub)
	if err != nil {
		t.Fatal(err)
	}
	// A stream request signed with the WRONG key is rejected.
	_, wrongPriv, _ := runnerhub.GenerateKeyPair()
	const path = "/v1/runners/stream"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, _ := runnerhub.Nonce()
	sig, _ := runnerhub.Sign(wrongPriv, http.MethodGet, path, ts, nonce, nil)
	req, _ := http.NewRequest(http.MethodGet, runnerSrv.URL+path, nil)
	req.Header.Set(runnerhub.HeaderRunnerID, r.ID)
	req.Header.Set(runnerhub.HeaderTime, ts)
	req.Header.Set(runnerhub.HeaderSig, sig)
	req.Header.Set(runnerhub.HeaderNonce, nonce)
	resp, err := runnerSrv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad-signature stream = %d, want 401", resp.StatusCode)
	}

	// A validly signed request replayed verbatim is rejected the second time (ADR-0024 anti-replay).
	goodPub, goodPriv, _ := runnerhub.GenerateKeyPair()
	gr, err := db.CreateRunner(context.Background(), "replayer", goodPub)
	if err != nil {
		t.Fatal(err)
	}
	ts2 := strconv.FormatInt(time.Now().Unix(), 10)
	nonce2, _ := runnerhub.Nonce()
	sig2, _ := runnerhub.Sign(goodPriv, http.MethodPost, "/v1/runners/result", ts2, nonce2, []byte("{}"))
	do := func() int {
		rq, _ := http.NewRequest(http.MethodPost, runnerSrv.URL+"/v1/runners/result", bytes.NewReader([]byte("{}")))
		rq.Header.Set(runnerhub.HeaderRunnerID, gr.ID)
		rq.Header.Set(runnerhub.HeaderTime, ts2)
		rq.Header.Set(runnerhub.HeaderSig, sig2)
		rq.Header.Set(runnerhub.HeaderNonce, nonce2)
		rp, err := runnerSrv.Client().Do(rq)
		if err != nil {
			t.Fatal(err)
		}
		_ = rp.Body.Close()
		return rp.StatusCode
	}
	// First use passes auth (403 = past auth, task not assigned); the replay is stopped at auth (401).
	if got := do(); got == http.StatusUnauthorized {
		t.Fatalf("first signed request = 401, want past-auth")
	}
	if got := do(); got != http.StatusUnauthorized {
		t.Fatalf("replayed request = %d, want 401", got)
	}
}
