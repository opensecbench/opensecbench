package capability

import (
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestTrufflehogRegisteredAsBuiltIn(t *testing.T) {
	// Regression: trufflehog was briefly a bundled extension that never loaded by default. It must ship
	// as a built-in so it is always in the registry.
	if _, ok := BuiltIns().Get("trufflehog"); !ok {
		t.Fatal("trufflehog is not registered as a built-in capability")
	}
}

func TestTrufflehogIsOffline(t *testing.T) {
	m := trufflehog{}.Manifest()
	if m.ID != "trufflehog" || m.OutputMediaType != "application/x-trufflehog-json" {
		t.Fatalf("manifest = %+v", m)
	}
	spec, err := trufflehog{}.Plan(Input{TargetDir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	// Secret scanning runs on a read-only mount and must not touch the network (no live verification).
	if spec.Network != "none" {
		t.Fatalf("trufflehog must be offline, got network %q", spec.Network)
	}
	if !strings.Contains(cmdString(spec.Cmd), "--no-update") {
		t.Fatalf("trufflehog cmd should stay offline with --no-update: %s", cmdString(spec.Cmd))
	}
	if len(spec.Mounts) != 1 || !spec.Mounts[0].ReadOnly {
		t.Fatalf("trufflehog should mount the source read-only, got %+v", spec.Mounts)
	}
}

func TestTrufflehogRouting(t *testing.T) {
	rules := trufflehog{}.Manifest().Dispositions
	// A verified secret was live-checked against its provider → auto-confirm as a finding.
	if a := disposition.Evaluate(secretObs(true), rules); a != disposition.ActionFinding {
		t.Fatalf("verified secret should be a finding, got %q", a)
	}
	// An unverified match → open an investigation for a human/agent to validate.
	if a := disposition.Evaluate(secretObs(false), rules); a != disposition.ActionInvestigate {
		t.Fatalf("unverified secret should investigate, got %q", a)
	}
}

func secretObs(verified bool) model.Observation {
	v := "false"
	if verified {
		v = "true"
	}
	return model.Observation{Severity: "high", Attributes: map[string]string{"verified": v}}
}
