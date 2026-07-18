package bundle

import (
	"bytes"
	"context"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Import decrypts a bundle and recreates its graph in db/blobs, remapping IDs (re-import safe) while
// preserving evidence content hashes. It returns the new project id.
func Import(ctx context.Context, db *store.DB, blobs *cas.Store, data []byte, passphrase string) (string, error) {
	d, err := open(data, passphrase)
	if err != nil {
		return "", err
	}
	if d.Version > FormatVersion {
		return "", fmt.Errorf("bundle: version %d is newer than supported (%d)", d.Version, FormatVersion)
	}

	// Targets → new ids, then the project referencing them.
	targetMap := map[string]string{}
	var newTargetIDs []string
	for _, t := range d.Targets {
		nt, err := db.CreateTarget(ctx, t.Name, t.Description, nil)
		if err != nil {
			return "", err
		}
		targetMap[t.ID] = nt.ID
		newTargetIDs = append(newTargetIDs, nt.ID)
	}

	proj, err := db.CreateProject(ctx, store.NewProject{Name: d.Project.Name, TargetIDs: newTargetIDs})
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
			ArtifactID: aid, Origin: o.Origin, ReviewState: o.ReviewState,
			Title: o.Title, Detail: o.Detail, Severity: o.Severity, RuleID: o.RuleID, Location: o.Location,
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
		if _, err := db.CreateKBEntry(ctx, model.KBEntry{
			TargetID: tid, Kind: k.Kind, Scope: k.Scope, Title: k.Title, Body: k.Body, Tags: k.Tags,
			Sensitivity: k.Sensitivity, Origin: k.Origin, ReviewState: k.ReviewState, SourceRef: k.SourceRef,
		}); err != nil {
			return "", err
		}
	}

	return proj.ID, nil
}
