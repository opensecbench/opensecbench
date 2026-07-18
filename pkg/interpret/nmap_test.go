package interpret

import (
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

const nmapSample = `<?xml version="1.0"?>
<nmaprun>
  <host>
    <address addr="10.0.0.5" addrtype="ipv4"/>
    <hostnames><hostname name="api.acme.internal"/></hostnames>
    <ports>
      <port protocol="tcp" portid="443"><state state="open"/><service name="https" product="nginx" version="1.25"/></port>
      <port protocol="tcp" portid="22"><state state="open"/><service name="ssh"/></port>
      <port protocol="tcp" portid="8080"><state state="closed"/><service name="http"/></port>
    </ports>
  </host>
</nmaprun>`

func TestNmapXML(t *testing.T) {
	obs, err := NmapXML([]byte(nmapSample))
	if err != nil {
		t.Fatal(err)
	}
	// Two open ports (443, 22); the closed 8080 is skipped.
	if len(obs) != 2 {
		t.Fatalf("got %d observations, want 2: %+v", len(obs), obs)
	}
	for _, o := range obs {
		if o.Origin != model.OriginTool || o.RuleID != "nmap/open-port" {
			t.Fatalf("observation not tool/nmap: %+v", o)
		}
		if !strings.HasPrefix(o.Location, "api.acme.internal:") {
			t.Fatalf("location should use hostname: %q", o.Location)
		}
	}
	if !strings.Contains(obs[0].Title, "443/tcp") || !strings.Contains(obs[0].Title, "nginx") {
		t.Fatalf("first obs title wrong: %q", obs[0].Title)
	}
}

func TestNmapXMLBadInput(t *testing.T) {
	if _, err := NmapXML([]byte("not xml")); err == nil {
		t.Fatal("expected error for non-XML input")
	}
}
