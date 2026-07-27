// Package report builds engagement deliverables from project data (ADR-0008). A Builder gathers a
// Data snapshot — enforcing the "confirmed findings with traceable evidence only" rule in one place
// — and templates render it to Markdown/HTML (PDF via a headless browser layers on top).
package report

import (
	"context"
	htmltemplate "html/template"
	"sort"
	"time"

	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/viz"
)

// Format is an output format.
type Format string

const (
	FormatMarkdown Format = "md"
	FormatHTML     Format = "html"
)

// Source is the read surface the Builder needs (satisfied by *store.DB).
type Source interface {
	GetProject(ctx context.Context, id string) (model.Project, error)
	ListApplicationsByProject(ctx context.Context, projectID string) ([]model.Application, error)
	ListAssetsByApplication(ctx context.Context, applicationID string) ([]model.Asset, error)
	ListFindings(ctx context.Context) ([]model.Finding, error)
	GetFinding(ctx context.Context, id string) (model.Finding, error)
	GetObservation(ctx context.Context, id string) (model.Observation, error)
	ListScopeEntries(ctx context.Context, projectID string) ([]model.ScopeEntry, error)
	ListTasks(ctx context.Context, limit int) ([]model.Task, error)
	ListAdoptedMethodologies(ctx context.Context, projectID string) ([]string, error)
	ListCoverage(ctx context.Context, projectID string) ([]model.CoverageEntry, error)
}

// Data is the immutable snapshot a report renders from.
type Data struct {
	Project       model.Project
	GeneratedAt   time.Time
	Scope         []model.ScopeEntry
	Summary       Summary
	Findings      []Finding
	Methodology   methodology.View  // adopted checklist coverage + roll-up
	SeverityChart htmltemplate.HTML // self-contained inline SVG figure (HTML reports)
	CoverageChart htmltemplate.HTML // severity × status heatmap (inline SVG)
	Brand         Brand             // optional client branding (branded template)
	// ExecutiveSummary is agent-authored narrative (ADR-0045): prose summarizing the engagement's outcome,
	// grounded in the reportable findings. Empty when narration is off/unavailable — templates degrade to
	// the data-only overview.
	ExecutiveSummary string
	Narrated         bool // true when narrative was authored for this snapshot (drives a "AI-drafted" note)
}

// Narrative is the agent-authored prose for a report: an executive summary plus per-finding impact and
// remediation keyed by finding id. It is produced from a Data snapshot by a Narrator and merged back in,
// so it is always grounded in the exact reportable finding set (ADR-0045).
type Narrative struct {
	ExecutiveSummary string                      `json:"executive_summary"`
	Findings         map[string]FindingNarrative `json:"-"`
}

// FindingNarrative is the authored prose for one finding.
type FindingNarrative struct {
	ID          string `json:"id"`
	Impact      string `json:"impact"`
	Remediation string `json:"remediation"`
}

// Narrator authors narrative from a grounded report snapshot for a given audience ("executive" | "technical").
// Implemented by the analyst service (which has the LLM provider); kept as an interface here so pkg/report has
// no dependency on the agent runtime.
type Narrator interface {
	Narrate(ctx context.Context, d Data, audience string) (Narrative, error)
}

// Report audiences — drive the narrator's tone.
const (
	AudienceExecutive = "executive"
	AudienceTechnical = "technical"
)

// AudienceFor maps a report template id to the narrative audience: executive/branded reports read as business
// risk; technical/compliance/retest read as precise engineering detail.
func AudienceFor(templateID string) string {
	switch templateID {
	case "executive", "branded":
		return AudienceExecutive
	default:
		return AudienceTechnical
	}
}

// ApplyNarrative merges authored prose into the snapshot: the executive summary and each finding's impact/
// remediation (matched by id). Unknown finding ids are ignored.
func (d *Data) ApplyNarrative(n Narrative) {
	d.ExecutiveSummary = n.ExecutiveSummary
	d.Narrated = true
	for i := range d.Findings {
		if fn, ok := n.Findings[d.Findings[i].ID]; ok {
			d.Findings[i].Impact = fn.Impact
			d.Findings[i].Remediation = fn.Remediation
		}
	}
}

