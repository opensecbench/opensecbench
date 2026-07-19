package interpret

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// RouteMediaType is the media type interpreted by Routes — the route-map capability's semgrep JSON output.
const RouteMediaType = "application/vnd.osb-routes+json"

// semgrep --json output: a top-level object with a results array. Route rules capture the path in a $ROUTE
// metavariable and carry method/framework in their metadata.
type semgrepJSON struct {
	Results []semgrepResult `json:"results"`
}

type semgrepResult struct {
	CheckID string       `json:"check_id"`
	Path    string       `json:"path"`
	Start   semgrepPos   `json:"start"`
	Extra   semgrepExtra `json:"extra"`
}

type semgrepPos struct {
	Line int `json:"line"`
}

type semgrepExtra struct {
	Metavars map[string]semgrepMetavar `json:"metavars"`
	Metadata semgrepRouteMeta          `json:"metadata"`
}

type semgrepMetavar struct {
	AbstractContent string `json:"abstract_content"`
}

type semgrepRouteMeta struct {
	Method    string `json:"method"`
	Framework string `json:"framework"`
}

// Routes parses the route-map capability's semgrep JSON into declared routes (ADR-0033). Each result's
// $ROUTE metavariable is the path (quotes stripped); method/framework come from the rule metadata; the
// match location is the handler file:line. Routes are returned without a project id — the engine sets it.
func Routes(data []byte) ([]model.Route, error) {
	var doc semgrepJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("interpret: parse route JSON: %w", err)
	}
	var routes []model.Route
	for _, r := range doc.Results {
		path := ""
		if mv, ok := r.Extra.Metavars["$ROUTE"]; ok {
			path = unquoteRoute(mv.AbstractContent)
		}
		if path == "" {
			continue // no captured path — nothing to record
		}
		routes = append(routes, model.Route{
			Method:      strings.ToUpper(r.Extra.Metadata.Method),
			Path:        path,
			HandlerFile: r.Path,
			HandlerLine: r.Start.Line,
			Framework:   r.Extra.Metadata.Framework,
			Source:      "route-map",
		})
	}
	return routes, nil
}

// unquoteRoute strips a single layer of surrounding quotes (semgrep reports string literals with them).
func unquoteRoute(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '`' && s[len(s)-1] == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
