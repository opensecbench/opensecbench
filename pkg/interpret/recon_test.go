package interpret

import (
	"testing"
)

func TestSubfinder(t *testing.T) {
	input := `{"host":"api.example.com","source":"crtsh"}
{"host":"mail.example.com","source":"dnsdumpster"}
{"host":"api.example.com","source":"virustotal"}
not json
`
	result, err := Subfinder([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(result.Assets))
	}
	if result.Assets[0].Type != "domain" || result.Assets[0].Location != "api.example.com" {
		t.Errorf("unexpected first asset: %+v", result.Assets[0])
	}
	if result.Assets[0].Metadata["source"] != "crtsh" {
		t.Errorf("expected source=crtsh, got %q", result.Assets[0].Metadata["source"])
	}
	if result.Assets[1].Location != "mail.example.com" {
		t.Errorf("unexpected second asset: %+v", result.Assets[1])
	}
}

func TestDnsx(t *testing.T) {
	input := `{"host":"api.example.com","a":["93.184.216.34"],"status_code":"NOERROR"}
{"host":"mail.example.com","a":["93.184.216.34","10.0.0.1"],"aaaa":["2001:db8::1"],"cname":["mail.cdn.example.com"]}
`
	result, err := Dnsx([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 5 {
		t.Fatalf("expected 5 assets (2 domains + 3 hosts), got %d", len(result.Assets))
	}
	domains := 0
	hosts := 0
	for _, a := range result.Assets {
		switch a.Type {
		case "domain":
			domains++
		case "host":
			hosts++
		}
	}
	if domains != 2 || hosts != 3 {
		t.Errorf("expected 2 domains + 3 hosts, got %d domains + %d hosts", domains, hosts)
	}
	if len(result.Links) != 4 {
		t.Fatalf("expected 4 links, got %d", len(result.Links))
	}
	if result.Links[0].Relationship != "resolves_to" {
		t.Errorf("expected resolves_to, got %q", result.Links[0].Relationship)
	}
}

func TestDnsxDedup(t *testing.T) {
	input := `{"host":"a.example.com","a":["1.2.3.4"]}
{"host":"b.example.com","a":["1.2.3.4"]}
`
	result, err := Dnsx([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	hosts := 0
	for _, a := range result.Assets {
		if a.Type == "host" {
			hosts++
		}
	}
	if hosts != 1 {
		t.Errorf("expected 1 deduplicated host, got %d", hosts)
	}
	if len(result.Links) != 2 {
		t.Errorf("expected 2 links (one per domain→host), got %d", len(result.Links))
	}
}

func TestHttpx(t *testing.T) {
	input := `{"input":"api.example.com","url":"https://api.example.com","status_code":200,"title":"API","tech":["Express"],"webserver":"nginx/1.25.3"}
{"input":"10.0.0.1","url":"http://10.0.0.1:8080","status_code":301,"title":"","webserver":"Apache"}
`
	result, err := Httpx([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 2 {
		t.Fatalf("expected 2 web_service assets, got %d", len(result.Assets))
	}
	if result.Assets[0].Type != "web_service" {
		t.Errorf("expected web_service, got %q", result.Assets[0].Type)
	}
	if result.Assets[0].Location != "https://api.example.com" {
		t.Errorf("unexpected location: %q", result.Assets[0].Location)
	}
	if len(result.Assets[0].Tags) != 2 {
		t.Errorf("expected 2 tags (nginx + express), got %v", result.Assets[0].Tags)
	}
	if len(result.Links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(result.Links))
	}
	if result.Links[0].SourceType != "domain" {
		t.Errorf("expected domain→web_service link, got source type %q", result.Links[0].SourceType)
	}
	if result.Links[1].SourceType != "host" {
		t.Errorf("expected host→web_service link, got source type %q", result.Links[1].SourceType)
	}
}

func TestFfuf(t *testing.T) {
	input := `{
  "results": [
    {"url":"https://example.com/admin","status":200,"length":1234,"words":100,"lines":50},
    {"url":"https://example.com/login","status":302,"length":0,"words":0,"lines":0,"redirectlocation":"https://example.com/dashboard"},
    {"url":"https://example.com/admin","status":200,"length":1234,"words":100,"lines":50}
  ]
}`
	result, err := Ffuf([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 2 {
		t.Fatalf("expected 2 deduplicated endpoints, got %d", len(result.Assets))
	}
	if result.Assets[0].Type != "endpoint" {
		t.Errorf("expected endpoint, got %q", result.Assets[0].Type)
	}
	if result.Assets[1].Metadata["redirect"] != "https://example.com/dashboard" {
		t.Errorf("expected redirect metadata, got %+v", result.Assets[1].Metadata)
	}
}

func TestSubfinderEmpty(t *testing.T) {
	result, err := Subfinder([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Assets) != 0 {
		t.Errorf("expected 0 assets from empty input, got %d", len(result.Assets))
	}
}