// Brand is optional client branding for the branded report template.
type Brand struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
	Color   string `json:"color"` // hex accent, e.g. #0b5
}

// Summary is the coverage + severity roll-up.
type Summary struct {
	Applications int
	Assets       int
	TasksRun     int
	Capabilities []string
	Total        int            // reportable findings
	BySeverity   map[string]int // severity -> count
}

// Finding is a reportable finding expanded with its evidence and application name. Impact and Remediation are
// agent-authored narrative (ADR-0045), empty unless the report was narrated.
type Finding struct {
	model.Finding
	AppName     string
	Evidence    []model.Observation
	Impact      string
	Remediation string
}

// CWEGroup is a set of findings sharing a CWE, for the compliance/mapping report.
type CWEGroup struct {
	CWE      string
	Findings []Finding
}

// CWEGroups partitions findings by CWE, most-severe group first; unmapped findings group last.
func CWEGroups(findings []Finding) []CWEGroup {
	byCWE := map[string][]Finding{}
	var order []string
	for _, f := range findings {
		key := f.CWE
		if key == "" {
			key = "Unmapped"
		}
		if _, seen := byCWE[key]; !seen {
			order = append(order, key)
		}
		byCWE[key] = append(byCWE[key], f)
	}
	// Order groups by their most-severe finding (findings are already severity-sorted), Unmapped last.
	sort.SliceStable(order, func(i, j int) bool {
		if order[i] == "Unmapped" {
			return false
		}
		if order[j] == "Unmapped" {
			return true
		}
		return rankOf(byCWE[order[i]][0].Severity) < rankOf(byCWE[order[j]][0].Severity)
	})
	out := make([]CWEGroup, 0, len(order))
	for _, k := range order {
		out = append(out, CWEGroup{CWE: k, Findings: byCWE[k]})
	}
	return out
}

// severityRank orders severities most-severe first; unknown severities sort last.
var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

func rankOf(sev string) int {
	if r, ok := severityRank[sev]; ok {
		return r
	}
	return 99
}

// Builder assembles report Data from a Source.
type Builder struct {
	src     Source
	methods *methodology.Registry // live catalog (built-ins + user/extension packs); nil ⇒ built-ins only
}

// NewBuilder returns a Builder over src.
func NewBuilder(src Source) *Builder { return &Builder{src: src} }

// WithMethodology sets the live methodology registry the report reads coverage against (ADR-0056), so
// coverage on user-authored or extension packs appears in reports instead of being silently dropped. Passing
// nil (or never calling this) falls back to the built-in packs. Returns the builder for chaining.
func (b *Builder) WithMethodology(r *methodology.Registry) *Builder {
	b.methods = r
	return b
}

