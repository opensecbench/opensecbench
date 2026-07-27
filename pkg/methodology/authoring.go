package methodology

import (
	"fmt"
	"regexp"
	"strings"
)

// Authoring helpers for user-defined methodology packs (ADR-0055). Built-in packs are code-defined; these
// let the API validate and normalize an operator-authored pack before it's persisted and registered.

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns free text into a stable, url-safe id fragment (e.g. "Broken Access Control" → "broken-access-control").
func Slug(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(s, "-")
}

// Normalize fills in derived fields and trims noise so a pack authored in the editor is well-formed before
// validation: a pack id from the title, sensible defaults for tech/version, and pack-scoped item ids. Item
// ids are kept if already supplied (so edits stay stable) and otherwise derived as "<pack>/<item-slug>".
func Normalize(m *Methodology) {
	m.Title = strings.TrimSpace(m.Title)
	m.ID = strings.TrimSpace(m.ID)
	if m.ID == "" {
		m.ID = Slug(m.Title)
	}
	if m.Tech = strings.TrimSpace(m.Tech); m.Tech == "" {
		m.Tech = "custom"
	}
	if m.Version = strings.TrimSpace(m.Version); m.Version == "" {
		m.Version = "1.0.0"
	}
	m.Keywords = cleanStrings(m.Keywords)
	for i := range m.Items {
		it := &m.Items[i]
		it.Title = strings.TrimSpace(it.Title)
		it.Objective = strings.TrimSpace(it.Objective)
		it.Procedure = strings.TrimSpace(it.Procedure)
		if it.ID = strings.TrimSpace(it.ID); it.ID == "" {
			it.ID = m.ID + "/" + Slug(it.Title)
		}
		it.Standards = cleanStrings(it.Standards)
		it.SuggestedCapabilities = cleanStrings(it.SuggestedCapabilities)
	}
}

// Validate checks a normalized pack is self-consistent: it has an id, a title, at least one titled item, and
// unique item ids within the pack. Cross-pack id collisions are the registry's concern and are checked by the
// caller against the live registry.
func Validate(m Methodology) error {
	if m.ID == "" {
		return fmt.Errorf("a methodology needs a title")
	}
	if m.Title == "" {
		return fmt.Errorf("a methodology needs a title")
	}
	if len(m.Items) == 0 {
		return fmt.Errorf("a methodology needs at least one item")
	}
	seen := map[string]bool{}
	for _, it := range m.Items {
		if it.Title == "" {
			return fmt.Errorf("every item needs a title")
		}
		if seen[it.ID] {
			return fmt.Errorf("duplicate item id %q", it.ID)
		}
		seen[it.ID] = true
	}
	return nil
}

// CheckItemCollisions ensures a pack's item ids don't clash with items in any OTHER registered pack. Item ids
// are globally unique so the coverage store and Registry.Item lookup stay unambiguous (ADR-0055). Shared by
// the HTTP handlers and the agent authoring tool so both enforce the same rule.
func CheckItemCollisions(r *Registry, m Methodology) error {
	taken := map[string]string{} // itemID -> owning pack id
	for _, other := range r.All() {
		if other.ID == m.ID {
			continue
		}
		for _, it := range other.Items {
			taken[it.ID] = other.ID
		}
	}
	for _, it := range m.Items {
		if owner, ok := taken[it.ID]; ok {
			return fmt.Errorf("item id %s is already used by pack %s", it.ID, owner)
		}
	}
	return nil
}

// cleanStrings trims each element and drops empties, returning nil for an all-empty slice so it omits from JSON.
func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
