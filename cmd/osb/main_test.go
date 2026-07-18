package main

import (
	"os/exec"
	"testing"
)

func TestFindBrowserOverride(t *testing.T) {
	// A resolvable binary is returned; "sh" exists on any unix test host.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not in PATH")
	}
	got, err := findBrowser("sh")
	if err != nil || got == "" {
		t.Fatalf("findBrowser(\"sh\") = %q, %v", got, err)
	}
}

func TestFindBrowserOverrideMissing(t *testing.T) {
	if _, err := findBrowser("definitely-not-a-real-browser-xyz"); err == nil {
		t.Fatal("expected error for a missing browser override")
	}
}

func TestFindBrowserEnv(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not in PATH")
	}
	t.Setenv("OSB_BROWSER", "sh")
	got, err := findBrowser("")
	if err != nil || got == "" {
		t.Fatalf("findBrowser via OSB_BROWSER = %q, %v", got, err)
	}
}
