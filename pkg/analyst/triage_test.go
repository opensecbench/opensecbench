package analyst

import (
	"strings"
	"testing"
)

func TestTriageSignals(t *testing.T) {
	got := triageSignals(map[string]string{
		"reachable_confirmed": "true",
		"exposed":             "true",
		"reachable":           "false",
		"dependency":          "transitive",
		"fixed_version":       "2.13.4",
		"package":             "org.example:x",
	})
	for _, want := range []string{"reachable_confirmed", "exposed", "reachable=false", "transitive", "fix_available"} {
		if !strings.Contains(got, want) {
			t.Errorf("signals %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "org.example") {
		t.Errorf("package is not a triage signal: %q", got)
	}
	if triageSignals(nil) != "" {
		t.Error("nil attrs should yield no signals")
	}
}
