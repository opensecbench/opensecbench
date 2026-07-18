package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
)

func writePackage(t *testing.T, dir string, m Manifest, sig string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if sig != "" {
		if err := os.WriteFile(filepath.Join(dir, SignatureFile), []byte(sig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func sampleManifest() Manifest {
	return Manifest{
		ID: "opensecbench.trufflehog", Name: "TruffleHog", Version: "1.0.0", Publisher: "acme",
		Capabilities: []ContainerCapability{{
			ID: "trufflehog", Version: "1.0.0", Title: "TruffleHog", Image: "trufflesecurity/trufflehog:3.63.0",
			Cmd: []string{"filesystem", "/src", "--json"}, MountSource: true,
			OutputName: "trufflehog.json", OutputMediaType: "application/json", OKExitCodes: []int{0, 183},
		}},
	}
}

func TestSignVerifyAndTrust(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "trufflehog")
	trustDir := filepath.Join(dir, "trusted_keys")

	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	m := sampleManifest()
	sig, err := Sign(m, priv)
	if err != nil {
		t.Fatal(err)
	}
	writePackage(t, pkgDir, m, sig)

	ts, err := LoadTrustStore(trustDir)
	if err != nil {
		t.Fatal(err)
	}

	// Untrusted publisher: refused (no override).
	if _, err := Load(pkgDir, ts, false); err == nil {
		t.Fatal("expected refusal for untrusted publisher")
	}
	// Allowed with override, but marked untrusted.
	l, err := Load(pkgDir, ts, true)
	if err != nil || l.Trusted {
		t.Fatalf("override load: err=%v trusted=%v (want trusted=false)", err, l.Trusted)
	}

	// Trust the publisher key → now trusted and loads without override.
	if err := ts.Trust("acme", pub); err != nil {
		t.Fatal(err)
	}
	l, err = Load(pkgDir, ts, false)
	if err != nil || !l.Trusted {
		t.Fatalf("trusted load: err=%v trusted=%v", err, l.Trusted)
	}
	if l.Digest == "" || len(l.CapabilityList()) != 1 {
		t.Fatalf("loaded package wrong: %+v", l)
	}
}

func TestTamperedManifestFailsVerification(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	pub, priv, _ := GenerateKeyPair()
	m := sampleManifest()
	sig, _ := Sign(m, priv)

	// Tamper: change the image after signing.
	m.Capabilities[0].Image = "evil/image:latest"
	writePackage(t, pkgDir, m, sig)

	ts, _ := LoadTrustStore(filepath.Join(dir, "keys"))
	_ = ts.Trust("acme", pub)
	if _, err := Load(pkgDir, ts, false); err == nil {
		t.Fatal("tampered manifest should fail signature verification")
	}
}

func TestContainerCapabilityPlan(t *testing.T) {
	c := ContainerCapability{
		ID: "nmap", Version: "1.0.0", Image: "instrumentisto/nmap:7.94",
		Cmd: []string{"-sV", "{{target}}"}, Network: "bridge", TargetParam: "target",
		OutputName: "scan.txt", OutputMediaType: "text/plain",
	}
	spec, err := c.Plan(capability.Input{Params: map[string]any{"target": "scanme.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Image != "instrumentisto/nmap:7.94" || spec.Network != "bridge" {
		t.Fatalf("spec wrong: %+v", spec)
	}
	if strings.Join(spec.Cmd, " ") != "-sV scanme.example" {
		t.Fatalf("cmd substitution wrong: %v", spec.Cmd)
	}

	// A source-mount capability requires a target dir and mounts it.
	src := ContainerCapability{ID: "x", Version: "1", Image: "alpine:3", Cmd: []string{"ls", "/src"}, MountSource: true}
	spec, err = src.Plan(capability.Input{TargetDir: "/work/repo"})
	if err != nil || len(spec.Mounts) != 1 || spec.Mounts[0].Source != "/work/repo" || !spec.Mounts[0].ReadOnly {
		t.Fatalf("source mount wrong: %+v (err=%v)", spec.Mounts, err)
	}
}
