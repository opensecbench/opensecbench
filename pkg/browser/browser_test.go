package browser

import (
	"os/exec"
	"testing"
)

func TestResolveOverride(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not in PATH")
	}
	got, err := Resolve("sh")
	if err != nil || got == "" {
		t.Fatalf("Resolve(\"sh\") = %q, %v", got, err)
	}
}

func TestResolveOverrideMissing(t *testing.T) {
	if _, err := Resolve("definitely-not-a-real-browser-xyz"); err == nil {
		t.Fatal("expected error for a missing override")
	}
}

func TestResolveEnv(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not in PATH")
	}
	t.Setenv("OSB_BROWSER", "sh")
	got, err := Resolve("")
	if err != nil || got == "" {
		t.Fatalf("Resolve via OSB_BROWSER = %q, %v", got, err)
	}
}
