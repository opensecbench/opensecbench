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

// reachableExposed escalates only a vulnerability that govulncheck proved reachable in the call graph AND
// that sits on a network-exposed service (ADR-0030). Everything else — imported-but-uncalled, or reachable
// in an internal-only service — falls to manual review, keeping triage focused on real exposure. The
// `reachable` attribute comes from govulncheck; `exposed` is enriched by the engine from the project's
// derived exposure.
var reachableExposed = []disposition.Disposition{
	{When: map[string]string{"reachable": "true", "exposed": "true"}, Action: disposition.ActionInvestigate},
}

// sastReachabilityRouting routes semgrep with dataflow reachability (ADR-0032) and route awareness
// (ADR-0034). Order matters — first match wins:
//  1. a finding in a TRAFFIC-CONFIRMED exposed route's handler (route_observed) escalates at medium+ — being
//     directly on a live endpoint is strong exposure evidence even without a dataflow trace;
//  2. a taint finding (reachable) on an exposed service escalates at any severity;
//  3. a high/critical pattern finding still investigates on severity.
// There is no downgrade on the ABSENCE of a route or a dataflow trace — route detection is heuristic and
// incomplete, so a missing route must never hide a finding.
var sastReachabilityRouting = []disposition.Disposition{
	{When: map[string]string{"route_observed": "true"}, MinSeverity: "medium", Action: disposition.ActionInvestigate},
	{When: map[string]string{"reachable": "true", "exposed": "true"}, Action: disposition.ActionInvestigate},
	{MinSeverity: "high", Action: disposition.ActionInvestigate},
}

// scaReachabilityRouting routes a general SCA tool (grype) whose CVE findings may be enriched with a shared
// reachability verdict (ADR-0031) and route association (ADR-0034). Order matters — first match wins:
//  1. a CVE govulncheck proved uncalled is downgraded to review even if the tool rates it high (authoritative);
//  2. a finding in a traffic-confirmed exposed route's handler escalates at medium+;
//  3. a reachable CVE on an exposed service escalates;
//  4. anything else high/critical (e.g. a non-Go CVE with no reachability verdict) still investigates.
// Rule 1 precedes the route escalation: if the vulnerable symbol is proven uncalled, sitting in a live
// handler's file doesn't make it exploitable.
var scaReachabilityRouting = []disposition.Disposition{
	{When: map[string]string{"reachable": "false"}, Action: disposition.ActionReview},
	{When: map[string]string{"route_observed": "true"}, MinSeverity: "medium", Action: disposition.ActionInvestigate},
	{When: map[string]string{"reachable": "true", "exposed": "true"}, Action: disposition.ActionInvestigate},
	{MinSeverity: "high", Action: disposition.ActionInvestigate},
}

// BuiltIns returns the registry of first-party capabilities. Third-party capabilities load as
// extension packages later (ADR-0003), using this same contract.
func BuiltIns() *Registry {
	r := NewRegistry()
	r.Register(sourceInventory{})
	r.Register(semgrep{})
	r.Register(httpProbe{})
	r.Register(nmapScan{})
	r.Register(grypeScan{})
	r.Register(syftSBOM{})
	r.Register(govulncheck{})
	r.Register(routeMap{})
	return r
}

// sourceInventory lists the files in a source tree. It is offline (no network) and uses a tiny
// image, making it the fast, dependency-free proof of the capability→sandbox→artifact loop.
type sourceInventory struct{}

func (sourceInventory) Manifest() Manifest {
	return Manifest{
		ID:              "source-inventory",
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
		ID:              "semgrep",
		Version:         "1.0.0",
		Title:           "Semgrep (SAST)",
		Description:     "Static analysis over source with Semgrep; emits SARIF. Fetches rules over the network.",
		OutputName:      "semgrep.sarif",
		OutputMediaType: "application/sarif+json",
		OKExitCodes:     []int{0, 1}, // 0 = clean, 1 = findings; >=2 is an error
		Dispositions:    sastReachabilityRouting,
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
	return runner.RunSpec{
		Image:    semgrepImage,
		Cmd:      []string{"semgrep", "scan", "--sarif", "--quiet", "--config", config, "/src"},
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
		Workdir:  "/src",
		Network:  "bridge", // required to fetch rules
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
		Version:         "1.0.0",
		Title:           "Grype (SCA / dependency vulnerabilities)",
		Description:     "Scans dependencies for known vulnerabilities; emits SARIF. Fetches its vulnerability DB over the network.",
		OutputName:      "grype.sarif",
		OutputMediaType: "application/sarif+json",
		OKExitCodes:     []int{0},
		Dispositions:    scaReachabilityRouting,
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

// syftSBOM generates a CycloneDX SBOM of a source tree (offline). The dependency graph parses it.
type syftSBOM struct{}

func (syftSBOM) Manifest() Manifest {
	return Manifest{
		ID:              "syft",
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
		Version:         "1.0.0",
		Title:           "govulncheck (Go reachability SCA)",
		Description:     "Call-graph reachability analysis of Go dependency vulnerabilities; emits govulncheck JSON. Escalates only reachable vulns on an exposed service.",
		OutputName:      "govulncheck.json",
		OutputMediaType: "application/vnd.govulncheck+json", // interpret.GovulncheckMediaType
		OKExitCodes:     []int{0, 3},                        // 3 = vulnerabilities found
		Dispositions:    reachableExposed,
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
		Mounts:   []runner.Mount{{Source: in.TargetDir, Target: "/src", ReadOnly: true}},
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
		Image: semgrepImage,
		Cmd:   []string{"semgrep", "scan", "--json", "--quiet", "--config", "/rules/routes.yml", "/src"},
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
