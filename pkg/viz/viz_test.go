package viz

import (
	"strings"
	"testing"
)

func TestSeverityChartRendersBars(t *testing.T) {
	svg := SeverityChart(map[string]int{"critical": 1, "high": 3, "medium": 0})
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not a self-contained svg: %.40s", svg)
	}
	// No script or external asset references (embeddable + CSP-safe). The xmlns namespace URI is
	// an identifier, not a fetch, so it is allowed.
	for _, bad := range []string{"<script", "xlink:href", "<image", "<use", "url("} {
		if strings.Contains(svg, bad) {
			t.Fatalf("svg contains %q", bad)
		}
	}
	// Labels and counts present.
	for _, want := range []string{"CRITICAL", "HIGH", "MEDIUM", "#dc2626"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("svg missing %q", want)
		}
	}
}

func TestSeverityChartEmpty(t *testing.T) {
	svg := SeverityChart(nil)
	if !strings.Contains(svg, "No findings") {
		t.Fatalf("empty chart should say 'No findings': %s", svg)
	}
}
