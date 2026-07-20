package analyst

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestPlaybookStepsReferenceRealProfiles(t *testing.T) {
	valid := map[string]bool{}
	for _, p := range Profiles() {
		valid[p.ID] = true
	}
	for _, pb := range Playbooks() {
		if len(pb.Steps) == 0 {
			t.Errorf("playbook %q has no steps", pb.ID)
		}
		keys := map[string]bool{}
		for _, s := range pb.Steps {
			if s.Gate {
				if s.Profile != "" {
					t.Errorf("playbook %q gate step %q must have no profile (it's a pause)", pb.ID, s.Key)
				}
			} else if !valid[s.Profile] {
				t.Errorf("playbook %q step %q references unknown profile %q", pb.ID, s.Key, s.Profile)
			}
			keys[s.Key] = true
		}
		// Dependencies must reference earlier step keys.
		for _, s := range pb.Steps {
			for _, d := range s.DependsOn {
				if !keys[d] {
					t.Errorf("playbook %q step %q depends on unknown step %q", pb.ID, s.Key, d)
				}
			}
		}
	}
}

func TestStartPlanValidates(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)

	// No provider configured.
	noProv := NewService(store.NewCombinedManager(db), nil, nil, "", nil)
	if _, err := noProv.StartPlan(ctx, "proj", "onboarding"); err == nil {
		t.Fatal("StartPlan should require a provider")
	}
	// Unknown playbook.
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})
	if _, err := svc.StartPlan(ctx, "proj", "nope"); err == nil {
		t.Fatal("StartPlan should reject an unknown playbook")
	}
}

