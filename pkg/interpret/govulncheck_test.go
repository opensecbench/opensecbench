package interpret

import (
	"testing"
)

// A stream with two vulns: one whose vulnerable symbol is called (symbol-level trace → reachable) and one
// that is only imported (package-level finding, no function → not reachable).
const govulnStream = `
{"osv": {"id": "GO-2022-0969", "aliases": ["CVE-2022-41723"], "summary": "Denial of service in net/http2",
  "affected": [{"package": {"name": "golang.org/x/net"}}]}}
{"osv": {"id": "GO-2021-0113", "aliases": ["CVE-2021-38561"], "summary": "Out of bounds read in golang.org/x/text",
  "affected": [{"package": {"name": "golang.org/x/text"}}]}}
{"finding": {"osv": "GO-2022-0969", "fixed_version": "v0.7.0", "trace": [
  {"module": "golang.org/x/net", "package": "golang.org/x/net/http2", "function": "readFrameHeader",
   "position": {"filename": "frame.go", "line": 237}},
  {"module": "example.com/app", "package": "example.com/app", "function": "main"}
]}}
{"finding": {"osv": "GO-2021-0113", "fixed_version": "v0.3.7", "trace": [
  {"module": "golang.org/x/text", "package": "golang.org/x/text/language"}
]}}
`

func TestGovulncheckReachability(t *testing.T) {
	obs, err := Govulncheck([]byte(govulnStream))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("got %d observations, want 2", len(obs))
	}

	byRule := map[string]int{}
	for i, o := range obs {
		byRule[o.RuleID] = i
	}
	// The called vuln is reachable, keyed by its CVE alias, located at the call site, with fix metadata.
	called, ok := byRule["CVE-2022-41723"]
	if !ok {
		t.Fatalf("expected an observation keyed by CVE-2022-41723, got %v", byRule)
	}
	c := obs[called]
	if c.Attributes["reachable"] != "true" {
		t.Fatalf("called vuln reachable = %q, want true", c.Attributes["reachable"])
	}
	if c.Attributes["tool"] != "govulncheck" || c.Attributes["osv"] != "GO-2022-0969" {
		t.Fatalf("attributes = %v", c.Attributes)
	}
	if c.Attributes["fixed_version"] != "v0.7.0" {
		t.Fatalf("fixed_version = %q", c.Attributes["fixed_version"])
	}
	if c.Location != "frame.go:237" {
		t.Fatalf("location = %q, want frame.go:237", c.Location)
	}

	// The imported-but-uncalled vuln is not reachable.
	imported := obs[byRule["CVE-2021-38561"]]
	if imported.Attributes["reachable"] != "false" {
		t.Fatalf("imported vuln reachable = %q, want false", imported.Attributes["reachable"])
	}
}

func TestGovulncheckEmpty(t *testing.T) {
	// A clean run emits only config/progress messages — no findings, no observations.
	obs, err := Govulncheck([]byte(`{"config": {"protocol_version": "v1.0.0"}}` + "\n" + `{"progress": {"message": "Scanning..."}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 0 {
		t.Fatalf("clean run should yield no observations, got %d", len(obs))
	}
}
