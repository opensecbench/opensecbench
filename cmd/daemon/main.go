// Command daemon runs the OpenSecBench control plane headless on a loopback address.
//
// The Wails desktop app boots this same control plane in-process; the daemon is the headless
// entrypoint used for the CLI, automation, and future team deployments (ADR-0001).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opensecbench/opensecbench/pkg/controlplane"
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
	cp, err := controlplane.Start(controlplane.Options{Addr: addr, DBPath: dbPath})
	if err != nil {
		return err
	}
	schemaVersion, _ := cp.SchemaVersion()
	log.Printf("opensecbench control plane %s ready at %s (schema version %d)", version.Version, cp.BaseURL, schemaVersion)
	log.Printf("Analyst provider: %s (configure via OSB_LLM_PROVIDER / OSB_LLM_BASE_URL / OSB_LLM_MODEL / OSB_LLM_API_KEY)", cp.ProviderName())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return cp.Shutdown(shutdownCtx)
}
