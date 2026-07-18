package methodology

// Coverage status values (mirrors model; kept as literals so this package stays dependency-free).
const (
	statusNotStarted    = "not_started"
	statusNotApplicable = "not_applicable"
	statusCovered       = "covered"
	statusInProgress    = "in_progress"
)

// State is a project's recorded status + note for an item.
type State struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// ItemCoverage is a catalog item paired with its project status.
type ItemCoverage struct {
	Item          Item   `json:"item"`
	Status        string `json:"status"`
	Note          string `json:"note,omitempty"`
	EvidenceCount int    `json:"evidence_count,omitempty"` // observations attached to this item (ADR-0015 P3b)
}

// PackCoverage is an adopted methodology and its items' statuses.
type PackCoverage struct {
	ID    string         `json:"id"`
	Title string         `json:"title"`
	Tech  string         `json:"tech"`
	Items []ItemCoverage `json:"items"`
}

// Summary is the coverage roll-up over adopted items.
type Summary struct {
	Total         int `json:"total"`
	Covered       int `json:"covered"`
	InProgress    int `json:"in_progress"`
	NotApplicable int `json:"not_applicable"`
	NotStarted    int `json:"not_started"`
	CoveredPct    int `json:"covered_pct"` // covered / (total - not_applicable), 0..100
}

// View is a project's full methodology coverage.
type View struct {
	Packs   []PackCoverage `json:"packs"`
	Summary Summary        `json:"summary"`
}

// BuildCoverage assembles a project's coverage view from the catalog, its adopted pack ids, and its
// recorded per-item states. Items without a recorded state default to not_started.
func BuildCoverage(reg *Registry, adopted []string, states map[string]State) View {
	var v View
	sum := &v.Summary
	for _, mid := range adopted {
		m, ok := reg.Get(mid)
		if !ok {
			continue
		}
		pc := PackCoverage{ID: m.ID, Title: m.Title, Tech: m.Tech}
		for _, it := range m.Items {
			st := states[it.ID]
			if st.Status == "" {
				st.Status = statusNotStarted
			}
			pc.Items = append(pc.Items, ItemCoverage{Item: it, Status: st.Status, Note: st.Note})
			sum.Total++
			switch st.Status {
			case statusCovered:
				sum.Covered++
			case statusInProgress:
				sum.InProgress++
			case statusNotApplicable:
				sum.NotApplicable++
			default:
				sum.NotStarted++
			}
		}
		v.Packs = append(v.Packs, pc)
	}
	if applicable := sum.Total - sum.NotApplicable; applicable > 0 {
		sum.CoveredPct = sum.Covered * 100 / applicable
	}
	return v
}
