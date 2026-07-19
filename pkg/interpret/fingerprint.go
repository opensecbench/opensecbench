package interpret

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Fingerprint returns a stable content hash for an observation, used to dedup the same finding across
// re-scans (ADR-0029). It hashes only content-identifying fields — origin, rule, location, detail — so a
// re-run that reproduces the same finding yields the same fingerprint and the engine can skip it. Severity
// and attributes are deliberately excluded: a finding whose CVSS score or verified flag shifts between runs
// is still the same finding (dedup wins over refresh; refreshing a deduped observation is future work).
func Fingerprint(o model.Observation) string {
	origin := o.Origin
	if origin == "" {
		origin = model.OriginTool
	}
	h := sha256.New()
	// Length-prefix-free but delimiter-separated with a NUL, which cannot appear in these text fields.
	for _, part := range []string{origin, o.RuleID, o.Location, o.Detail} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
