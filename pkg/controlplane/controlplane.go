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
	"github.com/opensecbench/opensecbench/pkg/runner"
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
	BaseURL string
	db      *store.DB
	srv     *http.Server
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

	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	srv := &http.Server{
		Handler:           api.New(api.Deps{Store: db, Engine: engine, CAS: blobs}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()

	return &Instance{BaseURL: "http://" + ln.Addr().String(), db: db, srv: srv}, nil
}

// SchemaVersion returns the applied schema version.
func (i *Instance) SchemaVersion() (int, error) { return i.db.Version() }

// Shutdown stops the HTTP server and closes the database.
func (i *Instance) Shutdown(ctx context.Context) error {
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
