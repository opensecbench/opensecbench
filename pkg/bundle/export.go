package bundle

import (
	"context"
	"errors"
	"io"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Export gathers a project's assessment graph and returns an encrypted bundle sealed with passphrase.
// It reads the project's rows from its own database and its durable targets/KB from the global database
// (ADR-0049); blobs is the project's own content store.
//
// The default (shareable) bundle is the ADR-0012 deliverable: project, targets, applications, assets,
// scope, findings + their supporting observations, evidence artifacts + CAS blobs, and KB. When full is
// set, it additionally captures the working state a demo/backup needs (ADR-0060): all observations,
// Analyst threads + messages, investigations, HTTP exchanges, reports, context items (uploaded
// docs/notes), methodology adoption + coverage, and the engagement record — plus the CAS blobs those
// reference. A full bundle carries the Analyst's raw reasoning and captured traffic and is NOT a
// client-facing deliverable.
func Export(ctx context.Context, mgr *store.Manager, blobs *cas.Store, projectID, passphrase string, full bool) ([]byte, error) {
	proj, err := mgr.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	db, err := mgr.Project(projectID)
	if err != nil {
		return nil, err
	}
	d := &Data{Version: FormatVersion, Project: proj, Blobs: map[string][]byte{}}

	for _, tid := range proj.TargetIDs {
		if t, err := mgr.Global().GetTarget(ctx, tid); err == nil {
			d.Targets = append(d.Targets, t)
		}
	}

	apps, err := db.ListApplicationsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	inProject := map[string]bool{}
	for _, a := range apps {
		d.Applications = append(d.Applications, a)
		inProject[a.ID] = true
		assets, err := db.ListAssetsByApplication(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		d.Assets = append(d.Assets, assets...)
	}

	if d.Scope, err = db.ListScopeEntries(ctx, projectID); err != nil {
		return nil, err
	}

	// addArtifact/addObs dedupe and pull the referenced CAS blob along; shared by both modes.
	artSeen := map[string]bool{}
	addArtifact := func(artID string) {
		if artID == "" || artSeen[artID] {
			return
		}
		art, err := db.GetArtifact(ctx, artID)
		if err != nil {
			return
		}
		artSeen[artID] = true
		d.Artifacts = append(d.Artifacts, art)
		if b, err := readBlob(blobs, art.SHA256); err == nil {
			d.Blobs[art.SHA256] = b
		}
	}
	obsSeen := map[string]bool{}
	addObs := func(o model.Observation) {
		if obsSeen[o.ID] {
			return
		}
		obsSeen[o.ID] = true
		d.Observations = append(d.Observations, o)
		if o.ArtifactID != nil {
			addArtifact(*o.ArtifactID)
		}
	}

	all, err := db.ListFindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range all {
		if f.ApplicationID == nil || !inProject[*f.ApplicationID] {
			continue
		}
		finding, err := db.GetFinding(ctx, f.ID) // populates ObservationIDs
		if err != nil {
			return nil, err
		}
		d.Findings = append(d.Findings, finding)
		for _, oid := range finding.ObservationIDs {
			o, err := db.GetObservation(ctx, oid)
			if err != nil {
				continue
			}
			addObs(o)
		}
	}

	if d.KB, err = mgr.ListKBForProject(ctx, projectID); err != nil {
		return nil, err
	}

	if !full {
		return seal(d, passphrase)
	}

	// --- Full-fidelity working state (ADR-0060) ---

	// All observations, not just the finding-backed ones, so the Triage surface and investigations
	// restore with their full history.
	obs, err := db.ListObservationsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, o := range obs {
		addObs(o)
	}

	// Analyst threads (active) + their messages.
	threads, err := db.ListThreads(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range threads {
		if t.ProjectID == nil || *t.ProjectID != projectID {
			continue
		}
		d.Threads = append(d.Threads, t)
		msgs, err := db.ListMessages(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		d.Messages = append(d.Messages, msgs...)
	}

	if d.Investigations, err = db.ListInvestigationsByProject(ctx, projectID); err != nil {
		return nil, err
	}
	// Ensure each investigation's observation travels even if it wasn't project-attributed above.
	for _, inv := range d.Investigations {
		if o, err := db.GetObservation(ctx, inv.ObservationID); err == nil {
			addObs(o)
		}
	}
	// Captured proxy/replay traffic. NOTE: capped at the store's default (200 most recent); ample for a
	// demo/backup, raise via a dedicated lister if a full traffic archive is ever needed.
	if d.Exchanges, err = db.ListExchangesByProject(ctx, projectID); err != nil {
		return nil, err
	}

	// Reports + their rendered CAS blobs.
	reports, err := db.ListReportsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, r := range reports {
		d.Reports = append(d.Reports, r)
		addArtifact(r.ArtifactID)
	}

	// Context items (uploaded docs/notes/emails) + their CAS blobs.
	items, err := db.ListContextItemsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, ci := range items {
		d.ContextItems = append(d.ContextItems, ci)
		addArtifact(ci.ArtifactID)
	}

	if d.Adopted, err = db.ListAdoptedMethodologies(ctx, projectID); err != nil {
		return nil, err
	}
	if d.Coverage, err = db.ListCoverage(ctx, projectID); err != nil {
		return nil, err
	}

	if eng, err := db.GetEngagement(ctx, projectID); err == nil {
		d.Engagement = &eng
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	return seal(d, passphrase)
}

func readBlob(blobs *cas.Store, sha string) ([]byte, error) {
	rc, err := blobs.Open(sha)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}
