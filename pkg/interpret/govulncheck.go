package interpret

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// GovulncheckMediaType is the media type interpreted by Govulncheck.
const GovulncheckMediaType = "application/vnd.govulncheck+json"

// govulncheck emits a stream of JSON messages, each with exactly one of these keys. We read the vuln
// definitions (osv) and the call-graph findings (finding); config/progress are ignored.
type govulnMessage struct {
	OSV     *govulnOSV     `json:"osv"`
	Finding *govulnFinding `json:"finding"`
}

type govulnOSV struct {
	ID       string   `json:"id"`      // Go advisory id, e.g. GO-2022-0969
	Aliases  []string `json:"aliases"` // includes the CVE, if any
	Summary  string   `json:"summary"`
	Affected []struct {
		Package struct {
			Name string `json:"name"`
		} `json:"package"`
	} `json:"affected"`
}

type govulnFinding struct {
	OSV          string        `json:"osv"`
	FixedVersion string        `json:"fixed_version"`
	Trace        []govulnFrame `json:"trace"`
}

type govulnFrame struct {
	Module   string `json:"module"`
	Package  string `json:"package"`
	Function string `json:"function"`
	Position *struct {
		Filename string `json:"filename"`
		Line     int    `json:"line"`
	} `json:"position"`
}

// govulnAgg accumulates the messages for one vulnerability: its definition plus whether any finding proved
// it reachable in the call graph.
type govulnAgg struct {
	osv        *govulnOSV
	hasFinding bool // govulncheck emits an osv definition for every vuln in the import graph; only ones
	reachable  bool // with a finding actually affect this module — the rest are DB noise, never recorded.
	pkg        string
	location   string
	fixed      string
}

// Govulncheck parses govulncheck's JSON stream into one observation per vulnerability (ADR-0030). The key
// signal is reachability: govulncheck builds the call graph, so a finding whose trace reaches a *symbol*
// (the vulnerable function is actually called) marks the vuln reachable=true; a vuln that is only
// imported/required (module- or package-level finding, no function) is reachable=false. Disposition rules
// then escalate only reachable vulns on an exposed service.
func Govulncheck(data []byte) ([]model.Observation, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	byOSV := map[string]*govulnAgg{}
	var order []string
	get := func(id string) *govulnAgg {
		a, ok := byOSV[id]
		if !ok {
			a = &govulnAgg{}
			byOSV[id] = a
			order = append(order, id)
		}
		return a
	}

	for {
		var msg govulnMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			// A malformed stream still yields whatever we decoded before the error (best-effort, like SARIF).
			return govulnObservations(order, byOSV), nil
		}
		switch {
		case msg.OSV != nil && msg.OSV.ID != "":
			get(msg.OSV.ID).osv = msg.OSV
		case msg.Finding != nil && msg.Finding.OSV != "":
			a := get(msg.Finding.OSV)
			a.hasFinding = true
			if msg.Finding.FixedVersion != "" {
				a.fixed = msg.Finding.FixedVersion
			}
			if len(msg.Finding.Trace) > 0 {
				vuln := msg.Finding.Trace[0] // the vulnerable frame
				if a.pkg == "" {
					a.pkg = firstNonEmpty(vuln.Module, vuln.Package)
				}
				// A symbol-level finding (the vulnerable function is named) means it is in the call graph.
				if vuln.Function != "" {
					a.reachable = true
					if a.location == "" && vuln.Position != nil && vuln.Position.Filename != "" {
						a.location = vuln.Position.Filename + ":" + strconv.Itoa(vuln.Position.Line)
					}
				}
			}
		}
	}
	return govulnObservations(order, byOSV), nil
}

func govulnObservations(order []string, byOSV map[string]*govulnAgg) []model.Observation {
	var obs []model.Observation
	for _, id := range order {
		a := byOSV[id]
		if !a.hasFinding {
			continue // an osv definition with no finding is a DB entry that doesn't affect this module
		}
		ruleID, summary, pkg := id, "", a.pkg
		if a.osv != nil {
			if cve := firstCVE(a.osv.Aliases); cve != "" {
				ruleID = cve
			}
			summary = a.osv.Summary
			if pkg == "" && len(a.osv.Affected) > 0 {
				pkg = a.osv.Affected[0].Package.Name
			}
		}
		attrs := map[string]string{
			"tool":      "govulncheck",
			"reachable": strconv.FormatBool(a.reachable),
			"osv":       id,
		}
		// All advisory ids (CVE + GHSA + GO id) so the reachability verdict can be correlated to another
		// tool that keys by a different scheme — grype reports GHSA, not CVE (ADR-0031).
		if a.osv != nil && len(a.osv.Aliases) > 0 {
			attrs["aliases"] = strings.Join(append([]string{id}, a.osv.Aliases...), ",")
		}
		if pkg != "" {
			attrs["package"] = pkg
		}
		if a.fixed != "" {
			attrs["fixed_version"] = a.fixed
		}
		location := a.location
		if location == "" {
			location = pkg
		}
		obs = append(obs, model.Observation{
			Origin:      model.OriginTool,
			ReviewState: model.ReviewUnreviewed,
			Title:       govulnTitle(summary, pkg, ruleID),
			Detail:      govulnDetail(summary, pkg, a.fixed),
			Severity:    "high", // known vuln; reachability/exposure (not severity) drive routing here
			RuleID:      ruleID,
			Location:    location,
			Attributes:  attrs,
		})
	}
	return obs
}

func govulnTitle(summary, pkg, ruleID string) string {
	if summary != "" {
		return title(ruleID, summary)
	}
	if pkg != "" {
		return ruleID + " in " + pkg
	}
	return ruleID
}

func govulnDetail(summary, pkg, fixed string) string {
	parts := []string{}
	if summary != "" {
		parts = append(parts, summary)
	}
	if pkg != "" {
		parts = append(parts, "Package: "+pkg)
	}
	if fixed != "" {
		parts = append(parts, "Fixed in: "+fixed)
	}
	return strings.Join(parts, "\n")
}

func firstCVE(aliases []string) string {
	for _, a := range aliases {
		if strings.HasPrefix(a, "CVE-") {
			return a
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
