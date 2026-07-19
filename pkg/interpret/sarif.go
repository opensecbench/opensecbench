// Package interpret converts tool output artifacts into observations (ADR-0005). Deterministic
// interpreters (like this SARIF parser) emit tool-origin observations; they never infer beyond
// what the tool reported.
package interpret

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// SARIFMediaType is the media type interpreted by SARIF.
const SARIFMediaType = "application/sarif+json"

type sarifLog struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID         string          `json:"id"`
	Properties sarifProperties `json:"properties"`
}

// sarifProperties carries the bag of extra facts both semgrep and grype emit; we only read the CVSS-like
// security-severity, present at either the result or the rule level depending on the tool.
type sarifProperties struct {
	SecuritySeverity string `json:"security-severity"`
}

type sarifResult struct {
	RuleID     string          `json:"ruleId"`
	Level      string          `json:"level"`
	Message    sarifMessage    `json:"message"`
	Locations  []sarifLocation `json:"locations"`
	Properties sarifProperties `json:"properties"`
	// CodeFlows carry a dataflow trace (source → sink) for taint-mode findings; their presence means the
	// tool proved the finding is reachable from an untrusted input (ADR-0032).
	CodeFlows []sarifCodeFlow `json:"codeFlows"`
}

type sarifCodeFlow struct {
	ThreadFlows []sarifThreadFlow `json:"threadFlows"`
}

type sarifThreadFlow struct {
	Locations []sarifThreadFlowLocation `json:"locations"`
}

type sarifThreadFlowLocation struct {
	Location sarifLocation `json:"location"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region struct {
			StartLine int `json:"startLine"`
		} `json:"region"`
	} `json:"physicalLocation"`
}

// SARIF parses SARIF bytes into tool observations (unreviewed). The caller sets task/artifact
// links and ids.
func SARIF(data []byte) ([]model.Observation, error) {
	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("interpret: parse SARIF: %w", err)
	}

	var obs []model.Observation
	for _, run := range log.Runs {
		tool := strings.ToLower(run.Tool.Driver.Name) // "semgrep" / "grype" — carried as an attribute
		// security-severity often lives on the rule definition (semgrep) rather than the result; index it.
		ruleSev := make(map[string]string)
		for _, rule := range run.Tool.Driver.Rules {
			if rule.Properties.SecuritySeverity != "" {
				ruleSev[rule.ID] = rule.Properties.SecuritySeverity
			}
		}
		for _, r := range run.Results {
			secSev := r.Properties.SecuritySeverity
			if secSev == "" {
				secSev = ruleSev[r.RuleID]
			}
			// Structured attributes so post-run disposition rules can route (ADR-0028/0029).
			attrs := map[string]string{}
			if tool != "" {
				attrs["tool"] = tool
			}
			if secSev != "" {
				attrs["security_severity"] = secSev
			}
			// A dataflow trace means a taint finding — the tool proved a source→sink path, i.e. the sink is
			// reachable from untrusted input (ADR-0032). Record it as reachable and note where input enters.
			if len(r.CodeFlows) > 0 {
				attrs["reachable"] = "true"
				if src := dataflowSource(r.CodeFlows); src != "" {
					attrs["dataflow_source"] = src
				}
			}
			// The SARIF level collapses distinct CVSS bands (grype maps both Critical and High to "error"),
			// so prefer the numeric security-severity when present — this is what makes MinSeverity routing
			// (e.g. critical vs high) meaningful for grype/semgrep.
			sev := severityFromLevel(r.Level)
			if refined := severityFromCVSS(secSev); refined != "" {
				sev = refined
			}
			o := model.Observation{
				Origin:      model.OriginTool,
				ReviewState: model.ReviewUnreviewed,
				Title:       title(r.RuleID, r.Message.Text),
				Detail:      r.Message.Text,
				Severity:    sev,
				RuleID:      r.RuleID,
				Location:    firstLocation(r.Locations),
			}
			if len(attrs) > 0 {
				o.Attributes = attrs
			}
			obs = append(obs, o)
		}
	}
	return obs, nil
}

// severityFromCVSS maps a SARIF security-severity (a CVSS-base-score string, "0.0".."10.0") to our severity
// vocabulary using the standard CVSS v3 bands. Returns "" when the value is absent or unparseable, so the
// caller can fall back to the SARIF level.
func severityFromCVSS(s string) string {
	if s == "" {
		return ""
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return ""
	}
	switch {
	case v >= 9.0:
		return "critical"
	case v >= 7.0:
		return "high"
	case v >= 4.0:
		return "medium"
	case v > 0:
		return "low"
	default:
		return "info"
	}
}

func title(ruleID, message string) string {
	line := message
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = ruleID
	}
	if len(line) > 160 {
		line = line[:157] + "…"
	}
	if line == "" {
		return "Untitled observation"
	}
	return line
}

func severityFromLevel(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "high"
	case "warning":
		return "medium"
	case "note":
		return "low"
	default:
		return "info"
	}
}

// dataflowSource returns the origin of a taint trace — the first thread-flow location, where untrusted
// input enters — as "file:line", or "" if the trace has no usable location.
func dataflowSource(flows []sarifCodeFlow) string {
	for _, cf := range flows {
		for _, tf := range cf.ThreadFlows {
			if len(tf.Locations) > 0 {
				return firstLocation([]sarifLocation{tf.Locations[0].Location})
			}
		}
	}
	return ""
}

func firstLocation(locs []sarifLocation) string {
	if len(locs) == 0 {
		return ""
	}
	phys := locs[0].PhysicalLocation
	if phys.ArtifactLocation.URI == "" {
		return ""
	}
	if phys.Region.StartLine > 0 {
		return phys.ArtifactLocation.URI + ":" + strconv.Itoa(phys.Region.StartLine)
	}
	return phys.ArtifactLocation.URI
}
