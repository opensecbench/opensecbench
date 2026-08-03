package capability

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/runner"
)

// Queue-first triage (ADR-0068): scanners do NOT auto-open investigations. Interpreted observations land in
// the review Queue, where reachability / exposure / route / severity are decision-support filters — the
// operator (or an agent pointed at the whole Queue) decides what to validate, so nothing important hides
// behind an auto-sorted "worklist". The one auto-route kept is a trufflehog-VERIFIED secret: it was
// live-checked against its provider, so it is a confirmed finding with no open question.
//
// The reachability/exposure enrichment of ADR-0030/0031/0032/0033/0034 still runs — it now informs
// prioritization (pills + filters) instead of routing. Supersedes the auto-investigate stance of those ADRs
// (ADR-0028). A team that wants auto-routing back can add project-level disposition overrides.
var secretRouting = []disposition.Disposition{
	{When: map[string]string{"verified": "true"}, Action: disposition.ActionFinding},
}

// BuiltIns returns the registry of first-party capabilities. Third-party capabilities load as
// extension packages (ADR-0003) using this same contract — but first-party tools ship in-tree as
// built-ins: the extension format's payoff (add/update without recompiling) is moot for tools that
// ship in the binary, and bundling one as an unsigned pack only made it fail to load by default.
func BuiltIns() *Registry {
	r := NewRegistry()
	r.Register(sourceInventory{})
	r.Register(semgrep{})
	r.Register(httpProbe{})
	r.Register(nmapScan{})
	r.Register(grypeScan{})
	r.Register(osvScanner{})
	r.Register(syftSBOM{})
	r.Register(govulncheck{})
	r.Register(routeMap{})
	r.Register(opengrepScan{})
	r.Register(trufflehog{})
	return r
}

// sourceInventory lists the files in a source tree. It is offline (no network) and uses a tiny
// image, making it the fast, dependency-free proof of the capability→sandbox→artifact loop.
type sourceInventory struct{}

func (sourceInventory) Manifest() Manifest {
	return Manifest{
		ID:              "source-inventory",
		AppliesTo:       []string{"source_repo"},
		Version:         "1.0.0",
		Title:           "Source inventory",
		Description:     "Lists the files in a source tree (offline recon).",
		OutputName:      "inventory.txt",
		OutputMediaType: "text/plain",
		OKExitCodes:     []int{0},
	}
}

func (sourceInventory) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("source-inventory: target directory required")
	}
	return runner.RunSpec{
		Image:    "alpine:3",
		Cmd:      []string{"sh", "-c", "cd /src && find . -type f | sort"},
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Network:  "none",
		Timeout:  2 * time.Minute,
		MemoryMB: 256,
		CPUs:     1,
	}, nil
}

// semgrep runs static analysis and emits SARIF. It needs network to fetch its ruleset, which the
// manifest declares by planning a networked RunSpec (an opt-in to the default-deny sandbox).
type semgrep struct{}

const semgrepImage = "semgrep/semgrep:1.104.0"

func (semgrep) Manifest() Manifest {
	return Manifest{
		ID:      "semgrep",
		Version: "1.0.0",
		// No AppliesTo: the scan orchestrator runs opengrep (the open fork with dataflow reachability) as the
		// default SAST; semgrep stays available for explicit runs (e.g. an existing Semgrep Pro license).
		Title:           "Semgrep (SAST)",
		Description:     "Static analysis over source with Semgrep; emits SARIF. Prefer opengrep (open, ships dataflow reachability); use this for an existing Semgrep license — set pro=true + a SEMGREP_APP_TOKEN secret for the Pro engine (interprocedural taint + codeFlows reachability).",
		OutputName:      "semgrep.sarif",
		OutputMediaType: "application/sarif+json",
		OKExitCodes:     []int{0, 1}, // 0 = clean, 1 = findings; >=2 is an error
	}
}

