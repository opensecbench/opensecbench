package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateTargetDir(t *testing.T) {
	scan := t.TempDir() // a real, benign directory

	// Empty (no mount), a real directory, and a not-yet-present path all pass (Docker auto-creates missing
	// mount sources; existence is not required, only that the path isn't a sensitive system location).
	for _, ok := range []string{"", scan, filepath.Join(scan, "not-here"), "/home/user/repo"} {
		if err := validateTargetDir(ok, true); err != nil {
			t.Errorf("validateTargetDir(%q) = %v, want nil", ok, err)
		}
	}

	// Sensitive system paths are rejected (lexical check, no fs access needed).
	for _, p := range []string{"/", "/etc", "/etc/ssh", "/root", "/proc/self", "/var/run/docker.sock", "/sys"} {
		if err := validateTargetDir(p, false); err == nil {
			t.Errorf("expected %q to be rejected", p)
		}
	}

	// A symlink into a sensitive path is canonicalized and rejected on a local run.
	link := filepath.Join(scan, "sneaky")
	if err := os.Symlink("/etc", link); err == nil {
		if err := validateTargetDir(link, true); err == nil {
			t.Error("symlink to /etc should be rejected after canonicalization")
		}
	}
}
