package scope

import (
	"testing"
)

func TestParseScopeDocumentSimple(t *testing.T) {
	text := `
In Scope
- *.example.com
- api.example.com
- 10.0.0.0/24
- 192.168.1.1
- https://api.example.com/v2/*

Out of Scope
- staging.example.com
- 10.0.1.0/24
`
	result := ParseScopeDocument(text)
	if len(result.Seeds) == 0 {
		t.Fatal("expected seeds")
	}

	allows := 0
	denies := 0
	for _, s := range result.Seeds {
		if s.Disposition == Allow {
			allows++
		} else {
			denies++
		}
	}
	if allows < 3 {
		t.Errorf("expected at least 3 allows, got %d", allows)
	}
	if denies < 1 {
		t.Errorf("expected at least 1 deny, got %d", denies)
	}
	if result.Complex {
		t.Errorf("simple doc should not be flagged complex: %s", result.Reason)
	}
}

func TestParseScopeDocumentURLs(t *testing.T) {
	text := `https://api.example.com/v2/users
https://admin.example.com`

	result := ParseScopeDocument(text)
	if len(result.Seeds) != 2 {
		t.Fatalf("expected 2 seeds, got %d: %+v", len(result.Seeds), result.Seeds)
	}
	for _, s := range result.Seeds {
		if s.Kind != KindURL {
			t.Errorf("expected url kind, got %q for %q", s.Kind, s.Value)
		}
	}
}

func TestParseScopeDocumentCIDR(t *testing.T) {
	text := `10.0.0.0/24`
	result := ParseScopeDocument(text)
	if len(result.Seeds) != 1 {
		t.Fatalf("expected 1 seed, got %d", len(result.Seeds))
	}
	if result.Seeds[0].Kind != KindCIDR {
		t.Errorf("expected cidr, got %q", result.Seeds[0].Kind)
	}
}

func TestParseScopeDocumentDedup(t *testing.T) {
	text := `example.com
example.com
*.example.com`
	result := ParseScopeDocument(text)
	if len(result.Seeds) != 1 {
		t.Errorf("expected 1 deduped seed, got %d: %+v", len(result.Seeds), result.Seeds)
	}
}

func TestParseScopeDocumentComplex(t *testing.T) {
	text := `## In Scope
| Asset | Type |
|-------|------|
| example.com | Web |

Testing must not include brute force or denial of service.
`
	result := ParseScopeDocument(text)
	if !result.Complex {
		t.Error("expected complex flag for table + technique language")
	}
}

func TestParseScopeDocumentAmbiguous(t *testing.T) {
	text := `In Scope
- example.com except for admin.example.com`
	result := ParseScopeDocument(text)
	if !result.Complex {
		t.Error("expected complex flag for ambiguous language")
	}
}

func TestParseScopeDocumentIPNotDuplicatedFromURL(t *testing.T) {
	text := `https://192.168.1.1/admin`
	result := ParseScopeDocument(text)
	kinds := map[string]int{}
	for _, s := range result.Seeds {
		kinds[s.Kind]++
	}
	if kinds[KindHost] > 0 {
		t.Error("IP from URL should not create a separate host entry")
	}
}

func TestParseScopeDocumentWildcardStrip(t *testing.T) {
	text := `*.example.com`
	result := ParseScopeDocument(text)
	if len(result.Seeds) != 1 {
		t.Fatalf("expected 1, got %d", len(result.Seeds))
	}
	if result.Seeds[0].Value != "example.com" {
		t.Errorf("expected wildcard stripped, got %q", result.Seeds[0].Value)
	}
	if result.Seeds[0].Kind != KindDomain {
		t.Errorf("expected domain, got %q", result.Seeds[0].Kind)
	}
}