func (semgrep) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("semgrep: target directory required")
	}
	config := "auto"
	if c, ok := in.Params["config"].(string); ok && c != "" {
		config = c
	}
	// --dataflow-traces surfaces taint dataflow as SARIF codeFlows (reachability, ADR-0032) — but Semgrep CE
	// masks it; it only materializes under the Pro engine. Pro (interprocedural taint + codeFlows) needs a
	// license: pass pro=true AND a SEMGREP_APP_TOKEN secret ref (injected as env at exec, ADR-0011/0036).
	cmd := []string{"semgrep", "scan", "--sarif", "--dataflow-traces", "--quiet", "--config", config, "/src"}
	if pro, _ := in.Params["pro"].(bool); pro {
		cmd = append([]string{"semgrep", "scan", "--pro"}, cmd[2:]...)
	}
	return runner.RunSpec{
		Image:    semgrepImage,
		Cmd:      cmd,
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Workdir:  "/src",
		Network:  "bridge", // fetch rules; Pro also authenticates + pulls the pro engine
		Timeout:  10 * time.Minute,
		MemoryMB: 4096,
		CPUs:     2,
	}, nil
}

// opengrepScan is SAST with the open (LGPL-2.1) Semgrep fork. Unlike Semgrep CE, opengrep emits the SARIF
// `codeFlows` dataflow trace and metavariables (Semgrep moved these behind a commercial login), so SAST
// dataflow reachability (ADR-0032) works with a fully open tool. It is the default SAST engine (ADR-0036).
type opengrepScan struct{}

const opengrepImage = "osb/opengrep:latest" // OSB-built (images/opengrep); ships the pinned opengrep binary

func (opengrepScan) Manifest() Manifest {
	return Manifest{
		ID:              "opengrep",
		AppliesTo:       []string{"source_repo"},
		Version:         "1.0.0",
		Title:           "Opengrep (SAST + dataflow reachability)",
		Description:     "Static analysis with the open Semgrep fork; emits SARIF with dataflow traces (codeFlows) so taint findings carry reachability. Fetches registry rules over the network.",
		OutputName:      "opengrep.sarif",
		OutputMediaType: "application/sarif+json",
		OKExitCodes:     []int{0, 1}, // 0 = clean, 1 = findings; >=2 is an error
	}
}

func (opengrepScan) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("opengrep: target directory required")
	}
	config := "auto"
	if c, ok := in.Params["config"].(string); ok && c != "" {
		config = c
	}
	// --dataflow-traces is required for codeFlows (the reachability signal); --sarif writes SARIF to stdout.
	return runner.RunSpec{
		Image:    opengrepImage,
		Cmd:      []string{"opengrep", "scan", "--sarif", "--dataflow-traces", "--quiet", "--config", config, "/src"},
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Workdir:  "/src",
		Network:  "bridge", // fetch registry rulesets
		Timeout:  10 * time.Minute,
		MemoryMB: 4096,
		CPUs:     2,
	}, nil
}

// httpProbe fetches a URL's response headers. It touches the network against a caller-supplied
// target, so its manifest declares TargetParam and the engine enforces the scope allowlist first.
type httpProbe struct{}

const curlImage = "curlimages/curl:8.11.1"

func (httpProbe) Manifest() Manifest {
	return Manifest{
		ID:              "http-probe",
		Version:         "1.0.0",
		Title:           "HTTP probe",
		Description:     "Fetches a target URL's response headers (scope-guarded network capability).",
		OutputName:      "response-headers.txt",
		OutputMediaType: "text/plain",
		OKExitCodes:     []int{0},
		TargetParam:     "target",
		// Auto-run against web_service assets: the scan orchestrator sources the target from the asset's base
		// URL (ADR-0067). nmap stays opt-out — it needs a host, not a URL (host extraction is a follow-up).
		AppliesTo: []string{"web_service"},
	}
}

