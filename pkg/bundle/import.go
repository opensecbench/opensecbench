package bundle

import (
	"bytes"
	"context"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Import decrypts a bundle and recreates its graph, remapping IDs (re-import safe) while preserving
// evidence content hashes. Durable targets/KB go to the global database; the project's own rows and blobs
// go to a freshly provisioned projects/<id>/ (ADR-0049). It returns the new project id.
func Import(ctx context.Context, mgr *store.Manager, casr cas.Resolver, data []byte, passphrase string) (string, error) {
	d, err := open(data, passphrase)
	if err != nil {
		return "", err
	}
	if d.Version > FormatVersion {
		return "", fmt.Errorf("bundle: version %d is newer than supported (%d)", d.Version, FormatVersion)
	}

	// Targets → new ids in the global database, then the project referencing them.
	targetMap := map[string]string{}
	var newTargetIDs []string
	for _, t := range d.Targets {
		nt, err := mgr.Global().CreateTarget(ctx, t.Name, t.Description, nil)
		if err != nil {
			return "", err
		}
		targetMap[t.ID] = nt.ID
		newTargetIDs = append(newTargetIDs, nt.ID)
	}

	proj, err := mgr.CreateProject(ctx, store.NewProject{Name: d.Project.Name, TargetIDs: newTargetIDs})
	if err != nil {
		return "", err
	}
	db, err := mgr.Project(proj.ID)
	if err != nil {
		return "", err
	}
	blobs, err := casr.For(proj.ID)
	if err != nil {
		return "", err
	}

	appMap := map[string]string{}
	for _, a := range d.Applications {
		na, err := db.CreateApplication(ctx, proj.ID, a.Name)
		if err != nil {
			return "", err
		}
		appMap[a.ID] = na.ID
	}

	for _, as := range d.Assets {
		if _, err := db.CreateAsset(ctx, store.NewAsset{
			ApplicationID: appMap[as.ApplicationID], Type: as.Type, Location: as.Location, Sensitivity: as.Sensitivity,
		}); err != nil {
			return "", err
		}
	}

	// Blobs re-enter the CAS under the same sha256 (content-addressed provenance survives).
	for _, b := range d.Blobs {
		if _, err := blobs.Put(bytes.NewReader(b)); err != nil {
			return "", err
		}
	}

	artMap := map[string]string{}
	for _, art := range d.Artifacts {
		na, err := db.CreateArtifact(ctx, model.Artifact{
			SHA256: art.SHA256, Size: art.Size, Kind: art.Kind, Name: art.Name, MediaType: art.MediaType,
		})
		if err != nil {
			return "", err
		}
		artMap[art.ID] = na.ID
	}

	obsMap := map[string]string{}
	for _, o := range d.Observations {
		var aid *string
		if o.ArtifactID != nil {
			if n, ok := artMap[*o.ArtifactID]; ok {
				aid = &n
			}
		}
		no, err := db.CreateObservation(ctx, model.Observation{
			ProjectID: &proj.ID, ArtifactID: aid, Origin: o.Origin, ReviewState: o.ReviewState,
			Title: o.Title, Detail: o.Detail, Severity: o.Severity, RuleID: o.RuleID, Location: o.Location,
			Attributes: o.Attributes, Fingerprint: o.Fingerprint,
		})
		if err != nil {
			return "", err
		}
		obsMap[o.ID] = no.ID
	}

	for _, f := range d.Findings {
		var aid *string
		if f.ApplicationID != nil {
			if n, ok := appMap[*f.ApplicationID]; ok {
				aid = &n
			}
		}
		oids := make([]string, 0, len(f.ObservationIDs))
		for _, oid := range f.ObservationIDs {
			if n, ok := obsMap[oid]; ok {
				oids = append(oids, n)
			}
		}
		nf, err := db.CreateFinding(ctx, store.NewFinding{
			ApplicationID: aid, Title: f.Title, Severity: f.Severity, Description: f.Description,
			CWE: f.CWE, ObservationIDs: oids,
		})
		if err != nil {
			return "", err
		}
		if f.Status != "" && f.Status != model.FindingOpen {
			if err := db.SetFindingStatus(ctx, nf.ID, f.Status); err != nil {
				return "", err
			}
		}
	}

	for _, k := range d.KB {
		tid, ok := targetMap[k.TargetID]
		if !ok {
			continue
		}
		if _, err := mgr.Global().CreateKBEntry(ctx, model.KBEntry{
			TargetID: tid, Kind: k.Kind, Scope: k.Scope, Title: k.Title, Body: k.Body, Tags: k.Tags,
			Sensitivity: k.Sensitivity, Origin: k.Origin, ReviewState: k.ReviewState, SourceRef: k.SourceRef,
		}); err != nil {
			return "", err
		}
	}

	if err := importFull(ctx, db, d, proj.ID, appMap, obsMap, artMap); err != nil {
		return "", err
	}

	return proj.ID, nil
}

// importFull recreates the ADR-0060 full-fidelity working state, remapping foreign keys onto the newly
// created project/apps/observations/artifacts. Every slice is empty for a shareable (or v1) bundle, so
// this is a no-op there. threadMap is built locally as threads are created.
func importFull(ctx context.Context, db *store.DB, d *Data, projID string, appMap, obsMap, artMap map[string]string) error {
	remapApp := func(old *string) *string {
		if old == nil {
			return nil
		}
		if n, ok := appMap[*old]; ok {
			return &n
		}
		return nil
	}

	threadMap := map[string]string{}
	for _, t := range d.Threads {
		var parent *string
		if t.ParentThreadID != nil {
			if np, ok := threadMap[*t.ParentThreadID]; ok {
				parent = &np
			}
		}
		nt, err := db.CreateThread(ctx, store.NewThread{
			ProjectID: &projID, Title: t.Title, Provider: t.Provider,
			AgentType: t.AgentType, ParentThreadID: parent, ForkSeq: t.ForkSeq,
		})
		if err != nil {
			return err
		}
		threadMap[t.ID] = nt.ID
	}
	// Messages are grouped by thread in seq order on export; AppendMessageFull re-assigns seq in order.
	for _, m := range d.Messages {
		ntid, ok := threadMap[m.ThreadID]
		if !ok {
			continue
		}
		if _, err := db.AppendMessageFull(ctx, model.Message{
			ThreadID: ntid, Role: m.Role, Content: m.Content,
			ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID, ToolError: m.ToolError,
		}); err != nil {
			return err
		}
	}

	for _, inv := range d.Investigations {
		noid, ok := obsMap[inv.ObservationID]
		if !ok {
			continue // its observation didn't travel; skip rather than orphan
		}
		ninv, err := db.CreateInvestigation(ctx, model.Investigation{
			ProjectID: projID, ApplicationID: remapApp(inv.ApplicationID), ObservationID: noid,
			Title: inv.Title, Status: inv.Status,
		})
		if err != nil {
			return err
		}
		if inv.ThreadID != nil {
			if ntid, ok := threadMap[*inv.ThreadID]; ok {
				if err := db.SetInvestigationThread(ctx, ninv.ID, ntid); err != nil {
					return err
				}
			}
		}
	}

	for _, e := range d.Exchanges {
		ne, err := db.CreateExchange(ctx, model.HTTPExchange{
			ProjectID: projID, Name: e.Name, Origin: e.Origin, Method: e.Method, URL: e.URL,
			RequestHeaders: e.RequestHeaders, RequestBody: e.RequestBody, TLS: e.TLS,
		})
		if err != nil {
			return err
		}
		if e.Status != nil {
			// Egress names a runner that doesn't exist here; kept as an informational provenance label.
			if err := db.RecordResponse(ctx, ne.ID, *e.Status, e.ResponseHeaders, e.ResponseBody, intOr(e.DurationMS), e.Egress); err != nil {
				return err
			}
		}
	}

	for _, r := range d.Reports {
		naid, ok := artMap[r.ArtifactID]
		if !ok {
			continue // rendered blob didn't travel
		}
		if _, err := db.CreateReport(ctx, model.Report{
			ProjectID: projID, TemplateID: r.TemplateID, Format: r.Format, Title: r.Title, ArtifactID: naid,
		}); err != nil {
			return err
		}
	}

	for _, ci := range d.ContextItems {
		naid, ok := artMap[ci.ArtifactID]
		if !ok {
			continue
		}
		if _, err := db.CreateContextItem(ctx, model.ContextItem{
			ProjectID: projID, ApplicationID: remapApp(ci.ApplicationID), Type: ci.Type,
			Name: ci.Name, ArtifactID: naid, Tags: ci.Tags, Pinned: ci.Pinned,
		}); err != nil {
			return err
		}
	}

	// Methodology adoption references stable pack ids (built-ins/global saved packs); coverage is
	// per-item status. Coverage→observation evidence links are not restored (no bulk lister); the
	// per-item status that drives the coverage roll-up is.
	for _, mid := range d.Adopted {
		if err := db.AdoptMethodology(ctx, projID, mid); err != nil {
			return err
		}
	}
	for _, cov := range d.Coverage {
		if err := db.SetCoverage(ctx, projID, cov.ItemID, cov.Status, cov.Note); err != nil {
			return err
		}
	}

	if d.Engagement != nil {
		e := *d.Engagement
		e.ProjectID = projID
		for i := range e.Contacts {
			e.Contacts[i].ProjectID = projID
		}
		for i := range e.TestAccounts {
			e.TestAccounts[i].ProjectID = projID
		}
		if _, err := db.SetEngagement(ctx, e); err != nil {
			return err
		}
	}

	return nil
}

func intOr(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}
