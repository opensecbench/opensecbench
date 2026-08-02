package model

import "sort"

// ClassificationLevel is one tier in the user-configurable data-classification scale. The scale is shared
// by asset sensitivity and destination data clearance (ADR-0011/0020): content tagged at a level may reach
// a destination cleared for that level or any MORE-sensitive one. Rank orders the scale (higher = more
// sensitive). Builtin levels (open_source/internal/private) are permanent — renamable and reorderable, but
// never deleted, because code paths, defaults, and existing data reference their ids.
type ClassificationLevel struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Rank    int    `json:"rank"`
	Builtin bool   `json:"builtin"`
	Color   string `json:"color,omitempty"`
}

// Scale is an ordered, read-only view of the classification levels used for egress decisions. Build it once
// per use with NewScale from the stored levels; it is safe to copy by value.
type Scale struct {
	byID   map[string]ClassificationLevel
	sorted []ClassificationLevel // ascending rank
}

// NewScale builds a Scale from levels in any order.
func NewScale(levels []ClassificationLevel) Scale {
	byID := make(map[string]ClassificationLevel, len(levels))
	for _, l := range levels {
		byID[l.ID] = l
	}
	sorted := append([]ClassificationLevel(nil), levels...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Rank < sorted[j].Rank })
	return Scale{byID: byID, sorted: sorted}
}

// Levels returns the levels ordered least-sensitive first.
func (s Scale) Levels() []ClassificationLevel { return s.sorted }

// Has reports whether id is a known level.
func (s Scale) Has(id string) bool { _, ok := s.byID[id]; return ok }

// Label returns a level's display label, or the id itself when unknown.
func (s Scale) Label(id string) string {
	if l, ok := s.byID[id]; ok && l.Label != "" {
		return l.Label
	}
	if id == "" {
		return ClassificationLabelFallback("")
	}
	return id
}

// Min returns the least-sensitive level id — the least-privilege default for a new connection's clearance
// and the floor a restricted engagement clamps to. Falls back to open_source when the scale is empty.
func (s Scale) Min() string {
	if len(s.sorted) > 0 {
		return s.sorted[0].ID
	}
	return SensitivityOpenSource
}

// Max returns the most-sensitive level id — what "private-by-default" content requires a destination to be
// cleared for. Falls back to private when the scale is empty.
func (s Scale) Max() string {
	if len(s.sorted) > 0 {
		return s.sorted[len(s.sorted)-1].ID
	}
	return SensitivityPrivate
}

// Allows reports whether a destination cleared for `clearance` may receive content tagged `sensitivity`.
// Fail-safe: unknown content sensitivity is blocked; unknown clearance permits only the least-sensitive tier.
func (s Scale) Allows(clearance, sensitivity string) bool {
	sl, ok := s.byID[sensitivity]
	if !ok {
		return false // can't classify the content ⇒ don't send it
	}
	cl, ok := s.byID[clearance]
	if !ok {
		return sl.Rank <= s.minRank() // unknown clearance ⇒ only the least-sensitive level
	}
	return sl.Rank <= cl.Rank
}

// MinClearance returns the less-permissive (lower-rank) of two clearance ids. An empty override means
// "inherit" and does not tighten; an unknown id is treated as least privilege (most conservative).
func (s Scale) MinClearance(base, override string) string {
	if override == "" {
		return base
	}
	if base == "" {
		return override
	}
	if s.rank(override) < s.rank(base) {
		return override
	}
	return base
}

func (s Scale) rank(id string) int {
	if l, ok := s.byID[id]; ok {
		return l.Rank
	}
	return s.minRank()
}

func (s Scale) minRank() int {
	if len(s.sorted) > 0 {
		return s.sorted[0].Rank
	}
	return 0
}

// ClassificationLabelFallback renders a built-in tier when no scale is available (empty label). Kept so
// error text still reads sensibly even before the registry loads.
func ClassificationLabelFallback(id string) string {
	switch id {
	case SensitivityPrivate:
		return "private (corporate)"
	case SensitivityInternal:
		return "internal"
	default:
		return "open-source only"
	}
}
