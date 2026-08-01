package analyst

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opensecbench/opensecbench/pkg/action"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
)

// Custom actions (ADR-0059). An action is a thin union of two execution paths that already exist: an
// agent action delegates to a saved profile (Service.Delegate), a script action runs a templated RunSpec
// in the sandbox. This file holds the executor; persistence of the run record and event streaming are
// owned by the API layer (which holds the events hub), mirroring the methodology-check pattern.

// ErrActionTechniqueNotPermitted is returned when an action's ROE technique isn't authorized by the
// project's engagement. Mirrors task.ErrTechniqueNotPermitted, which keys on a capability manifest and so
// can't be reused directly for an action.
var ErrActionTechniqueNotPermitted = errors.New("analyst: action technique not permitted by this engagement")

// checkActionTechnique enforces the engagement rules-of-engagement gate for an action, the same way the
// task engine gates a capability (ADR-0051/0054): a passive action (no technique) is always allowed; an
// unconfigured or missing engagement is unconstrained; otherwise the technique must be permitted.
func (svc *Service) checkActionTechnique(ctx context.Context, projectID, technique string) error {
	if technique == "" || projectID == "" {
		return nil
	}
	db := svc.p(projectID)
	if db == nil {
		return nil
	}
	eng, err := db.GetEngagement(ctx, projectID)
	if err != nil {
		return nil // no engagement (ErrNotFound) or unreadable → unconstrained, as the engine treats it
	}
	if len(eng.Techniques) == 0 {
		return nil
	}
	if !eng.Techniques[technique] {
		return fmt.Errorf("%w: action uses technique %q", ErrActionTechniqueNotPermitted, technique)
	}
	return nil
}

// ExecuteAction runs an action against a subject and returns its text output plus the id of the CAS
// artifact the output was captured to. It is synchronous; the caller runs it in the background and
// records the resulting action.Run. The ROE gate is checked first.
func (svc *Service) ExecuteAction(ctx context.Context, projectID string, a action.Action, subj action.Subject) (output, artifactID string, err error) {
	if err := svc.checkActionTechnique(ctx, projectID, a.Technique); err != nil {
		return "", "", err
	}

	switch a.Kind {
	case action.KindAgent:
		output, err = svc.executeAgentAction(ctx, projectID, a, subj)
	case action.KindScript:
		output, err = svc.executeScriptAction(ctx, projectID, a, subj)
	default:
		return "", "", fmt.Errorf("analyst: unknown action kind %q", a.Kind)
	}
	if err != nil {
		return "", "", err
	}

	artifactID = svc.captureActionOutput(ctx, projectID, a, output)
	return output, artifactID, nil
}

func (svc *Service) executeAgentAction(ctx context.Context, projectID string, a action.Action, subj action.Subject) (string, error) {
	if svc.provider == nil && svc.resolver == nil {
		return "", errors.New("no LLM provider configured")
	}
	task := action.Render(a.Instruction, subj)
	authorize := profileToolNames(svc.resolveProfile(ctx, a.ProfileID))
	res, err := svc.Delegate(ctx, projectID, a.ProfileID, task, authorize)
	if err != nil {
		return "", err
	}
	if res.Stopped {
		return res.Answer, errors.New("agent stopped before finishing (step budget exhausted)")
	}
	return res.Answer, nil
}

func (svc *Service) executeScriptAction(ctx context.Context, projectID string, a action.Action, subj action.Subject) (string, error) {
	if svc.workspaceRoot == "" {
		return "", errors.New("workspace is not configured")
	}
	workDir := filepath.Join(svc.workspaceRoot, projectID)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", err
	}
	spec := a.PlanScript(subj, workDir)
	res, err := runner.LocalRunner{}.Run(ctx, spec)
	if err != nil {
		return "", err
	}
	out := string(res.Stdout)
	if res.ExitCode != 0 {
		return out, fmt.Errorf("script exited %d: %s", res.ExitCode, truncate(string(res.Stderr), 500))
	}
	return out, nil
}

// captureActionOutput stores the run's text output in the CAS as an artifact linked to the project, so it
// is durable, downloadable evidence. A failure to capture is non-fatal (the output still returns to the
// caller) — best-effort, so a CAS hiccup never loses a completed run.
func (svc *Service) captureActionOutput(ctx context.Context, projectID string, a action.Action, output string) string {
	blobs := svc.casFor(projectID)
	if blobs == nil || output == "" {
		return ""
	}
	digest, err := blobs.Put(bytes.NewReader([]byte(output)))
	if err != nil {
		return ""
	}
	name, media := a.ID+".md", "text/markdown; charset=utf-8"
	if a.Kind == action.KindScript {
		name, media = a.ID+".txt", "text/plain; charset=utf-8"
	}
	art, err := svc.p(projectID).CreateArtifact(ctx, model.Artifact{
		SHA256:    digest,
		Size:      int64(len(output)),
		Kind:      model.ArtifactOutput,
		Name:      name,
		MediaType: media,
	})
	if err != nil {
		return ""
	}
	return art.ID
}