func (httpProbe) Plan(in Input) (runner.RunSpec, error) {
	target, _ := in.Params["target"].(string)
	if target == "" {
		return runner.RunSpec{}, errors.New("http-probe: a 'target' param (host or URL) is required")
	}
	return runner.RunSpec{
		Image:    curlImage,
		Cmd:      []string{"-sS", "-I", "--max-time", "20", target},
		Network:  "bridge",
		Timeout:  1 * time.Minute,
		MemoryMB: 256,
		CPUs:     1,
	}, nil
}

// nmapScan runs an nmap service scan against a target and emits XML (interpreted into open-port
// observations). It is a network capability — scope-guarded — and is runner-agnostic: on a
// project-bound remote runner it scans from that runner's network vantage (ADR-0004).
type nmapScan struct{}

func (nmapScan) Manifest() Manifest {
	return Manifest{
		ID:              "nmap",
		Version:         "1.0.0",
		Title:           "Nmap (service/topology scan)",
		Description:     "Service-version scan of a target host; emits nmap XML → open-port observations. Network capability (scope-guarded).",
		OutputName:      "nmap.xml",
		OutputMediaType: "application/x-nmap-xml", // interpret.NmapMediaType
		OKExitCodes:     []int{0},
		TargetParam:     "target",
		Technique:       "intrusive", // active service probing — gated by the engagement's rules of engagement (ADR-0051)
	}
}

func (nmapScan) Plan(in Input) (runner.RunSpec, error) {
	target, _ := in.Params["target"].(string)
	if target == "" {
		return runner.RunSpec{}, errors.New("nmap: a 'target' param (host/IP/CIDR) is required")
	}
	// The image's entrypoint is nmap, so Cmd carries args only.
	return runner.RunSpec{
		Image:    "instrumentisto/nmap:7.95",
		Cmd:      []string{"-sV", "-oX", "-", "--host-timeout", "90s", target},
		Network:  "bridge",
		Timeout:  10 * time.Minute,
		MemoryMB: 512,
		CPUs:     1,
	}, nil
}

// grypeScan is SCA: it scans a source tree for known-vulnerable dependencies and emits SARIF
// (interpreted into observations via the existing SARIF loop). Needs network to fetch its vuln DB.
type grypeScan struct{}

func (grypeScan) Manifest() Manifest {
	return Manifest{
		ID:              "grype",
		AppliesTo:       []string{"source_repo"},
		Version:         "1.0.0",
		Title:           "Grype (SCA / dependency vulnerabilities)",
		Description:     "Scans dependencies for known vulnerabilities; emits SARIF. Fetches its vulnerability DB over the network.",
		OutputName:      "grype.sarif",
		OutputMediaType: "application/sarif+json",
		OKExitCodes:     []int{0},
	}
}

func (grypeScan) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("grype: target directory required")
	}
	return runner.RunSpec{
		Image:    "anchore/grype:v0.80.2",
		Cmd:      []string{"dir:/src", "-o", "sarif", "-q"},
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Network:  "bridge", // vuln DB download
		Timeout:  10 * time.Minute,
		MemoryMB: 2048,
		CPUs:     2,
	}, nil
}

// osvScanner is SCA via Google's OSV database — broad multi-ecosystem coverage from lockfiles/SBOMs,
// complementing grype (different DB, different ecosystems). Emits SARIF, interpreted like any other SCA
// tool and enriched with the shared reachability verdict (ADR-0031). Needs network to query osv.dev.
type osvScanner struct{}

func (osvScanner) Manifest() Manifest {
	return Manifest{
		ID:              "osv-scanner",
		AppliesTo:       []string{"source_repo"},
		Version:         "1.0.0",
		Title:           "OSV-Scanner (SCA / dependency vulnerabilities)",
		Description:     "Scans dependency manifests/lockfiles against Google's OSV database across ecosystems; emits SARIF. Complements grype (broader ecosystem + advisory coverage). Queries osv.dev over the network.",
		OutputName:      "osv.sarif",
		OutputMediaType: "application/sarif+json",
		OKExitCodes:     []int{0, 1}, // 0 = no vulns, 1 = vulns found; >=2 is an error
	}
}

