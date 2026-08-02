package api

import (
	"context"
	"regexp"
	"sync"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// compiledRule is a match/replace rule ready to apply.
type compiledRule struct {
	target  string
	re      *regexp.Regexp
	replace string
}

// ruleEngine implements proxy.Processor from a project's enabled match/replace rules. It is rebuilt
// from the store whenever rules change, so a running proxy always applies the current set.
type ruleEngine struct {
	mu    sync.RWMutex
	rules []compiledRule
}

func (e *ruleEngine) set(rules []compiledRule) {
	e.mu.Lock()
	e.rules = rules
	e.mu.Unlock()
}

func (e *ruleEngine) NeedsResponseBody() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, r := range e.rules {
		if r.target == model.RuleTargetResponseBody || r.target == model.RuleTargetResponseHeader {
			return true
		}
	}
	return false
}

func (e *ruleEngine) ProcessRequest(method, url, headers, body string) (string, string, string, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, r := range e.rules {
		switch r.target {
		case model.RuleTargetURL:
			url = r.re.ReplaceAllString(url, r.replace)
		case model.RuleTargetRequestHeader:
			headers = r.re.ReplaceAllString(headers, r.replace)
		case model.RuleTargetRequestBody:
			body = r.re.ReplaceAllString(body, r.replace)
		}
	}
	return method, url, headers, body
}

func (e *ruleEngine) ProcessResponse(status int, headers, body string) (int, string, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, r := range e.rules {
		switch r.target {
		case model.RuleTargetResponseHeader:
			headers = r.re.ReplaceAllString(headers, r.replace)
		case model.RuleTargetResponseBody:
			body = r.re.ReplaceAllString(body, r.replace)
		}
	}
	return status, headers, body
}

// ruleEngineFor returns the project's engine (creating it) loaded with its current rules.
func (s *Server) ruleEngineFor(projectID string) *ruleEngine {
	s.ruleMu.Lock()
	eng := s.matchReplace[projectID]
	if eng == nil {
		eng = &ruleEngine{}
		s.matchReplace[projectID] = eng
	}
	s.ruleMu.Unlock()
	s.rebuildRules(projectID)
	return eng
}

// rebuildRules recompiles a project's enabled rules onto its engine (invalid patterns are skipped).
func (s *Server) rebuildRules(projectID string) {
	s.ruleMu.Lock()
	eng := s.matchReplace[projectID]
	s.ruleMu.Unlock()
	if eng == nil {
		return
	}
	rules, err := s.pdbID(projectID).ListProxyRules(context.Background(), projectID)
	if err != nil {
		return
	}
	compiled := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		re, err := regexp.Compile(r.Match)
		if err != nil {
			continue
		}
		compiled = append(compiled, compiledRule{target: r.Target, re: re, replace: r.Replace})
	}
	eng.set(compiled)
}
