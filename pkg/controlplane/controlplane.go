// Package controlplane boots the OpenSecBench control plane: it opens the database, applies
// migrations, and serves the HTTP API on a loopback listener. Both the headless daemon and the
// desktop app start the control plane through here so they share one code path (ADR-0001).
package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/api"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/extension"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/proxy"
	"github.com/opensecbench/opensecbench/pkg/report"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/session"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// Options configures a control-plane instance.
type Options struct {
	// Addr is the loopback address to listen on. Use a ":0" port to auto-assign a free port
	// (handy for the desktop app). Defaults to 127.0.0.1:7373.
	Addr string
	// RunnerAddr, if set, binds a separate listener serving only the remote-runner protocol
	// (`/v1/runners/{enroll,stream,result}`, ADR-0024). Typically a routable address behind an
	// operator-provided TLS terminator/tunnel. Empty disables remote runners.
	RunnerAddr string
	// DBPath is the SQLite path. Empty uses the per-user default location.
	DBPath string
}

// Instance is a running control plane.
type Instance struct {
	BaseURL   string
	RunnerURL string
	// Token is the local API bearer token (ADR-0061). The desktop app hands it to the webview over
	// the Wails bridge; the CLI reads it from APITokenPath(dataDir). Clients present it as
	// `Authorization: Bearer <token>` (or `?token=` where a header can't be set).
	Token string
	mgr   *store.Manager
	srv       *http.Server
	runnerSrv *http.Server
	api       *api.Server
	provider  llm.Provider
}

// ProviderName reports the configured LLM provider (or "none").
func (i *Instance) ProviderName() string {
	if i.provider == nil {
		return "none"
	}
	return i.provider.Name()
}

