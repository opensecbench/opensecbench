// Package dlp inspects outbound content for data that must not leave (ADR-0011): known vault secret
// values, planted canary tokens, and high-signal patterns (cloud keys, private keys, JWTs). Vault
// secrets and canaries are blocked on external egress; patterns are alerted.
package dlp

import (
	"regexp"
	"strings"
)

// Hit kinds and actions.
const (
	KindSecret  = "secret"
	KindCanary  = "canary"
	KindPattern = "pattern"

	ActionBlock = "block"
	ActionAlert = "alert"
)

// Hit is one DLP match found in inspected content.
type Hit struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`  // secret/canary name, or pattern name
	Action string `json:"action"` // block | alert
}

type pattern struct {
	name string
	re   *regexp.Regexp
}

// patterns are high-signal secret shapes. They alert (heuristic) rather than block.
var patterns = []pattern{
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |)PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// Scanner inspects content against a set of secret values, canary tokens, and built-in patterns.
type Scanner struct {
	secrets  map[string]string // value -> name
	canaries map[string]string // value -> label
}

// New builds a scanner. secrets maps a secret value to its name; canaries maps a canary token to its
// label.
func New(secrets, canaries map[string]string) *Scanner {
	return &Scanner{secrets: secrets, canaries: canaries}
}

// Inspect returns every DLP hit in text (deduplicated by kind+label).
func (s *Scanner) Inspect(text string) []Hit {
	var hits []Hit
	seen := map[string]bool{}
	add := func(kind, label, action string) {
		k := kind + ":" + label
		if seen[k] {
			return
		}
		seen[k] = true
		hits = append(hits, Hit{Kind: kind, Label: label, Action: action})
	}

	for val, name := range s.secrets {
		if val != "" && strings.Contains(text, val) {
			add(KindSecret, name, ActionBlock)
		}
	}
	for val, label := range s.canaries {
		if val != "" && strings.Contains(text, val) {
			add(KindCanary, label, ActionBlock)
		}
	}
	for _, p := range patterns {
		if p.re.MatchString(text) {
			add(KindPattern, p.name, ActionAlert)
		}
	}
	return hits
}
