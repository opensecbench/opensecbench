// Command osb-runner is the OpenSecBench remote runner agent (ADR-0024). It dials home to the control
// plane over an operator-secured transport (TLS/tunnel), authenticates with an ed25519 key established
// at enrollment, and executes dispatched capability tasks from its own network vantage — so scans and
// probes originate here (inside a target network, a different region) rather than from the control-plane
// host. Governance (scope, audit) stays control-plane-side; this agent only executes.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/opensecbench/opensecbench/pkg/replay"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/runnerhub"
)

// identity is the runner's persisted credentials, written 0600 after enrollment.
type identity struct {
	RunnerID string `json:"runner_id"`
	Name     string `json:"name"`
	PrivKey  string `json:"priv_key"` // base64 ed25519 private key
}

func main() {
	url := flag.String("url", "", "control-plane runner endpoint (e.g. https://cp.example:7374)")
	enroll := flag.String("enroll", "", "one-time enrollment token (first run only)")
	name := flag.String("name", "", "runner display name (first run only)")
	dataDir := flag.String("data", defaultDataDir(), "directory for the runner's identity")
	flag.Parse()

	if *url == "" {
		log.Fatal("--url is required")
	}
	*url = strings.TrimRight(*url, "/")
	if err := run(*url, *enroll, *name, *dataDir); err != nil {
		log.Fatal(err)
	}
}

func run(url, enrollToken, name, dataDir string) error {
	if !runner.Available() {
		log.Print("warning: docker not found on PATH — dispatched tasks will fail until it is available")
	}
	id, err := loadOrEnroll(url, enrollToken, name, dataDir)
	if err != nil {
		return err
	}
	log.Printf("osb-runner %q (%s) connecting to %s", id.Name, id.RunnerID, url)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := &agent{url: url, id: id, client: &http.Client{}, cancels: map[string]context.CancelFunc{}}
	// Reconnect loop: the stream drops on network blips or control-plane restarts; back off and retry.
	for ctx.Err() == nil {
		if err := a.stream(ctx); err != nil && ctx.Err() == nil {
			log.Printf("stream ended: %v; reconnecting in 3s", err)
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
			}
		}
	}
	return nil
}

type agent struct {
	url    string
	id     identity
	client *http.Client

	mu      sync.Mutex
	cancels map[string]context.CancelFunc // taskID -> cancel, for in-flight local runs
}

// stream opens the signed SSE dispatch channel and handles messages until it closes.
func (a *agent) stream(ctx context.Context) error {
	const path = "/v1/runners/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url+path, nil)
	if err != nil {
		return err
	}
	a.signHeaders(req, http.MethodGet, path, nil)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stream returned %s", resp.Status)
	}
	log.Print("connected")

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // dispatches can carry a large RunSpec
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
			go a.execute(ctx, d)
		case runnerhub.KindCancel:
			a.cancel(d.TaskID)
		case runnerhub.KindHTTP:
			if d.HTTP != nil {
				go a.doHTTP(ctx, *d.HTTP)
			}
		}
	}
	return sc.Err()
}

// execute runs a dispatched task locally and posts the result back.
func (a *agent) execute(parent context.Context, d runnerhub.Dispatch) {
	ctx, cancel := context.WithCancel(parent)
	a.mu.Lock()
	a.cancels[d.TaskID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.cancels, d.TaskID)
		a.mu.Unlock()
		cancel()
	}()

	res, err := runner.LocalRunner{}.Run(ctx, d.Spec)
	if err != nil {
		// A run that couldn't be carried out is reported as a non-zero exit with the error on stderr.
		res = runner.Result{ExitCode: 1, Stderr: []byte(err.Error())}
	}
	a.postResult(d.TaskID, res)
}