func (osvScanner) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("osv-scanner: target directory required")
	}
	return runner.RunSpec{
		Image:    "ghcr.io/google/osv-scanner:v1.9.2",
		Cmd:      []string{"--format", "sarif", "--recursive", "/src"},
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Network:  "bridge", // query the OSV database
		Timeout:  10 * time.Minute,
		MemoryMB: 2048,
		CPUs:     2,
	}, nil
}

// syftSBOM generates a CycloneDX SBOM of a source tree (offline). The dependency graph parses it.
type syftSBOM struct{}

func (syftSBOM) Manifest() Manifest {
	return Manifest{
		ID:              "syft",
		AppliesTo:       []string{"source_repo"},
		Version:         "1.0.0",
		Title:           "Syft (SBOM / dependency inventory)",
		Description:     "Builds a CycloneDX SBOM of a source tree (offline). Feeds the dependency graph.",
		OutputName:      "sbom.cdx.json",
		OutputMediaType: "application/vnd.cyclonedx+json",
		OKExitCodes:     []int{0},
	}
}

func (syftSBOM) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("syft: target directory required")
	}
	return runner.RunSpec{
		Image:    "anchore/syft:v1.20.0",
		Cmd:      []string{"dir:/src", "-o", "cyclonedx-json", "-q"},
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Network:  "none",
		Timeout:  10 * time.Minute,
		MemoryMB: 2048,
		CPUs:     2,
	}, nil
}

// govulncheck is reachability-aware SCA for Go: it builds the call graph and reports whether each known
// vulnerability's symbol is actually *called*, not merely imported (ADR-0030). Its observations carry a
// `reachable` attribute; combined with the engine's `exposed` enrichment, only reachable vulns on an
// exposed service escalate. Needs the Go toolchain + network (installs the analyzer, fetches the vuln DB
// and module graph).
type govulncheck struct{}

func (govulncheck) Manifest() Manifest {
	return Manifest{
		ID:              "govulncheck",
		AppliesTo:       []string{"source_repo"},
		Ecosystems:      []string{"go"},
		Version:         "1.0.0",
		Title:           "govulncheck (Go reachability SCA)",
		Description:     "Call-graph reachability analysis of Go dependency vulnerabilities; emits govulncheck JSON. Escalates only reachable vulns on an exposed service.",
		OutputName:      "govulncheck.json",
		OutputMediaType: "application/vnd.govulncheck+json", // interpret.GovulncheckMediaType
		OKExitCodes:     []int{0, 3},                        // 3 = vulnerabilities found
	}
}

func (govulncheck) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("govulncheck: target directory required")
	}
	// The golang image has no govulncheck; install it, then analyse /src. Module/build caches land in the
	// writable GOPATH/GOCACHE, so the /src mount stays read-only. The image must satisfy govulncheck@latest's
	// minimum Go version (v1.6.0 needs Go >= 1.25; GOTOOLCHAIN=local won't auto-upgrade), verified in Docker.
	return runner.RunSpec{
		Image: "golang:1.25",
		Cmd: []string{"sh", "-c",
			"go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck -C /src -json ./..."},
		Mounts: []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		// The container runs as a non-root user (runner hardening), so the Go toolchain's caches and the
		// installed analyzer must live under the writable tmpfs /tmp rather than the image's root-owned /go.
		Env: []string{
			"GOPATH=/tmp/go", "GOCACHE=/tmp/gocache", "GOMODCACHE=/tmp/gomod", "GOBIN=/tmp/gobin",
			"PATH=/tmp/gobin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
		},
		Network:  "bridge", // install analyzer + fetch vuln DB and modules
		Timeout:  15 * time.Minute,
		MemoryMB: 4096,
		CPUs:     2,
	}, nil
}

