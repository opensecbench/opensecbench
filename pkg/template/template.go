// Package template holds project archetypes that scaffold a new project (ADR-0003). A template
// seeds a default application and a suggested capability set. Methodology packs and starter
// playbooks are added when those subsystems land (P5); the shape here anticipates them.
package template

// Template is a project archetype.
type Template struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	DefaultApplication    string   `json:"default_application"`
	SuggestedCapabilities []string `json:"suggested_capabilities"`
}

var builtins = []Template{
	{
		ID:                    "blank",
		Name:                  "Blank",
		Description:           "An empty project you structure yourself.",
		DefaultApplication:    "",
		SuggestedCapabilities: []string{"source-inventory"},
	},
	{
		ID:                    "web-app",
		Name:                  "Web application",
		Description:           "A web application assessment (source + traffic).",
		DefaultApplication:    "web",
		SuggestedCapabilities: []string{"source-inventory", "semgrep"},
	},
	{
		ID:                    "rest-api",
		Name:                  "REST API + OIDC",
		Description:           "A REST API assessment with OIDC authentication.",
		DefaultApplication:    "api",
		SuggestedCapabilities: []string{"source-inventory", "semgrep"},
	},
	{
		ID:                    "graphql",
		Name:                  "GraphQL API",
		Description:           "A GraphQL API assessment.",
		DefaultApplication:    "graphql",
		SuggestedCapabilities: []string{"source-inventory", "semgrep"},
	},
	{
		ID:                    "mobile",
		Name:                  "Mobile application",
		Description:           "A mobile application assessment (client + backend).",
		DefaultApplication:    "mobile",
		SuggestedCapabilities: []string{"source-inventory", "semgrep"},
	},
	{
		ID:                    "cloud-aws",
		Name:                  "Cloud / AWS",
		Description:           "A cloud environment assessment on AWS.",
		DefaultApplication:    "cloud",
		SuggestedCapabilities: []string{"source-inventory"},
	},
}

// BuiltIns returns the built-in project templates.
func BuiltIns() []Template { return builtins }

// Get returns a template by id.
func Get(id string) (Template, bool) {
	for _, t := range builtins {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}