// doHTTP performs an outbound HTTP request from this runner's vantage (Replay egress, ADR-0025) and posts
// the captured response back. Scope was already enforced control-plane-side before dispatch.
func (a *agent) doHTTP(ctx context.Context, req runnerhub.HTTPRequest) {
	res := runnerhub.HTTPResult{ID: req.ID}
	resp, err := replay.New(0).Send(ctx, replay.Request{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body})
	if err != nil {
		res.Error = err.Error()
	} else {
		res.Status = resp.Status
		res.Headers = resp.Headers
		res.Body = resp.Body
		res.DurationMs = int64(resp.DurationMS)
	}
	const path = "/v1/runners/http-result"
	body, _ := json.Marshal(res)
	httpReq, err := http.NewRequest(http.MethodPost, a.url+path, bytes.NewReader(body))
	if err != nil {
		log.Printf("http-result for %s: %v", req.ID, err)
		return
	}
	a.signHeaders(httpReq, http.MethodPost, path, body)
	hresp, err := a.client.Do(httpReq)
	if err != nil {
		log.Printf("http-result for %s: %v", req.ID, err)
		return
	}
	_ = hresp.Body.Close()
	if hresp.StatusCode >= 300 {
		log.Printf("http-result for %s rejected: %s", req.ID, hresp.Status)
	}
}

func (a *agent) cancel(taskID string) {
	a.mu.Lock()
	cancel := a.cancels[taskID]
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *agent) postResult(taskID string, res runner.Result) {
	const path = "/v1/runners/result"
	body, _ := json.Marshal(map[string]any{
		"task_id":     taskID,
		"exit_code":   res.ExitCode,
		"stdout":      res.Stdout,
		"stderr":      res.Stderr,
		"duration_ms": res.Duration.Milliseconds(),
	})
	req, err := http.NewRequest(http.MethodPost, a.url+path, bytes.NewReader(body))
	if err != nil {
		log.Printf("result for %s: %v", taskID, err)
		return
	}
	a.signHeaders(req, http.MethodPost, path, body)
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("result for %s: %v", taskID, err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("result for %s rejected: %s", taskID, resp.Status)
	}
}

func (a *agent) signHeaders(req *http.Request, method, path string, body []byte) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := runnerhub.Sign(a.id.PrivKey, method, path, ts, body)
	if err != nil {
		log.Printf("signing: %v", err)
		return
	}
	req.Header.Set(runnerhub.HeaderRunnerID, a.id.RunnerID)
	req.Header.Set(runnerhub.HeaderTime, ts)
	req.Header.Set(runnerhub.HeaderSig, sig)
}

// loadOrEnroll returns the persisted identity, enrolling first if none exists and a token was supplied.
func loadOrEnroll(url, token, name, dataDir string) (identity, error) {
	path := filepath.Join(dataDir, "runner.json")
	if b, err := os.ReadFile(path); err == nil {
		var id identity
		if err := json.Unmarshal(b, &id); err != nil {
			return identity{}, fmt.Errorf("reading %s: %w", path, err)
		}
		return id, nil
	}
	if token == "" {
		return identity{}, fmt.Errorf("no identity at %s and no --enroll token given", path)
	}
	if name == "" {
		name, _ = os.Hostname()
		if name == "" {
			name = "runner"
		}
	}
	pub, priv, err := runnerhub.GenerateKeyPair()
	if err != nil {
		return identity{}, err
	}
	body, _ := json.Marshal(map[string]string{"token": token, "name": name, "pubkey": pub})
	resp, err := http.Post(url+"/v1/runners/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return identity{}, fmt.Errorf("enroll: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return identity{}, fmt.Errorf("enroll rejected: %s", resp.Status)
	}
	var out struct {
		RunnerID string `json:"runner_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return identity{}, err
	}
	id := identity{RunnerID: out.RunnerID, Name: name, PrivKey: priv}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return identity{}, err
	}
	b, _ := json.Marshal(id)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return identity{}, err
	}
	log.Printf("enrolled as %q (%s); identity saved to %s", name, out.RunnerID, path)
	return id, nil
}

func defaultDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "osb-runner-data"
	}
	return filepath.Join(dir, "opensecbench-runner")
}
