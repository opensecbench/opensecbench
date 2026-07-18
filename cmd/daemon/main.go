// Command daemon runs the OpenSecBench control plane headless on a loopback address.
//
// The Wails desktop app boots this same control plane in-process; the daemon is the headless
// entrypoint used for the CLI, automation, and future team deployments (ADR-0001).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/api"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/version"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7373", "loopback address for the control-plane API")
	dbPath := flag.String("db", "", "path to the SQLite database (default: user config dir)")
	flag.Parse()

	if err := run(*addr, *dbPath); err != nil {
		log.Fatal(err)
	}
}

func run(addr, dbPath string) error {
	if dbPath == "" {
		var err error
		if dbPath, err = defaultDBPath(); err != nil {
			return err
		}
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		return err
	}
	applied, err := db.Apply(ms)
	if err != nil {
		return err
	}
	schemaVersion, err := db.Version()
	if err != nil {
		return err
	}
	log.Printf("database %s ready (applied %d migration(s), schema version %d)", dbPath, applied, schemaVersion)

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(db).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("opensecbench control plane %s listening on %s", version.Version, addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func defaultDBPath() (string, error) {
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
