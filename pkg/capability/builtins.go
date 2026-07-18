package capability

import (
	"errors"
	"time"

	"github.com/opensecbench/opensecbench/pkg/runner"
)

// BuiltIns returns the registry of first-party capabilities. Third-party capabilities load as
// extension packages later (ADR-0003), using this same contract.
func BuiltIns() *Registry {
	r := NewRegistry()
	r.Register(sourceInventory{})
	r.Register(semgrep{})
	r.Register(httpProbe{})
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
