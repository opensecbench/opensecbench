package capability

import "testing"

func TestTargetsEcosystems(t *testing.T) {
	rust := map[string]bool{"rust": true}
	pyAndNode := map[string]bool{"python": true, "node": true}

	cases := []struct {
		name       string
		ecosystems []string
		detected   map[string]bool
		want       bool
	}{
		{"agnostic runs anywhere", nil, rust, true},
		{"python tool skips a rust repo", []string{"python"}, rust, false},
		{"python tool runs on a python repo", []string{"python"}, pyAndNode, true},
		{"rust tool runs on a rust repo", []string{"rust"}, rust, true},
		{"multi-eco tool runs if any present", []string{"go", "node"}, pyAndNode, true},
		{"language tool skips an unknown repo", []string{"go"}, map[string]bool{}, false},
	}
	for _, c := range cases {
		m := Manifest{Ecosystems: c.ecosystems}
		if got := m.TargetsEcosystems(c.detected); got != c.want {
			t.Errorf("%s: TargetsEcosystems(%v, detected=%v) = %v, want %v", c.name, c.ecosystems, c.detected, got, c.want)
		}
	}
}
