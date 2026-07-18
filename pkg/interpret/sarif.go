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
	Results []sarifResult `json:"results"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifMessage    `json:"message"`
	Locations []sarifLocation `json:"locations"`
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
		for _, r := range run.Results {
			obs = append(obs, model.Observation{
				Origin:      model.OriginTool,
				ReviewState: model.ReviewUnreviewed,
				Title:       title(r.RuleID, r.Message.Text),
				Detail:      r.Message.Text,
				Severity:    severityFromLevel(r.Level),
				RuleID:      r.RuleID,
				Location:    firstLocation(r.Locations),
			})
		}
	}
	return obs, nil
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
