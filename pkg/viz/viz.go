// Package viz renders aggregates into self-contained inline SVG figures (ADR-0008): no JavaScript,
// no external assets, so they embed directly in HTML/PDF reports and render anywhere.
package viz

import (
	"fmt"
	"html"
	"strings"
)

// severityOrder and colors mirror the report stylesheet.
var severityOrder = []string{"critical", "high", "medium", "low", "info"}
var severityColor = map[string]string{
	"critical": "#7c1d1d",
	"high":     "#dc2626",
	"medium":   "#f59e0b",
	"low":      "#3b82f6",
	"info":     "#6b7280",
}

// SeverityChart renders a horizontal bar chart of finding counts by severity as inline SVG.
func SeverityChart(bySeverity map[string]int) string {
	const (
		width    = 460
		rowH     = 26
		gap      = 8
		labelW   = 74
		countW   = 34
		barMaxW  = width - labelW - countW - 8
		padTop   = 8
		fontSize = 13
	)

	max := 0
	total := 0
	for _, s := range severityOrder {
		if c := bySeverity[s]; c > max {
			max = c
		}
		total += bySeverity[s]
	}
	height := padTop*2 + len(severityOrder)*rowH + (len(severityOrder)-1)*gap

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="Findings by severity" font-family="sans-serif" font-size="%d">`,
		width, height, width, height, fontSize)

	if total == 0 {
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#6b7280">No findings</text></svg>`, labelW, padTop+rowH/2+4)
		return b.String()
	}

	y := padTop
	for _, sev := range severityOrder {
		count := bySeverity[sev]
		barW := 2
		if max > 0 {
			barW = 2 + count*barMaxW/max
		}
		color := severityColor[sev]
		// label
		fmt.Fprintf(&b, `<text x="0" y="%d" fill="#374151" font-weight="600">%s</text>`,
			y+rowH/2+4, html.EscapeString(strings.ToUpper(sev)))
		// bar
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="3" fill="%s" opacity="%s"/>`,
			labelW, y+3, barW, rowH-6, color, barOpacity(count))
		// count
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#374151">%d</text>`,
			labelW+barW+6, y+rowH/2+4, count)
		y += rowH + gap
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func barOpacity(count int) string {
	if count == 0 {
		return "0.25"
	}
	return "1"
}
