package dossier

import (
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Knowledge freshness (ADR-0043). A confirmed fact stays fresh for a kind-specific window, after which it
// goes stale and should be re-verified — so accumulated knowledge doesn't silently rot. How fast a fact
// changes drives its window: the shape of a system (architecture, conventions, tactics) moves slowly; how
// it's secured and where data flows moves at release cadence; concrete endpoints and environments churn
// fastest.
const (
	day               = 24 * time.Hour
	defaultStaleAfter = 180 * day
)

var staleAfter = map[string]time.Duration{
	model.KBArchitecture: 365 * day, // structural — changes rarely
	model.KBConvention:   365 * day,
	model.KBTactic:       365 * day,
	model.KBAuth:         180 * day, // security posture — re-check each engagement
	model.KBTechStack:    180 * day,
	model.KBDataFlow:     180 * day,
	model.KBGotcha:       180 * day,
	model.KBEndpoint:     90 * day, // concrete surface — moves with each release
	model.KBEnvironment:  90 * day,
}

// StaleAfter is how long a fact of the given kind stays fresh before it should be re-verified.
func StaleAfter(kind string) time.Duration {
	if d, ok := staleAfter[kind]; ok {
		return d
	}
	return defaultStaleAfter
}

// IsStale reports whether a fact of the given kind, last verified at lastVerified, is stale as of now.
// A never-verified entry (zero time) is NOT stale — it's an unverified draft, a different state; staleness
// is about confirmed knowledge that has aged out.
func IsStale(kind string, lastVerified, now time.Time) bool {
	if lastVerified.IsZero() {
		return false
	}
	return now.Sub(lastVerified) > StaleAfter(kind)
}
