package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// manifestHead is the subset of a package manifest the hub needs for an index entry.
type manifestHead struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
}

// Publish archives a package directory into a hub directory (creating index.json + packages/) and
// returns the index entry. publisherKeyB64 is the publisher's base64 ed25519 public key (shown to
// installers, not auto-trusted).
func Publish(hubDir, pkgDir, publisherKeyB64, description string, tags []string) (PackageEntry, error) {
	raw, err := os.ReadFile(filepath.Join(pkgDir, "extension.json"))
	if err != nil {
		return PackageEntry{}, err
	}
	var m manifestHead
	if err := json.Unmarshal(raw, &m); err != nil {
		return PackageEntry{}, fmt.Errorf("hub: bad manifest: %w", err)
	}
	if m.ID == "" || m.Version == "" {
		return PackageEntry{}, fmt.Errorf("hub: manifest missing id/version")
	}

	archive, digest, err := ArchiveDir(pkgDir)
	if err != nil {
		return PackageEntry{}, err
	}
	rel := filepath.Join("packages", m.ID+"-"+m.Version+".tgz")
	if err := os.MkdirAll(filepath.Join(hubDir, "packages"), 0o755); err != nil {
		return PackageEntry{}, err
	}
	if err := os.WriteFile(filepath.Join(hubDir, rel), archive, 0o644); err != nil {
		return PackageEntry{}, err
	}

	entry := PackageEntry{
		ID: m.ID, Name: m.Name, Version: m.Version, Publisher: m.Publisher,
		Description: description, Tags: tags, Digest: digest,
		Archive: filepath.ToSlash(rel), PublisherKey: publisherKeyB64,
	}

	idx := Index{}
	if b, err := os.ReadFile(filepath.Join(hubDir, IndexFile)); err == nil {
		_ = json.Unmarshal(b, &idx)
	}
	replaced := false
	for i, p := range idx.Packages {
		if p.ID == entry.ID {
			idx.Packages[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		idx.Packages = append(idx.Packages, entry)
	}
	out, err := marshalIndex(idx)
	if err != nil {
		return PackageEntry{}, err
	}
	if err := os.WriteFile(filepath.Join(hubDir, IndexFile), out, 0o644); err != nil {
		return PackageEntry{}, err
	}
	return entry, nil
}
