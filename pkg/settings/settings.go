// Package settings defines the extensible, sectioned settings model (ADR-0021). A settings surface is a
// set of sections; a declarative section is a schema of typed fields that a generic UI renders — so core
// and extensions contribute settings the same way, without bespoke code. Values are namespaced keys
// ("<section>.<field>") persisted in the store's key/value settings table.
package settings

// Field types a declarative section may use.
const (
	TypeString = "string"
	TypeText   = "text"
	TypeBool   = "bool"
	TypeSelect = "select"
	TypeNumber = "number"
	TypeColor  = "color"
	TypeModel  = "model" // a catalog-backed model picker (ADR-0021 §3)
)

// Option is one choice for a select field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field is one typed setting within a section. Key is the fully-qualified "<section>.<field>".
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`
	Default     string   `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Options     []Option `json:"options,omitempty"`
}

// Section is a settings tab. Declarative sections carry Fields; a Custom section (rendered by a bespoke
// client component, e.g. Providers) carries no fields and is marked Custom. Source names the origin —
// empty for core, "ext:<id>" for a section contributed by an extension (ADR-0021 §5).
type Section struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Icon   string  `json:"icon,omitempty"`
	Order  int     `json:"order"`
	Custom bool    `json:"custom,omitempty"`
	Source string  `json:"source,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

// ValidType reports whether t is a field type the generic renderer understands.
func ValidType(t string) bool {
	switch t {
	case TypeString, TypeText, TypeBool, TypeSelect, TypeNumber, TypeColor, TypeModel:
		return true
	}
	return false
}

// Namespace prepares an extension's declared sections for inclusion in the settings surface. Every
// section ID and field key is prefixed with "ext.<extID>." so extension values can never collide with
// core settings (which never start with "ext."), and Source is stamped for provenance. Fields with an
// unknown type or empty key are dropped; a section left with no valid fields is dropped entirely, so a
// malformed manifest degrades gracefully instead of breaking the whole settings page.
func Namespace(extID string, secs []Section) []Section {
	if extID == "" {
		return nil
	}
	prefix := "ext." + extID + "."
	out := make([]Section, 0, len(secs))
	for _, sec := range secs {
		fields := make([]Field, 0, len(sec.Fields))
		for _, f := range sec.Fields {
			if f.Key == "" || !ValidType(f.Type) {
				continue
			}
			f.Key = prefix + f.Key
			fields = append(fields, f)
		}
		if len(fields) == 0 {
			continue
		}
		sec.ID = prefix + sec.ID
		sec.Fields = fields
		sec.Custom = false // extension sections are always declarative
		sec.Source = "ext:" + extID
		out = append(out, sec)
	}
	return out
}

// CoreSections returns the built-in declarative sections. Custom core sections (Providers, Approvals,
// Custom Agents) are declared client-side and composed by the settings shell; they are not listed here.
func CoreSections() []Section {
	return []Section{appearance}
}

var appearance = Section{
	ID:    "appearance",
	Title: "Appearance",
	Icon:  "🎨",
	Order: 10,
	Fields: []Field{
		{
			Key:         "appearance.theme",
			Label:       "Theme",
			Type:        TypeSelect,
			Default:     "dark",
			Description: "Light mode is often clearer for screenshots.",
			Options: []Option{
				{Value: "system", Label: "Match system"},
				{Value: "dark", Label: "Dark"},
				{Value: "light", Label: "Light"},
			},
		},
		{
			Key:         "appearance.accent",
			Label:       "Accent color",
			Type:        TypeColor,
			Default:     "#4aa8ff",
			Description: "The highlight color used across the app.",
		},
	},
}

// FieldByKey finds a field across the given sections (for validating a write).
func FieldByKey(sections []Section, key string) (Field, bool) {
	for _, s := range sections {
		for _, f := range s.Fields {
			if f.Key == key {
				return f, true
			}
		}
	}
	return Field{}, false
}
