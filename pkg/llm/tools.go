package llm

// Canonical, vendor-neutral tool types (ADR-0017). These live in the provider layer so a provider can
// receive tool definitions and return structured tool calls; the agent loop and each provider adapter
// (native or prompted) speak these same types.

// ParamType is the JSON type of a tool parameter — a pragmatic JSON-Schema subset.
type ParamType string

const (
	TypeString  ParamType = "string"
	TypeInteger ParamType = "integer"
	TypeNumber  ParamType = "number"
	TypeBoolean ParamType = "boolean"
	TypeEnum    ParamType = "enum"
	TypeArray   ParamType = "array"
	TypeObject  ParamType = "object"
)

// Param is one typed tool parameter.
type Param struct {
	Name        string
	Type        ParamType
	Required    bool
	Description string
	Enum        []string // allowed values when Type == TypeEnum
}

// ToolDef advertises a callable tool to the model.
type ToolDef struct {
	Name        string
	Description string
	Params      []Param
}

// ToolCall is a requested tool invocation. ID ties a native tool call to its result turn; it is empty
// on the prompted path (single call per turn).
type ToolCall struct {
	ID   string         `json:"id,omitempty"`
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// ToolAware reports whether a provider handles tools natively. A provider that does implements this and
// returns true; anything else is wrapped by the prompted adapter (EnsureToolAware).
type ToolAware interface {
	NativeTools() bool
}

// EnsureToolAware returns a provider that can handle req.Tools. If p already speaks tools natively it is
// returned as-is; otherwise it is wrapped in the prompted adapter.
func EnsureToolAware(p Provider) Provider {
	if ta, ok := p.(ToolAware); ok && ta.NativeTools() {
		return p
	}
	if _, ok := p.(*PromptedToolProvider); ok {
		return p
	}
	return &PromptedToolProvider{Raw: p}
}
