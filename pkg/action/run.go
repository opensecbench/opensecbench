package action

import "time"

// Run statuses.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunError   = "error"
)

// Run is one execution of an action against one subject (ADR-0059). It is a per-project record: the
// definition is global, but a run is tied to the finding/observation (and project) it acted on. The
// primary output is captured to the CAS as an artifact and summarized here so a subject can show its
// action-run history — the durable "what was run, when, and what came back."
type Run struct {
	ID          string     `json:"id"`
	ActionID    string     `json:"action_id"`
	ActionName  string     `json:"action_name"`
	Kind        Kind       `json:"kind"`
	SubjectKind string     `json:"subject_kind"`
	SubjectID   string     `json:"subject_id"`
	Status      string     `json:"status"` // running | done | error
	Summary     string     `json:"summary,omitempty"`
	Output      string     `json:"output,omitempty"`      // full text output (agent answer / script stdout)
	ArtifactID  string     `json:"artifact_id,omitempty"` // CAS artifact holding the output
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}
