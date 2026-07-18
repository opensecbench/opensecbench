// Package report builds engagement deliverables from project data (ADR-0008). A Builder gathers a
// Data snapshot — enforcing the "confirmed findings with traceable evidence only" rule in one place
// — and templates render it to Markdown/HTML (PDF via a headless browser layers on top).
package report

import (
	"context"
	"sort"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
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
}

// Data is the immutable snapshot a report renders from.
type Data struct {
	Project     model.Project
	GeneratedAt time.Time
	Scope       []model.ScopeEntry
	Summary     Summary
	Findings    []Finding
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

// Finding is a reportable finding expanded with its evidence and application name.
type Finding struct {
	model.Finding
	AppName  string
	Evidence []model.Observation
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
type Builder struct{ src Source }

// NewBuilder returns a Builder over src.
func NewBuilder(src Source) *Builder { return &Builder{src: src} }

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

	return Data{
		Project:     proj,
		GeneratedAt: now.UTC(),
		Scope:       scope,
		Findings:    findings,
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
