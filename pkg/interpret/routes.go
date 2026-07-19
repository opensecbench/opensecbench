package interpret

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// RouteMediaType is the media type interpreted by Routes — the route-map capability's semgrep JSON output.
const RouteMediaType = "application/vnd.osb-routes+json"

// semgrep --json output: a top-level object with a results array. Each route rule's message interpolates
// the captured route path as a quoted string literal (e.g. `Flask route "/users/<id>"`), and its metadata
// carries method/framework. NOTE: semgrep OSS masks `metavars`/`lines` ("requires login"), so we read the
// path from the (client-interpolated, always-present) message, not from metavars.
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
	Message  string           `json:"message"`
	Metadata semgrepRouteMeta `json:"metadata"`
}

type semgrepRouteMeta struct {
	Method    string `json:"method"`
	Framework string `json:"framework"`
}

// Routes parses the route-map capability's semgrep JSON into declared routes (ADR-0033). The route path is
// the quoted string literal in each result's message (semgrep interpolated the $ROUTE metavariable there);
// method/framework come from the rule metadata; the match location is the handler file:line. Routes are
// returned without a project id — the engine sets it.
func Routes(data []byte) ([]model.Route, error) {
	var doc semgrepJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("interpret: parse route JSON: %w", err)
	}
	var routes []model.Route
	for _, r := range doc.Results {
		path := quotedRoute(r.Extra.Message)
		if path == "" {
			continue // no route literal in the message — nothing to record
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

// quotedRoute extracts the first quoted string literal from a route rule's message — the interpolated route
// path (double, single, or backtick quoted). Returns "" when the message has no quoted literal.
func quotedRoute(msg string) string {
	for i := 0; i < len(msg); i++ {
		q := msg[i]
		if q == '"' || q == '\'' || q == '`' {
			if end := strings.IndexByte(msg[i+1:], q); end >= 0 {
				return msg[i+1 : i+1+end]
			}
			return ""
		}
	}
	return ""
}
