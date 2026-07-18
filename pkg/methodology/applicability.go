package methodology

import (
	"sort"
	"strings"
)

// Suggestion recommends adopting a methodology pack based on knowledge-base signals.
type Suggestion struct {
	MethodologyID string `json:"methodology_id"`
	Title         string `json:"title"`
	Reason        string `json:"reason"` // the matched keyword
}

// Suggest returns packs (not already adopted) whose keywords appear in the knowledge-base text,
// so a target's KB drives methodology applicability (ADR-0009/ADR-0010 tie-in).
func Suggest(catalog *Registry, kbText string, adopted []string) []Suggestion {
	text := strings.ToLower(kbText)
	isAdopted := map[string]bool{}
	for _, a := range adopted {
		isAdopted[a] = true
	}
	var out []Suggestion
	for _, m := range catalog.All() {
		if isAdopted[m.ID] {
			continue
		}
		for _, kw := range m.Keywords {
			if kw != "" && strings.Contains(text, strings.ToLower(kw)) {
				out = append(out, Suggestion{MethodologyID: m.ID, Title: m.Title, Reason: "knowledge base mentions “" + kw + "”"})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MethodologyID < out[j].MethodologyID })
	return out
}
