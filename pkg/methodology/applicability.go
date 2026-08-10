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

// SuggestForAsset returns packs whose AppliesTo or AppliesTags match the given asset (ADR-0071).
// Packs already adopted are excluded. The result is advisory -- suggested, not required.
func SuggestForAsset(catalog *Registry, assetType string, assetTags []string, adopted []string) []Suggestion {
	isAdopted := map[string]bool{}
	for _, a := range adopted {
		isAdopted[a] = true
	}
	tagSet := map[string]bool{}
	for _, t := range assetTags {
		tagSet[strings.ToLower(t)] = true
	}
	var out []Suggestion
	for _, m := range catalog.All() {
		if isAdopted[m.ID] {
			continue
		}
		if reason := matchAsset(m, assetType, tagSet); reason != "" {
			out = append(out, Suggestion{MethodologyID: m.ID, Title: m.Title, Reason: reason})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MethodologyID < out[j].MethodologyID })
	return out
}

func matchAsset(m Methodology, assetType string, tagSet map[string]bool) string {
	for _, at := range m.AppliesTo {
		if at == assetType {
			return "applies to " + assetType + " assets"
		}
	}
	for _, t := range m.AppliesTags {
		if tagSet[strings.ToLower(t)] {
			return "asset tagged “" + t + "”"
		}
	}
	return ""
}
