package scope

import (
	"net"
	"regexp"
	"strings"
)

// ScopeSeed is an extracted scope entry from a pasted scope document.
type ScopeSeed struct {
	Kind        string `json:"kind"`
	Value       string `json:"value"`
	Disposition string `json:"disposition"`
}

// ParseResult holds extracted scope entries and complexity signals.
type ParseResult struct {
	Seeds   []ScopeSeed `json:"seeds"`
	Complex bool        `json:"complex"`
	Reason  string      `json:"reason,omitempty"`
}

var (
	urlRe    = regexp.MustCompile(`https?://[^\s<>"'\)\],]+`)
	cidrRe   = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}/\d{1,2})\b`)
	ipRe     = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b`)
	domainRe = regexp.MustCompile(`(?i)\b(\*\.)?([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}\b`)

	inScopeHeading  = regexp.MustCompile(`(?i)^#{0,6}\s*(?:in[\s-]?scope|scope|targets?|included)\s*[:]*\s*$`)
	outScopeHeading = regexp.MustCompile(`(?i)^#{0,6}\s*(?:out[\s-]?of[\s-]?scope|excluded?|exclusions?|not[\s-]?in[\s-]?scope)\s*[:]*\s*$`)

	tableRowRe       = regexp.MustCompile(`^\|.*\|`)
	techniqueWordsRe = regexp.MustCompile(`(?i)\b(brute.?force|denial.?of.?service|dos|social.?engineering|destructive|automated.?exploit|phishing)\b`)
	ambiguousRe      = regexp.MustCompile(`(?i)\b(except|unless|only if|provided that|not including|excluding)\b`)
)

// ParseScopeDocument extracts scope entries from raw text. It returns extracted seeds
// and a complexity flag indicating whether agent interpretation is recommended.
func ParseScopeDocument(text string) ParseResult {
	lines := strings.Split(text, "\n")

	type section struct {
		disposition string
		lines       []string
	}

	var sections []section
	current := section{disposition: Allow}

	hasTable := false
	hasTechniqueLanguage := false
	hasAmbiguous := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if inScopeHeading.MatchString(trimmed) {
			if len(current.lines) > 0 {
				sections = append(sections, current)
			}
			current = section{disposition: Allow}
			continue
		}
		if outScopeHeading.MatchString(trimmed) {
			if len(current.lines) > 0 {
				sections = append(sections, current)
			}
			current = section{disposition: Deny}
			continue
		}
		if tableRowRe.MatchString(trimmed) {
			hasTable = true
		}
		if techniqueWordsRe.MatchString(trimmed) {
			hasTechniqueLanguage = true
		}
		if ambiguousRe.MatchString(trimmed) {
			hasAmbiguous = true
		}
		current.lines = append(current.lines, trimmed)
	}
	if len(current.lines) > 0 {
		sections = append(sections, current)
	}

	seen := make(map[string]bool)
	var seeds []ScopeSeed

	for _, sec := range sections {
		for _, line := range sec.lines {
			for _, seed := range extractFromLine(line, sec.disposition) {
				key := seed.Kind + "|" + seed.Value + "|" + seed.Disposition
				if seen[key] {
					continue
				}
				seen[key] = true
				seeds = append(seeds, seed)
			}
		}
	}

	result := ParseResult{Seeds: seeds}

	switch {
	case hasTable && hasAmbiguous:
		result.Complex = true
		result.Reason = "table formatting with ambiguous exclusion language"
	case hasAmbiguous:
		result.Complex = true
		result.Reason = "ambiguous exclusion language detected"
	case hasTechniqueLanguage:
		result.Complex = true
		result.Reason = "technique restrictions detected in scope text"
	case hasTable && len(seeds) == 0:
		result.Complex = true
		result.Reason = "table formatting but no entries extracted"
	}

	return result
}

func extractFromLine(line, disposition string) []ScopeSeed {
	line = strings.TrimLeft(line, "-*•·→▸► \t")
	// Skip markdown table separators.
	if strings.TrimLeft(line, "|-: ") == "" {
		return nil
	}

	var seeds []ScopeSeed

	for _, u := range urlRe.FindAllString(line, -1) {
		u = strings.TrimRight(u, ".,;:")
		seeds = append(seeds, ScopeSeed{Kind: KindURL, Value: u, Disposition: disposition})
	}

	for _, c := range cidrRe.FindAllString(line, -1) {
		if _, _, err := net.ParseCIDR(c); err == nil {
			seeds = append(seeds, ScopeSeed{Kind: KindCIDR, Value: c, Disposition: disposition})
		}
	}

	cidrs := cidrRe.FindAllStringIndex(line, -1)
	for _, loc := range ipRe.FindAllStringIndex(line, -1) {
		ip := line[loc[0]:loc[1]]
		if insideCIDR(loc, cidrs) {
			continue
		}
		parsed := net.ParseIP(ip)
		if parsed == nil {
			continue
		}
		if alreadyInURL(seeds, ip) {
			continue
		}
		seeds = append(seeds, ScopeSeed{Kind: KindHost, Value: ip, Disposition: disposition})
	}

	for _, d := range domainRe.FindAllString(line, -1) {
		if alreadyInURL(seeds, d) {
			continue
		}
		d = strings.TrimPrefix(d, "*.")
		seeds = append(seeds, ScopeSeed{Kind: KindDomain, Value: strings.ToLower(d), Disposition: disposition})
	}

	return seeds
}

func alreadyInURL(seeds []ScopeSeed, fragment string) bool {
	for _, s := range seeds {
		if s.Kind == KindURL && strings.Contains(s.Value, fragment) {
			return true
		}
	}
	return false
}

func insideCIDR(ipLoc []int, cidrLocs [][]int) bool {
	for _, cl := range cidrLocs {
		if ipLoc[0] >= cl[0] && ipLoc[1] <= cl[1] {
			return true
		}
	}
	return false
}
