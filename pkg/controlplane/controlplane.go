// Package controlplane boots the OpenSecBench control plane: it opens the database, applies
// migrations, and serves the HTTP API on a loopback listener. Both the headless daemon and the
// desktop app start the control plane through here so they share one code path (ADR-0001).
package controlplane

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/api"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/proxy"
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
	// DBPath is the SQLite path. Empty uses the per-user default location.
	DBPath string
}

// Instance is a running control plane.
type Instance struct {
	BaseURL  string
	db       *store.DB
	srv      *http.Server
	api      *api.Server
	provider llm.Provider
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

	db, err := store.Open(opts.DBPath)
	if err != nil {
		return nil, err
	}
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Apply(ms); err != nil {
		_ = db.Close()
		return nil, err
	}

	blobs, err := cas.Open(filepath.Join(filepath.Dir(opts.DBPath), "cas"))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), runner.LocalRunner{})

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
		sessMgr = session.NewManager("")
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

	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	apiSrv := api.New(api.Deps{
		Store: db, Engine: engine, CAS: blobs, Provider: provider,
		SessionMgr: sessMgr, ProxyCA: proxyCA, Vault: vault,
	})
	srv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()

	return &Instance{BaseURL: "http://" + ln.Addr().String(), db: db, srv: srv, api: apiSrv, provider: provider}, nil
}

// SchemaVersion returns the applied schema version.
func (i *Instance) SchemaVersion() (int, error) { return i.db.Version() }

// Shutdown stops the HTTP server, releases live resources (proxies, terminal sessions), and closes
// the database.
func (i *Instance) Shutdown(ctx context.Context) error {
	if i.api != nil {
		i.api.Close()
	}
	err := i.srv.Shutdown(ctx)
	if cerr := i.db.Close(); err == nil {
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
