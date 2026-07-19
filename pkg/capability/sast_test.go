package capability

import (
	"strings"
	"testing"
)

func cmdString(args []string) string { return strings.Join(args, " ") }

func TestOpengrepCapability(t *testing.T) {
	m := opengrepScan{}.Manifest()
	if m.ID != "opengrep" || m.OutputMediaType != "application/sarif+json" {
		t.Fatalf("manifest = %+v", m)
	}
	spec, err := opengrepScan{}.Plan(Input{TargetDir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Image != opengrepImage {
		t.Fatalf("image = %q, want %q", spec.Image, opengrepImage)
	}
	cmd := cmdString(spec.Cmd)
	// --dataflow-traces is what makes taint findings carry reachability (codeFlows) — it must be present.
	if !strings.Contains(cmd, "--dataflow-traces") || !strings.Contains(cmd, "--sarif") {
		t.Fatalf("opengrep cmd missing dataflow/sarif flags: %s", cmd)
	}
	if spec.Network != "bridge" {
		t.Fatalf("opengrep needs bridge for registry rules, got %q", spec.Network)
	}
}

func TestRouteMapUsesOpengrep(t *testing.T) {
	spec, err := routeMap{}.Plan(Input{TargetDir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Image != opengrepImage {
		t.Fatalf("route-map should run on the opengrep image, got %q", spec.Image)
	}
	if !strings.Contains(cmdString(spec.Cmd), "opengrep") {
		t.Fatalf("route-map cmd should invoke opengrep: %s", cmdString(spec.Cmd))
	}
}

func TestSemgrepProFlag(t *testing.T) {
	// Default (no license): dataflow-traces requested, but no --pro.
	base, _ := semgrep{}.Plan(Input{TargetDir: "/repo"})
	if !strings.Contains(cmdString(base.Cmd), "--dataflow-traces") {
		t.Fatalf("semgrep should request dataflow-traces: %s", cmdString(base.Cmd))
	}
	if strings.Contains(cmdString(base.Cmd), "--pro") {
		t.Fatalf("no pro param → no --pro: %s", cmdString(base.Cmd))
	}
	// With a license (pro=true): the Pro engine is enabled for codeFlows/interprocedural taint.
	pro, _ := semgrep{}.Plan(Input{TargetDir: "/repo", Params: map[string]any{"pro": true}})
	if !strings.Contains(cmdString(pro.Cmd), "--pro") {
		t.Fatalf("pro=true should add --pro: %s", cmdString(pro.Cmd))
	}
}
