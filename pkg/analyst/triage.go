package analyst

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// triageSignalKeys are the routing attributes worth putting in front of the triage agent, most-exploitable
// first — these are what let it fast-dismiss (an unreachable, unexposed finding is usually noise).
var triageSignalKeys = []string{"reachable_confirmed", "route_reachable", "reachable", "exposed", "verified", "outdated"}

func triageSignals(a map[string]string) string {
	if a == nil {
		return ""
	}
	var s []string
	for _, k := range triageSignalKeys {
		if a[k] == "true" {
			s = append(s, k)
		}
	}
	if a["reachable"] == "false" {
		s = append(s, "reachable=false")
	}
	return strings.Join(s, ",")
}

// StartTriage launches a background batch-triage run: ONE triage agent works down the given observations (or
// all still-unreviewed ones when ids is empty), leaning on reachability/exposure signals to dismiss noise
// fast (reversible, with a rationale), flag the genuinely-uncertain for a human, and propose findings
// (human-gated) for clear issues. It returns immediately with the count handed to the agent; the run
// proceeds on a detached context and its effects land on the observations the human is watching. A single
// shared-context agent — not one thread per item — keeps this cheap at scale.
func (svc *Service) StartTriage(projectID string, ids []string) (int, error) {
	if svc.provider == nil {
		return 0, errors.New("no LLM provider configured")
	}
	ctx := context.Background()
	all, err := svc.p(projectID).ListObservationsByProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var picked []model.Observation
	for _, o := range all {
		if len(want) > 0 {
			if !want[o.ID] {
				continue
			}
		} else if o.ReviewState != model.ReviewUnreviewed {
			continue // default: only the untriaged backlog
		}
		picked = append(picked, o)
	}
	if len(picked) == 0 {
		return 0, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Triage these %d raw observations. Decide each quickly using the signals — dismiss noise and "+
		"false positives with triage_observation (one-line rationale each), flag genuine-looking ones that need a "+
		"human's decision, and use create_finding only for a clearly real issue (a human still approves it). Lean on "+
		"reachability/exposure first; read code only when the call isn't obvious. Work through the whole list.\n\n", len(picked))
	for _, o := range picked {
		fmt.Fprintf(&b, "- id=%s [%s] %s", o.ID, o.Severity, o.Title)
		if o.RuleID != "" {
			fmt.Fprintf(&b, " rule=%s", o.RuleID)
		}
		if o.Location != "" {
			fmt.Fprintf(&b, " at %s", o.Location)
		}
		if sig := triageSignals(o.Attributes); sig != "" {
			fmt.Fprintf(&b, " signals=%s", sig)
		}
		b.WriteByte('\n')
	}
	seed := b.String()

	profile := svc.resolveProfile(ctx, "triage")
	tools := profileToolNames(profile)
	pickedIDs := make([]string, len(picked))
	for i, o := range picked {
		pickedIDs[i] = o.ID
	}
	go svc.runBatchTriage(projectID, seed, tools, pickedIDs)
	return len(picked), nil
}

// runBatchTriage drives the detached triage agent and, when it settles, raises a notification so the
// human knows what happened — the run outlives the HTTP request that started it, so this is the only
// durable signal it produced. On failure it says so (previously silent, log-only).
func (svc *Service) runBatchTriage(projectID, seed string, tools, pickedIDs []string) {
	ctx := context.Background()
	pid := projectID
	findingsBefore := 0
	if fs, err := svc.p(projectID).ListFindings(ctx); err == nil {
		findingsBefore = len(fs)
	}

	if _, err := svc.Delegate(ctx, projectID, "triage", seed, tools); err != nil {
		log.Printf("triage: batch run for project %s failed: %v", projectID, err)
		_, _ = svc.p(projectID).CreateNotification(ctx, model.Notification{
			Kind:      model.NotifyInfo,
			Title:     "AI triage didn’t finish",
			Body:      "The run stopped before completing: " + err.Error(),
			ProjectID: &pid,
			Link:      "observations",
		})
		return
	}

	dismissed, flagged, untouched := 0, 0, 0
	all, _ := svc.p(projectID).ListObservationsByProject(ctx, projectID)
	byID := make(map[string]model.Observation, len(all))
	for _, o := range all {
		byID[o.ID] = o
	}
	for _, id := range pickedIDs {
		o, ok := byID[id]
		if !ok {
			continue
		}
		switch {
		case o.ReviewState == model.ReviewRejected:
			dismissed++
		case o.Attributes["triage_flag"] == "true":
			flagged++
		default:
			untouched++
		}
	}
	proposed := 0
	if fs, err := svc.p(projectID).ListFindings(ctx); err == nil {
		if d := len(fs) - findingsBefore; d > 0 {
			proposed = d
		}
	}
	_, _ = svc.p(projectID).CreateNotification(ctx, model.Notification{
		Kind:      model.NotifyInfo,
		Title:     "AI triage complete",
		Body:      fmt.Sprintf("Dismissed %d, flagged %d for your review, proposed %d finding(s); %d left untouched.", dismissed, flagged, proposed, untouched),
		ProjectID: &pid,
		Link:      "observations",
	})
}
