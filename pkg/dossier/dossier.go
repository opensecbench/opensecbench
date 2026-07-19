// Package dossier assembles a project's or target's knowledge-base entries into a consolidated
// "what we know about this system" view (ADR-0042) — the thing an assessor (or the agent) reads first to
// know how the target is set up and what to look for. Deterministic: it groups and orders the (already
// inheritance-resolved, ADR-0041) KB entries; no model call.
package dossier

import (
	"sort"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// kindOrder is the reading order — the big picture first, then how it's built, secured, deployed, and where
// data flows, ending with the pitfalls and tactics to check.
var kindOrder = []string{
	model.KBArchitecture, model.KBTechStack, model.KBAuth, model.KBEnvironment,
	model.KBDataFlow, model.KBEndpoint, model.KBConvention, model.KBGotcha, model.KBTactic,
}

var kindTitle = map[string]string{
	model.KBArchitecture: "Architecture",
	model.KBTechStack:    "Technology stack",
	model.KBAuth:         "Authentication & authorization",
	model.KBEnvironment:  "Environment & deployment",
	model.KBDataFlow:     "Data flows",
	model.KBEndpoint:     "Endpoints",
	model.KBConvention:   "Conventions",
	model.KBGotcha:       "Gotchas & pitfalls",
	model.KBTactic:       "Testing tactics",
}

// Dossier is the consolidated knowledge view.
type Dossier struct {
	Subject  string    `json:"subject"`
	Entries  int       `json:"entries"`
	Sections []Section `json:"sections"`
}

// Section groups entries of one kind.
type Section struct {
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Entries []Entry `json:"entries"`
}

// Entry is one piece of knowledge in the dossier.
type Entry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	Scope       string `json:"scope"`        // target | group | org | global (where the knowledge lives)
	ReviewState string `json:"review_state"` // confirmed | unreviewed (draft) | rejected
}

// Assemble groups KB entries into a dossier: kinds in reading order; within a kind, confirmed before drafts,
// then most-specific scope first; rejected entries are dropped.
func Assemble(subject string, entries []model.KBEntry) Dossier {
	byKind := map[string][]Entry{}
	total := 0
	for _, e := range entries {
		if e.ReviewState == model.ReviewRejected {
			continue
		}
		byKind[e.Kind] = append(byKind[e.Kind], Entry{
			ID: e.ID, Title: e.Title, Body: e.Body, Scope: e.Scope, ReviewState: e.ReviewState,
		})
		total++
	}
	d := Dossier{Subject: subject, Entries: total}
	seen := map[string]bool{}
	add := func(kind string) {
		es := byKind[kind]
		if len(es) == 0 {
			return
		}
		sort.SliceStable(es, func(i, j int) bool {
			if r := reviewRank(es[i].ReviewState) - reviewRank(es[j].ReviewState); r != 0 {
				return r < 0
			}
			return scopeRank(es[i].Scope) < scopeRank(es[j].Scope)
		})
		d.Sections = append(d.Sections, Section{Kind: kind, Title: sectionTitle(kind), Entries: es})
		seen[kind] = true
	}
	for _, k := range kindOrder {
		add(k)
	}
	// Any kinds not in the fixed order (e.g. a future kind) come last, alphabetically.
	var extra []string
	for k := range byKind {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		add(k)
	}
	return d
}

func sectionTitle(kind string) string {
	if t, ok := kindTitle[kind]; ok {
		return t
	}
	return strings.Title(strings.ReplaceAll(kind, "_", " ")) //nolint:staticcheck // titles are ASCII labels
}

func reviewRank(s string) int {
	if s == model.ReviewConfirmed {
		return 0
	}
	return 1 // unreviewed drafts after confirmed
}

func scopeRank(s string) int {
	switch s {
	case model.KBScopeTarget:
		return 0
	case model.KBScopeGroup:
		return 1
	case model.KBScopeOrg:
		return 2
	default:
		return 3
	}
}

// Markdown renders the dossier as a readable brief.
func (d Dossier) Markdown() string {
	var b strings.Builder
	b.WriteString("# What we know: " + d.Subject + "\n\n")
	if d.Entries == 0 {
		b.WriteString("_No knowledge captured yet. Run onboarding or the capture-knowledge playbook._\n")
		return b.String()
	}
	for _, s := range d.Sections {
		b.WriteString("## " + s.Title + "\n\n")
		for _, e := range s.Entries {
			b.WriteString("- **" + e.Title + "**")
			tags := scopeTag(e.Scope) + reviewTag(e.ReviewState)
			if tags != "" {
				b.WriteString(" " + tags)
			}
			b.WriteString("\n")
			if body := strings.TrimSpace(e.Body); body != "" {
				b.WriteString("  " + strings.ReplaceAll(body, "\n", "\n  ") + "\n")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func scopeTag(scope string) string {
	if scope == "" || scope == model.KBScopeTarget {
		return ""
	}
	return "`(" + scope + ")`" // mark inherited knowledge (org/group/global)
}

func reviewTag(state string) string {
	if state == model.ReviewConfirmed {
		return ""
	}
	return "_(draft)_"
}