// routeMap extracts declared HTTP routes from source using a bundled semgrep ruleset (ADR-0033), feeding
// the exposed-route inventory. Offline — the ruleset is local, so no registry fetch. Its output is route
// JSON (not observations); the engine upserts it into the routes table.
type routeMap struct{}

//go:embed routes.yml
var routeRulesYML []byte

// routeRulesDir stages the embedded route ruleset to a stable temp dir and returns it for mounting. Written
// on each call (a few KB) so a deleted temp file self-heals.
func routeRulesDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "osb-route-rules")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "routes.yml"), routeRulesYML, 0o640); err != nil {
		return "", err
	}
	return dir, nil
}

func (routeMap) Manifest() Manifest {
	return Manifest{
		ID:              "route-map",
		AppliesTo:       []string{"source_repo"},
		Version:         "1.0.0",
		Title:           "Route map (HTTP entry points)",
		Description:     "Extracts declared HTTP routes from source with a bundled semgrep ruleset; feeds the exposed-route inventory. Offline.",
		OutputName:      "routes.json",
		OutputMediaType: "application/vnd.osb-routes+json", // interpret.RouteMediaType
		OKExitCodes:     []int{0, 1},                       // 1 = matches found
	}
}

func (routeMap) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("route-map: target directory required")
	}
	rulesDir, err := routeRulesDir()
	if err != nil {
		return runner.RunSpec{}, fmt.Errorf("route-map: stage ruleset: %w", err)
	}
	return runner.RunSpec{
		Image: opengrepImage, // the open Semgrep fork — same CLI, and it un-masks metavars (ADR-0036)
		Cmd:   []string{"opengrep", "scan", "--json", "--quiet", "--config", "/rules/routes.yml", "/src"},
		Mounts: []runner.Mount{
			{Source: in.TargetDir, Target: "/src", ReadOnly: true},
			{Source: rulesDir, Target: "/rules", ReadOnly: true},
		},
		Workdir:  "/src",
		Network:  "none", // bundled ruleset — no registry fetch
		Timeout:  10 * time.Minute,
		MemoryMB: 4096,
		CPUs:     2,
	}, nil
}

// trufflehog scans a source tree for secrets and emits NDJSON (interpreted into observations). Offline —
// filesystem mode reads the tree only; verification of found secrets against their providers is disabled by
// omitting --results/--only-verified so no network is touched (ADR-0028 routes on the `verified` flag the
// tool sets from its own offline heuristics). Was briefly shipped as a bundled extension pack, but an
// unsigned in-tree pack fails to load by default; a built-in is the right home for a first-party tool.
type trufflehog struct{}

func (trufflehog) Manifest() Manifest {
	return Manifest{
		ID:              "trufflehog",
		AppliesTo:       []string{"source_repo"},
		Version:         "1.0.0",
		Title:           "TruffleHog (secrets)",
		Description:     "Scans a source tree for secrets; emits NDJSON → secret observations. Offline (no network). Verified secrets auto-confirm as findings; unverified matches open investigations.",
		OutputName:      "trufflehog.json",
		OutputMediaType: "application/x-trufflehog-json", // interpret.TruffleHogMediaType
		OKExitCodes:     []int{0, 183},                   // 183 = secrets found (trufflehog's --fail convention)
		Dispositions:    secretRouting,
	}
}

func (trufflehog) Plan(in Input) (runner.RunSpec, error) {
	if in.TargetDir == "" {
		return runner.RunSpec{}, errors.New("trufflehog: target directory required")
	}
	// The image's entrypoint is trufflehog, so Cmd carries the subcommand + args. --no-update keeps it
	// offline (no self-update check); filesystem mode scans the read-only /src mount.
	return runner.RunSpec{
		Image:    "trufflesecurity/trufflehog:3.82.6",
		Cmd:      []string{"filesystem", "/src", "--json", "--no-update"},
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Network:  "none",
		Timeout:  10 * time.Minute,
		MemoryMB: 2048,
		CPUs:     2,
	}, nil
}