func TestRunPlanHappyPath(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	// Empty mock → every delegated sub-agent answers immediately, no tools.
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	pb, _ := PlaybookByID("onboarding")
	plan := model.Plan{ProjectID: projectID, PlaybookID: pb.ID, Goal: pb.Goal}
	for _, s := range pb.Steps {
		plan.Steps = append(plan.Steps, model.PlanStep{Key: s.Key, Profile: s.Profile, Instruction: s.Instruction, DependsOn: s.DependsOn})
	}
	created, err := db.CreatePlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	svc.runPlan(created) // synchronous

	got, err := db.GetPlan(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.PlanDone {
		t.Fatalf("plan status = %q", got.Status)
	}
	for _, s := range got.Steps {
		if s.Status != model.StepDone {
			t.Fatalf("step %q status = %q", s.Key, s.Status)
		}
	}
}

// A gate step parks the plan in 'waiting' before its dependents run; approving it resumes the run to done.
func TestRunPlanGatePausesThenResumes(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	plan := model.Plan{ProjectID: projectID, PlaybookID: "custom", Steps: []model.PlanStep{
		{Key: "recon", Profile: "report-writer", Instruction: "recon"},
		{Key: "gate", Gate: true, DependsOn: []string{"recon"}},
		{Key: "act", Profile: "report-writer", Instruction: "act", DependsOn: []string{"recon", "gate"}},
	}}
	created, err := db.CreatePlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	svc.runPlan(created) // synchronous — runs recon, then parks at the gate

	got, _ := db.GetPlan(ctx, created.ID)
	if got.Status != model.PlanWaiting {
		t.Fatalf("plan should be waiting at the gate, status = %q", got.Status)
	}
	var gateID, actStatus string
	for _, s := range got.Steps {
		switch s.Key {
		case "gate":
			gateID = s.ID
			if s.Status != model.StepWaiting {
				t.Fatalf("gate step status = %q, want waiting", s.Status)
			}
		case "act":
			actStatus = s.Status
		case "recon":
			if s.Status != model.StepDone {
				t.Fatalf("recon should have run before the gate, status = %q", s.Status)
			}
		}
	}
	if actStatus != model.StepPending {
		t.Fatalf("act must not run before the gate is approved, status = %q", actStatus)
	}

	// Approve the gate — the resumed run completes. (ResolvePlanGate relaunches runPlan on a goroutine;
	// call the store + a synchronous runPlan directly so the test is deterministic.)
	if err := db.ResolvePlanGate(ctx, gateID, true, "looks good"); err != nil {
		t.Fatal(err)
	}
	_ = db.UpdatePlanStatus(ctx, created.ID, model.PlanRunning)
	svc.runPlan(created)

	got, _ = db.GetPlan(ctx, created.ID)
	if got.Status != model.PlanDone {
		t.Fatalf("approved plan should complete, status = %q", got.Status)
	}
	for _, s := range got.Steps {
		if s.Status != model.StepDone {
			t.Fatalf("step %q status = %q after approval", s.Key, s.Status)
		}
	}
}

// Denying a gate skips it, and its dependents are skipped, so the plan ends failed.
func TestRunPlanGateDenySkipsDependents(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	plan := model.Plan{ProjectID: projectID, PlaybookID: "custom", Steps: []model.PlanStep{
		{Key: "gate", Gate: true},
		{Key: "act", Profile: "report-writer", Instruction: "act", DependsOn: []string{"gate"}},
	}}
	created, _ := db.CreatePlan(ctx, plan)
	svc.runPlan(created) // parks at the gate immediately

	got, _ := db.GetPlan(ctx, created.ID)
	var gateID string
	for _, s := range got.Steps {
		if s.Key == "gate" {
			gateID = s.ID
		}
	}
	if err := db.ResolvePlanGate(ctx, gateID, false, "not now"); err != nil {
		t.Fatal(err)
	}
	_ = db.UpdatePlanStatus(ctx, created.ID, model.PlanRunning)
	svc.runPlan(created)

	got, _ = db.GetPlan(ctx, created.ID)
	if got.Status != model.PlanFailed {
		t.Fatalf("denied plan should fail, status = %q", got.Status)
	}
	for _, s := range got.Steps {
		if s.Key == "act" && s.Status != model.StepSkipped {
			t.Fatalf("act should be skipped after deny, status = %q", s.Status)
		}
	}
}

// countingProvider records the peak number of Complete calls in flight at once — so a test can prove that
// independent plan steps actually ran concurrently rather than one-at-a-time.
type countingProvider struct {
	mu       sync.Mutex
	inflight int
	peak     int
}

func (p *countingProvider) Name() string { return "counting" }

func (p *countingProvider) Complete(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	p.mu.Lock()
	p.inflight++
	if p.inflight > p.peak {
		p.peak = p.inflight
	}
	p.mu.Unlock()

	time.Sleep(30 * time.Millisecond) // hold the call so overlapping steps actually overlap

	p.mu.Lock()
	p.inflight--
	p.mu.Unlock()
	return llm.CompletionResponse{Text: `{"answer":"done"}`}, nil
}

// A fan-out (root → {a,b,c} → join) runs the three independent middle steps in one concurrent wave, and the
// join still receives every branch's result. Proves parallel scheduling (ADR-0046).
func TestRunPlanRunsIndependentStepsConcurrently(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	prov := &countingProvider{}
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", prov)

	plan := model.Plan{ProjectID: projectID, PlaybookID: "custom", Steps: []model.PlanStep{
		{Key: "root", Profile: "report-writer", Instruction: "root"},
		{Key: "a", Profile: "report-writer", Instruction: "a", DependsOn: []string{"root"}},
		{Key: "b", Profile: "report-writer", Instruction: "b", DependsOn: []string{"root"}},
		{Key: "c", Profile: "report-writer", Instruction: "c", DependsOn: []string{"root"}},
		{Key: "join", Profile: "report-writer", Instruction: "join", DependsOn: []string{"a", "b", "c"}},
	}}
	created, err := db.CreatePlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	svc.runPlan(created)

	got, _ := db.GetPlan(ctx, created.ID)
	if got.Status != model.PlanDone {
		t.Fatalf("plan status = %q, want done", got.Status)
	}
	for _, s := range got.Steps {
		if s.Status != model.StepDone {
			t.Fatalf("step %q status = %q", s.Key, s.Status)
		}
	}
	if prov.peak < 2 {
		t.Fatalf("independent steps did not run concurrently (peak in-flight = %d, want >= 2)", prov.peak)
	}
}

// The assessment playbook's scanners fan out: driving the real playbook, the parallel scheduler runs the
// SAST/SCA/secrets steps as one concurrent wave, then pauses at the approval gate before validation.
func TestAssessmentScannersRunInParallel(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	prov := &countingProvider{}
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", prov)

	pb, ok := PlaybookByID("assessment")
	if !ok {
		t.Fatal("assessment playbook not found")
	}
	plan := model.Plan{ProjectID: projectID, PlaybookID: pb.ID, Goal: pb.Goal}
	for _, s := range pb.Steps {
		plan.Steps = append(plan.Steps, model.PlanStep{Key: s.Key, Profile: s.Profile, Instruction: s.Instruction, DependsOn: s.DependsOn, Gate: s.Gate})
	}
	created, err := db.CreatePlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	svc.runPlan(created)

	got, _ := db.GetPlan(ctx, created.ID)
	// Recon + all four scanners + triage have run; the plan is parked at the approval gate.
	if got.Status != model.PlanWaiting {
		t.Fatalf("plan should pause at the approval gate, status = %q", got.Status)
	}
	for _, key := range []string{"recon", "scan-sast", "scan-sca-grype", "scan-sca-govulncheck", "scan-secrets", "triage"} {
		if st := stepStatus(got, key); st != model.StepDone {
			t.Fatalf("step %q status = %q, want done", key, st)
		}
	}
	// The four scanners are independent and depend only on recon, so they ran concurrently.
	if prov.peak < 2 {
		t.Fatalf("scanners did not run in parallel (peak in-flight = %d)", prov.peak)
	}
}

func stepStatus(p model.Plan, key string) string {
	for _, s := range p.Steps {
		if s.Key == key {
			return s.Status
		}
	}
	return ""
}

func TestRunPlanBreaksOnCycle(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	// A depends on B, B depends on A — unresolvable.
	plan := model.Plan{ProjectID: projectID, PlaybookID: "custom", Steps: []model.PlanStep{
		{Key: "a", Profile: "report-writer", Instruction: "a", DependsOn: []string{"b"}},
		{Key: "b", Profile: "report-writer", Instruction: "b", DependsOn: []string{"a"}},
	}}
	created, err := db.CreatePlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	svc.runPlan(created)

	got, _ := db.GetPlan(ctx, created.ID)
	if got.Status != model.PlanFailed {
		t.Fatalf("a cyclic plan should fail, status = %q", got.Status)
	}
}

func TestPlansListedByProject(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	if _, err := db.CreatePlan(ctx, model.Plan{ProjectID: projectID, PlaybookID: "onboarding"}); err != nil {
		t.Fatal(err)
	}
	plans, err := db.ListPlansByProject(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].PlaybookID != "onboarding" {
		t.Fatalf("plans = %+v", plans)
	}
}