// Start opens the database, applies migrations, and begins serving the API. The listener is
// bound before returning, so BaseURL is immediately usable by clients.
func Start(opts Options) (*Instance, error) {
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:7373"
	}
	if opts.DBPath == "" {
		p, err := DefaultDBPath()
		if err != nil {
			return nil, err
		}
		opts.DBPath = p
	}

	// Two-tier storage (ADR-0049): an instance-wide global.db plus an on-demand per-project database
	// under projects/<id>/, all rooted at the data directory beside the configured DB path. This is the
	// physical split — each engagement's rows live in its own database, so purge/backup/migrate operate
	// on one self-contained directory.
	dataDir := filepath.Dir(opts.DBPath)
	mgr, err := store.OpenManager(dataDir, migrations.Global(), migrations.Project())
	if err != nil {
		return nil, err
	}
	db := mgr.Global()

	// Per-project content store (ADR-0049): each project's blobs live under projects/<id>/cas, beside its
	// database, so evidence moves with the project on purge/backup/migrate.
	casr := cas.NewPerProject(dataDir)
	// Build the capability + methodology registries from built-ins, then load installed extension
	// packages into them (ADR-0013). Extensions live under <data>/extensions; unsigned packages load
	// only when OSB_ALLOW_UNSIGNED_EXTENSIONS is set.
	capReg := capability.BuiltIns()
	methReg := methodology.BuiltIns()
	reportReg := report.BuiltIns()
	extDir := filepath.Join(filepath.Dir(opts.DBPath), "extensions")
	trust, _ := extension.LoadTrustStore(filepath.Join(extDir, "trusted_keys"))
	allowUnsigned := os.Getenv("OSB_ALLOW_UNSIGNED_EXTENSIONS") != ""
	loadedExt, extErrs := extension.LoadDir(extDir, trust, allowUnsigned)
	for _, l := range loadedExt {
		for _, c := range l.CapabilityList() {
			capReg.Register(c)
		}
		for _, m := range l.Manifest.Methodologies {
			methReg.Register(m)
		}
		for _, rd := range l.Manifest.Reports {
			if err := reportReg.Add(rd.ID, rd.Title, rd.Kind, rd.MD, rd.HTML); err != nil {
				log.Printf("extension %s: report %s skipped: %v", l.Manifest.ID, rd.ID, err)
			}
		}
		log.Printf("extension loaded: %s v%s (trusted=%v, %d caps)", l.Manifest.ID, l.Manifest.Version, l.Trusted, len(l.Manifest.Capabilities))
	}
	for dir, e := range extErrs {
		log.Printf("extension skipped (%s): %v", dir, e)
	}

	// Load user-authored methodology packs (ADR-0055) into the registry so adoption, coverage, and item
	// lookup treat them exactly like built-ins. Registered after extensions so a saved edit of an extension
	// pack (a saved copy under a new id) never clobbers the source.
	if saved, err := db.ListSavedMethodologies(context.Background()); err != nil {
		log.Printf("saved methodologies: load failed: %v", err)
	} else {
		for _, sm := range saved {
			var m methodology.Methodology
			if err := json.Unmarshal(sm.Data, &m); err != nil {
				log.Printf("saved methodology %s skipped: %v", sm.ID, err)
				continue
			}
			methReg.Register(m)
		}
	}

	// Load user-authored report templates so generation treats them like built-ins. Registered after
	// extensions so a saved fork (under a new id) never clobbers a shipped or extension template.
	if saved, err := db.ListReportTemplates(context.Background()); err != nil {
		log.Printf("report templates: load failed: %v", err)
	} else {
		for _, rt := range saved {
			if err := reportReg.Add(rt.ID, rt.Title, rt.Kind, rt.MD, rt.HTML); err != nil {
				log.Printf("saved report template %s skipped: %v", rt.ID, err)
			}
		}
	}

	engine := task.NewEngine(mgr, casr, capReg, runner.LocalRunner{})
	// Enable the deps.dev outdated-dependency enrichment (fires after a syft SBOM completes).
	engine.SetOutdatedChecker(&http.Client{Timeout: 20 * time.Second})

	// The LLM provider is configured via OSB_LLM_* (ollama/deepseek/grok/claude-cli/anthropic);
	// unset yields a mock. A misconfiguration disables the Analyst but never blocks startup.
	provider, err := llm.FromEnv()
	if err != nil {
		provider = nil
	}

	// Interactive terminals need Docker; without it the session endpoints report unavailable
	// rather than blocking startup.
	var sessMgr *session.Manager
	if session.Available() {
		// Terminals open into a toolchain image (e.g. one built from pkg/session/Dockerfile with git/python/
		// cloud CLIs baked in); empty uses the minimal default. The runtime.session_image setting takes
		// precedence over OSB_SESSION_IMAGE.
		img := os.Getenv("OSB_SESSION_IMAGE")
		if v, err := mgr.Global().GetSetting(context.Background(), "runtime.session_image"); err == nil && v != "" {
			img = v
		}
		sessMgr = session.NewManager(img)
	}

	// The intercepting proxy's CA is generated/persisted next to the database; a failure disables
	// the proxy but never blocks startup.
	proxyCA, err := proxy.LoadOrCreate(filepath.Join(filepath.Dir(opts.DBPath), "proxy-ca"))
	if err != nil {
		proxyCA = nil
	}

	// The vault master key is resolved from OSB_VAULT_KEY or a 0600 key file beside the DB; a
	// failure disables the vault (secret endpoints report unavailable) but never blocks startup.
	vault, err := secret.LoadVault(filepath.Dir(opts.DBPath))
	if err != nil {
		vault = nil
	}
	// Let the engine resolve secret references at exec time (ADR-0011).
	if vault != nil {
		engine.Secrets = func(ctx context.Context, name string) (string, error) {
			sealed, err := db.GetSealed(ctx, name)
			if err != nil {
				return "", err
			}
			v, err := vault.Open(sealed)
			return string(v), err
		}
	}

	// Local API bearer token (ADR-0061): loaded/created 0600 beside the DB. Every non-loopback caller
	// and every other-OS-user process is gated on it; the desktop webview gets it over the Wails
	// bridge, the CLI reads it from the file. A failure here is fatal — we don't serve an open API.
	authToken, err := LoadOrCreateAPIToken(dataDir)
	if err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("api token: %w", err)
	}

	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		_ = mgr.Close()
		return nil, err
	}
	apiSrv := api.New(api.Deps{
		Store: mgr, Engine: engine, CASResolver: casr, Provider: provider,
		SessionMgr: sessMgr, ProxyCA: proxyCA, Vault: vault,
		Methods: methReg, Reports: reportReg, Extensions: loadedExt, TrustStore: trust, ExtDir: extDir,
		WorkspaceDir: filepath.Join(filepath.Dir(opts.DBPath), "workspace"),
		AuthToken:    authToken,
	})
	// Tell the API its own address so the proxy skips capturing the app's own control-plane traffic (the
	// desktop webview follows the system proxy and would otherwise flood the capture list).
	apiSrv.SetSelfAddr(ln.Addr().String())
	srv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()

	inst := &Instance{BaseURL: "http://" + ln.Addr().String(), Token: authToken, mgr: mgr, srv: srv, api: apiSrv, provider: provider}
	log.Printf("api: authenticated on %s (token at %s)", inst.BaseURL, APITokenPath(dataDir))

	// Optional network-exposed runner listener (ADR-0024): serves only the authenticated runner protocol.
	if opts.RunnerAddr != "" {
		rln, err := net.Listen("tcp", opts.RunnerAddr)
		if err != nil {
			_ = srv.Close()
			_ = mgr.Close()
			return nil, fmt.Errorf("runner listener: %w", err)
		}
		inst.runnerSrv = &http.Server{Handler: apiSrv.RunnerHandler(), ReadHeaderTimeout: 5 * time.Second}
		inst.RunnerURL = "http://" + rln.Addr().String()
		go func() { _ = inst.runnerSrv.Serve(rln) }()
	}

	return inst, nil
}

// SchemaVersion returns the applied schema version.
func (i *Instance) SchemaVersion() (int, error) { return i.mgr.Global().Version() }

// Shutdown stops the HTTP server, releases live resources (proxies, terminal sessions), and closes
// the database.
func (i *Instance) Shutdown(ctx context.Context) error {
	if i.api != nil {
		i.api.Close()
	}
	if i.runnerSrv != nil {
		_ = i.runnerSrv.Shutdown(ctx)
	}
	err := i.srv.Shutdown(ctx)
	if cerr := i.mgr.Close(); err == nil {
		err = cerr
	}
	return err
}

// DefaultDBPath returns the per-user database location, creating its directory.
func DefaultDBPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "data"
	} else {
		dir = filepath.Join(dir, "opensecbench")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return filepath.Join(dir, "opensecbench.db"), nil
}
