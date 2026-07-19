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

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runnerhub"
	"github.com/opensecbench/opensecbench/pkg/store"
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

func (a *fakeAgent) sign(method, path string, body []byte) (id, ts, sig string) {
	ts = strconv.FormatInt(time.Now().Unix(), 10)
	s, err := runnerhub.Sign(a.priv, method, path, ts, body)
	if err != nil {
		a.t.Fatal(err)
	}
	return a.id, ts, s
}

func (a *fakeAgent) post(t *testing.T, path string, body any) *http.Response {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, a.runnerURL+path, bytes.NewReader(b))
	id, ts, sig := a.sign(http.MethodPost, path, b)
	req.Header.Set(runnerhub.HeaderRunnerID, id)
	req.Header.Set(runnerhub.HeaderTime, ts)
	req.Header.Set(runnerhub.HeaderSig, sig)
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
	id, ts, sig := a.sign(http.MethodGet, path, nil)
	req.Header.Set(runnerhub.HeaderRunnerID, id)
	req.Header.Set(runnerhub.HeaderTime, ts)
	req.Header.Set(runnerhub.HeaderSig, sig)
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
		if d.Kind != runnerhub.KindRun {
			continue
		}
		select {
		case a.gotDispatch <- d.TaskID:
		default:
		}
		// Answer with a canned result (as if the capability ran here).
		resp := a.post(a.t, "/v1/runners/result", map[string]any{
			"task_id": d.TaskID, "exit_code": 0, "stdout": []byte("scanned from the runner\n"),
		})
		_ = resp.Body.Close()
	}
}

func TestRemoteRunnerEndToEnd(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), fakeTaskRunner{})
	srvObj := New(Deps{Store: db, Engine: engine, CAS: blobs})
	main := httptest.NewServer(srvObj.Handler())
	runnerSrv := httptest.NewServer(srvObj.RunnerHandler())
	t.Cleanup(func() { main.Close(); runnerSrv.Close(); engine.Close(); _ = db.Close() })

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
	if done.Runner != "" && done.Runner != "local-docker" {
		// provenance runner name recorded at create; not asserting exact value here
	}

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

func TestRunnerAuthRejectsBadSignature(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	_, _ = db.Apply(ms)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), fakeTaskRunner{})
	srvObj := New(Deps{Store: db, Engine: engine, CAS: blobs})
	runnerSrv := httptest.NewServer(srvObj.RunnerHandler())
	t.Cleanup(func() { runnerSrv.Close(); engine.Close(); _ = db.Close() })

	pub, _, _ := runnerhub.GenerateKeyPair()
	r, err := db.CreateRunner(context.Background(), "edge", pub)
	if err != nil {
		t.Fatal(err)
	}
	// A stream request signed with the WRONG key is rejected.
	_, wrongPriv, _ := runnerhub.GenerateKeyPair()
	const path = "/v1/runners/stream"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig, _ := runnerhub.Sign(wrongPriv, http.MethodGet, path, ts, nil)
	req, _ := http.NewRequest(http.MethodGet, runnerSrv.URL+path, nil)
	req.Header.Set(runnerhub.HeaderRunnerID, r.ID)
	req.Header.Set(runnerhub.HeaderTime, ts)
	req.Header.Set(runnerhub.HeaderSig, sig)
	resp, err := runnerSrv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad-signature stream = %d, want 401", resp.StatusCode)
	}
}
