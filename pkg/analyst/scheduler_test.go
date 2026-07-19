package analyst

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSchedulerRunDueFiresAndAdvances(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	// Interval 1h; first run is due at creation time.
	if _, err := db.CreateSchedule(ctx, projectID, "onboarding", 3600, base); err != nil {
		t.Fatal(err)
	}

	var fired []string
	sched := NewScheduler(db, func(_ context.Context, proj, pb string) error {
		fired = append(fired, proj+"/"+pb)
		return nil
	}, nil)

	// Due now → fires once and advances to base+1h.
	sched.runDue(ctx, base)
	if len(fired) != 1 || fired[0] != projectID+"/onboarding" {
		t.Fatalf("fired = %v", fired)
	}
	got, _ := db.ListSchedulesByProject(ctx, projectID)
	if got[0].LastRunAt == nil || got[0].NextRunAt.Before(base.Add(time.Hour)) {
		t.Fatalf("schedule not advanced: %+v", got[0])
	}

	// Not due a minute later.
	fired = nil
	sched.runDue(ctx, base.Add(time.Minute))
	if len(fired) != 0 {
		t.Fatalf("should not fire before the interval, fired = %v", fired)
	}

	// Due again after the interval.
	sched.runDue(ctx, base.Add(2*time.Hour))
	if len(fired) != 1 {
		t.Fatalf("should fire again after the interval, fired = %v", fired)
	}
}

func TestSchedulerAdvancesOnTriggerError(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if _, err := db.CreateSchedule(ctx, projectID, "onboarding", 3600, base); err != nil {
		t.Fatal(err)
	}
	sched := NewScheduler(db, func(context.Context, string, string) error { return errors.New("boom") }, nil)

	sched.runDue(ctx, base)
	got, _ := db.ListSchedulesByProject(ctx, projectID)
	if got[0].NextRunAt.Before(base.Add(time.Hour)) {
		t.Fatal("a failing schedule must still advance so it doesn't fire every tick")
	}
}

func TestSchedulerSkipsDisabled(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	base := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sc, err := db.CreateSchedule(ctx, projectID, "onboarding", 3600, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetScheduleEnabled(ctx, sc.ID, false); err != nil {
		t.Fatal(err)
	}
	fired := 0
	sched := NewScheduler(db, func(context.Context, string, string) error { fired++; return nil }, nil)

	sched.runDue(ctx, base.Add(time.Hour))
	if fired != 0 {
		t.Fatal("a disabled schedule must not fire")
	}
}
