package extension

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/settings"
)

// FormatVersion is the extension manifest schema version.
const FormatVersion = 1

// ManifestFile and SignatureFile are the well-known filenames inside a package directory.
const (
	ManifestFile  = "extension.json"
	SignatureFile = "extension.sig"
)

// ReportDef is an extension-provided report template (raw MD/HTML template strings).
type ReportDef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	MD    string `json:"md"`
	HTML  string `json:"html"`
}

// Manifest is a package's extension.json.
type Manifest struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	Version       string                    `json:"version"`
	Publisher     string                    `json:"publisher"`
	Capabilities  []ContainerCapability     `json:"capabilities,omitempty"`
	Methodologies []methodology.Methodology `json:"methodologies,omitempty"`
	Reports       []ReportDef               `json:"reports,omitempty"`
	Settings      []settings.Section        `json:"settings,omitempty"` // declarative settings sections (ADR-0021 §5)
}

// Loaded is a verified, loaded package.
type Loaded struct {
	Manifest Manifest
	Path     string
	Digest   string // hex sha256 of the canonical manifest
	Trusted  bool   // signature verified against a trusted publisher key
}

// CapabilityList returns the package's capabilities as capability.Capability values.
func (l Loaded) CapabilityList() []capability.Capability {
	out := make([]capability.Capability, 0, len(l.Manifest.Capabilities))
	for _, c := range l.Manifest.Capabilities {
		out = append(out, c)
	}
	return out
}

// Digest computes the canonical sha256 (hex) of a manifest, excluding any signature.
func Digest(m Manifest) (string, []byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum), sum[:], nil
}

// Load reads and verifies a package directory. Unsigned or untrusted packages are refused unless
// allowUnsigned is set (ADR-0013).
func Load(dir string, trust *TrustStore, allowUnsigned bool) (Loaded, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Loaded{}, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Loaded{}, fmt.Errorf("extension: bad manifest in %s: %w", dir, err)
	}
	if m.ID == "" || m.Version == "" {
		return Loaded{}, fmt.Errorf("extension: %s manifest missing id/version", dir)
	}
	hexDigest, digest, err := Digest(m)
	if err != nil {
		return Loaded{}, err
	}

	trusted := false
	if sigRaw, err := os.ReadFile(filepath.Join(dir, SignatureFile)); err == nil {
		sig, derr := base64.StdEncoding.DecodeString(string(trimSpace(sigRaw)))
		if derr == nil && trust != nil {
			if pub := trust.Key(m.Publisher); pub != nil && ed25519.Verify(pub, digest, sig) {
				trusted = true
			}
		}
	}
	if !trusted && !allowUnsigned {
		return Loaded{}, fmt.Errorf("extension %q: not signed by a trusted publisher (%q); trust the key or allow unsigned", m.ID, m.Publisher)
	}

	return Loaded{Manifest: m, Path: dir, Digest: hexDigest, Trusted: trusted}, nil
}

// LoadDir loads every package under root/*/ (each a directory with an extension.json), skipping
// entries that fail to load. It returns the loaded packages and the per-directory errors.
func LoadDir(root string, trust *TrustStore, allowUnsigned bool) ([]Loaded, map[string]error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil // no extensions dir → nothing to load
	}
	var out []Loaded
	errs := map[string]error{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, statErr := os.Stat(filepath.Join(dir, ManifestFile)); statErr != nil {
			continue
		}
		l, lerr := Load(dir, trust, allowUnsigned)
		if lerr != nil {
			errs[dir] = lerr
			continue
		}
		out = append(out, l)
	}
	return out, errs
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	return b
}
