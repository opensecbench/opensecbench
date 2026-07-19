package interpret

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// TruffleHogMediaType is the media type interpreted by TruffleHog (`trufflehog … --json`, which emits
// newline-delimited JSON — one object per detected secret).
const TruffleHogMediaType = "application/x-trufflehog-json"

type truffleHogFinding struct {
	DetectorName   string `json:"DetectorName"`
	Verified       bool   `json:"Verified"`
	Redacted       string `json:"Redacted"`
	SourceMetadata struct {
		Data struct {
			Filesystem struct {
				File string `json:"file"`
				Line int    `json:"line"`
			} `json:"Filesystem"`
		} `json:"Data"`
	} `json:"SourceMetadata"`
}

// TruffleHog parses trufflehog's NDJSON output into tool observations — one per detected secret.
// Verified secrets are high severity; unverified ones are medium. Non-JSON log lines are skipped.
func TruffleHog(data []byte) ([]model.Observation, error) {
	var obs []model.Observation
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var f truffleHogFinding
		if err := json.Unmarshal(line, &f); err != nil || f.DetectorName == "" {
			continue
		}
		sev, state := "medium", "unverified"
		if f.Verified {
			sev, state = "high", "verified"
		}
		loc := f.SourceMetadata.Data.Filesystem.File
		if loc != "" && f.SourceMetadata.Data.Filesystem.Line > 0 {
			loc += ":" + strconv.Itoa(f.SourceMetadata.Data.Filesystem.Line)
		}
		detail := fmt.Sprintf("%s secret detected (%s)", f.DetectorName, state)
		if f.Redacted != "" {
			detail += ": " + f.Redacted
		}
		obs = append(obs, model.Observation{
			Origin:      model.OriginTool,
			ReviewState: model.ReviewUnreviewed,
			Title:       fmt.Sprintf("%s secret (%s)", f.DetectorName, state),
			Detail:      detail,
			Severity:    sev,
			RuleID:      "trufflehog:" + f.DetectorName,
			Location:    loc,
		})
	}
	if err := sc.Err(); err != nil {
		return obs, fmt.Errorf("interpret: read trufflehog output: %w", err)
	}
	return obs, nil
}
