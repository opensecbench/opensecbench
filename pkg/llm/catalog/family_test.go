package catalog

import "testing"

func TestFamily(t *testing.T) {
	cases := []struct {
		provider, id, want string
	}{
		{"anthropic", "claude-sonnet-5", "sonnet"},              // exact catalog hit
		{"anthropic", "claude-sonnet-4-5", "sonnet"},            // not in catalog, substring
		{"", "anthropic.claude-opus-4-5-20251101-v1:0", "opus"}, // Bedrock gateway id: strip vendor prefix
		{"", "meta.llama-3.3-70b-instruct", "llama"},            // Bedrock, different family
		{"grok", "grok-4-fast", "grok"},
		{"openai", "gpt-5.6-terra", "gpt-5"},
		{"deepseek", "deepseek-v4-flash", "deepseek"},
		{"openai", "text-embedding-3-large", ""}, // no family token → unenriched but still lists
	}
	for _, c := range cases {
		if got := Family(c.provider, c.id); got != c.want {
			t.Errorf("Family(%q, %q) = %q, want %q", c.provider, c.id, got, c.want)
		}
	}
}

func TestMetaForFamily(t *testing.T) {
	m, ok := MetaForFamily("sonnet")
	if !ok {
		t.Fatal("MetaForFamily(sonnet) not found")
	}
	if m.ContextWindow == 0 || m.OutputPerMTok == 0 {
		t.Errorf("sonnet family metadata missing context/price: %+v", m)
	}
	if _, ok := MetaForFamily("nonexistent"); ok {
		t.Error("MetaForFamily(nonexistent) should be false")
	}
}
