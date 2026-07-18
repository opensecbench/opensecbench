package bundle

import (
	"context"
	"io"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Export gathers a project's shareable assessment graph (findings + evidence + KB) and returns an
// encrypted bundle sealed with passphrase.
func Export(ctx context.Context, db *store.DB, blobs *cas.Store, projectID, passphrase string) ([]byte, error) {
	proj, err := db.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	d := &Data{Version: FormatVersion, Project: proj, Blobs: map[string][]byte{}}

	for _, tid := range proj.TargetIDs {
		if t, err := db.GetTarget(ctx, tid); err == nil {
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

	all, err := db.ListFindings(ctx)
	if err != nil {
		return nil, err
	}
	obsSeen := map[string]bool{}
	artSeen := map[string]bool{}
	for _, f := range all {
		if f.ApplicationID == nil || !inProject[*f.ApplicationID] {
			continue
		}
		full, err := db.GetFinding(ctx, f.ID) // populates ObservationIDs
		if err != nil {
			return nil, err
		}
		d.Findings = append(d.Findings, full)

		for _, oid := range full.ObservationIDs {
			if obsSeen[oid] {
				continue
			}
			obsSeen[oid] = true
			o, err := db.GetObservation(ctx, oid)
			if err != nil {
				continue
			}
			d.Observations = append(d.Observations, o)
			if o.ArtifactID == nil || artSeen[*o.ArtifactID] {
				continue
			}
			artSeen[*o.ArtifactID] = true
			art, err := db.GetArtifact(ctx, *o.ArtifactID)
			if err != nil {
				continue
			}
			d.Artifacts = append(d.Artifacts, art)
			if b, err := readBlob(blobs, art.SHA256); err == nil {
				d.Blobs[art.SHA256] = b
			}
		}
	}

	if d.KB, err = db.ListKBByProject(ctx, projectID); err != nil {
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