// Build gathers the report snapshot for a project. Only findings that are not false positives and
// carry at least one supporting observation (traceable evidence) are included (ADR-0005, ADR-0008).
func (b *Builder) Build(ctx context.Context, projectID string, now time.Time) (Data, error) {
	proj, err := b.src.GetProject(ctx, projectID)
	if err != nil {
		return Data{}, err
	}
	apps, err := b.src.ListApplicationsByProject(ctx, projectID)
	if err != nil {
		return Data{}, err
	}
	appName := make(map[string]string, len(apps))
	assetCount := 0
	for _, a := range apps {
		appName[a.ID] = a.Name
		assets, err := b.src.ListAssetsByApplication(ctx, a.ID)
		if err != nil {
			return Data{}, err
		}
		assetCount += len(assets)
	}

	scope, err := b.src.ListScopeEntries(ctx, projectID)
	if err != nil {
		return Data{}, err
	}

	all, err := b.src.ListFindings(ctx)
	if err != nil {
		return Data{}, err
	}
	bySeverity := map[string]int{}
	var findings []Finding
	for _, f := range all {
		if f.ApplicationID == nil || appName[*f.ApplicationID] == "" {
			continue // not in this project
		}
		if f.Status == model.FindingFalsePositive {
			continue
		}
		full, err := b.src.GetFinding(ctx, f.ID)
		if err != nil {
			return Data{}, err
		}
		evidence := make([]model.Observation, 0, len(full.ObservationIDs))
		for _, oid := range full.ObservationIDs {
			o, err := b.src.GetObservation(ctx, oid)
			if err != nil {
				continue // a missing observation should not sink the report
			}
			evidence = append(evidence, o)
		}
		if len(evidence) == 0 {
			continue // no traceable evidence -> not reportable
		}
		bySeverity[full.Severity]++
		findings = append(findings, Finding{Finding: full, AppName: appName[*full.ApplicationID], Evidence: evidence})
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if ri, rj := rankOf(findings[i].Severity), rankOf(findings[j].Severity); ri != rj {
			return ri < rj
		}
		return findings[i].Title < findings[j].Title
	})

	tasksRun, caps := b.coverage(ctx, appName)
	methodView := b.methodologyCoverage(ctx, projectID)

	return Data{
		Project:       proj,
		GeneratedAt:   now.UTC(),
		Scope:         scope,
		Findings:      findings,
		Methodology:   methodView,
		SeverityChart: htmltemplate.HTML(viz.SeverityChart(bySeverity)), //nolint:gosec // generated SVG, no user HTML
		CoverageChart: htmltemplate.HTML(coverageHeatmap(findings)),     //nolint:gosec // generated SVG, no user HTML
		Summary: Summary{
			Applications: len(apps),
			Assets:       assetCount,
			TasksRun:     tasksRun,
			Capabilities: caps,
			Total:        len(findings),
			BySeverity:   bySeverity,
		},
	}, nil
}

// methodologyCoverage assembles the project's adopted-checklist coverage for the report.
func (b *Builder) methodologyCoverage(ctx context.Context, projectID string) methodology.View {
	adopted, err := b.src.ListAdoptedMethodologies(ctx, projectID)
	if err != nil || len(adopted) == 0 {
		return methodology.View{}
	}
	entries, err := b.src.ListCoverage(ctx, projectID)
	if err != nil {
		return methodology.View{}
	}
	states := make(map[string]methodology.State, len(entries))
	for _, e := range entries {
		states[e.ItemID] = methodology.State{Status: e.Status, Note: e.Note}
	}
	reg := b.methods
	if reg == nil {
		reg = methodology.BuiltIns()
	}
	return methodology.BuildCoverage(reg, adopted, states)
}

// coverageHeatmap builds a severity × status matrix over the reportable findings as an SVG figure.
func coverageHeatmap(findings []Finding) string {
	sevRows := []string{"critical", "high", "medium", "low", "info"}
	statusCols := []string{model.FindingOpen, model.FindingConfirmed, model.FindingRemediated, model.FindingAccepted}
	sevIdx := map[string]int{}
	for i, s := range sevRows {
		sevIdx[s] = i
	}
	statusIdx := map[string]int{}
	for i, s := range statusCols {
		statusIdx[s] = i
	}
	counts := make([][]int, len(sevRows))
	for i := range counts {
		counts[i] = make([]int, len(statusCols))
	}
	for _, f := range findings {
		ri, rok := sevIdx[f.Severity]
		ci, cok := statusIdx[f.Status]
		if rok && cok {
			counts[ri][ci]++
		}
	}
	rowLabels := []string{"CRIT", "HIGH", "MED", "LOW", "INFO"}
	colLabels := []string{"open", "confirmed", "remediated", "accepted"}
	return viz.Heatmap(rowLabels, colLabels, counts)
}

// coverage counts tasks run against the project's applications and the distinct capabilities used.
func (b *Builder) coverage(ctx context.Context, appName map[string]string) (int, []string) {
	tasks, err := b.src.ListTasks(ctx, 1000)
	if err != nil {
		return 0, nil
	}
	capSet := map[string]bool{}
	run := 0
	for _, t := range tasks {
		if t.ApplicationID == nil || appName[*t.ApplicationID] == "" {
			continue
		}
		run++
		capSet[t.CapabilityID] = true
	}
	caps := make([]string, 0, len(capSet))
	for c := range capSet {
		caps = append(caps, c)
	}
	sort.Strings(caps)
	return run, caps
}
