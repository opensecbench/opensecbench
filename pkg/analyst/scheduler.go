package analyst

import (
	"context"
	"time"

	"github.com/opensecbench/opensecbench/pkg/store"
)

// Scheduler fires due playbook schedules on a cadence (ADR-0019 step 4). It is a trigger, not a busy
// loop: each firing starts one bounded plan, then the schedule waits for its next interval. A broken
// trigger still advances the schedule, so it can't fire every tick.
type Scheduler struct {
	mgr     *store.Manager
	trigger func(ctx context.Context, projectID, playbookID string) error
	audit   func(action, detail string)
	tick    time.Duration
}

// NewScheduler builds a scheduler. trigger starts a plan for (project, playbook); audit may be nil.
func NewScheduler(mgr *store.Manager, trigger func(context.Context, string, string) error, audit func(action, detail string)) *Scheduler {
	return &Scheduler{mgr: mgr, trigger: trigger, audit: audit, tick: time.Minute}
}

// Run ticks until ctx is cancelled, firing due schedules each tick.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.runDue(ctx, now.UTC())
		}
	}
}

// runDue fires every enabled schedule whose next run has passed and advances each one. Exposed for
// deterministic testing with an injected clock.
func (s *Scheduler) runDue(ctx context.Context, now time.Time) {
	due, err := s.mgr.ListDueSchedules(ctx, now)
	if err != nil {
		return
	}
	for _, sc := range due {
		if err := s.trigger(ctx, sc.ProjectID, sc.PlaybookID); err != nil {
			s.log("schedule.error", sc.PlaybookID+": "+err.Error())
		} else {
			s.log("schedule.fired", sc.PlaybookID)
		}
		// Advance regardless of success, so a failing schedule doesn't fire on every tick.
		next := now.Add(time.Duration(sc.IntervalSeconds) * time.Second)
		_ = s.markRun(ctx, sc.ProjectID, sc.ID, now, next)
	}
}

func (s *Scheduler) log(action, detail string) {
	if s.audit != nil {
		s.audit(action, detail)
	}
}

// markRun records a schedule's run in its own project's database (ADR-0049).
func (s *Scheduler) markRun(ctx context.Context, projectID, scheduleID string, now, next time.Time) error {
	db, err := s.mgr.Project(projectID)
	if err != nil || db == nil {
		db = s.mgr.Global()
	}
	return db.MarkScheduleRun(ctx, scheduleID, now, next)
}
