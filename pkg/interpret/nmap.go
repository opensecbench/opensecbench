package interpret

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// NmapMediaType is the media type interpreted by NmapXML (nmap -oX output).
const NmapMediaType = "application/x-nmap-xml"

// nmapRun mirrors the subset of nmap XML we consume.
type nmapRun struct {
	Hosts []struct {
		Addresses []struct {
			Addr     string `xml:"addr,attr"`
			AddrType string `xml:"addrtype,attr"`
		} `xml:"address"`
		Hostnames struct {
			Hostname []struct {
				Name string `xml:"name,attr"`
			} `xml:"hostname"`
		} `xml:"hostnames"`
		Ports struct {
			Port []struct {
				Protocol string `xml:"protocol,attr"`
				PortID   string `xml:"portid,attr"`
				State    struct {
					State string `xml:"state,attr"`
				} `xml:"state"`
				Service struct {
					Name    string `xml:"name,attr"`
					Product string `xml:"product,attr"`
					Version string `xml:"version,attr"`
				} `xml:"service"`
			} `xml:"port"`
		} `xml:"ports"`
	} `xml:"host"`
}

// NmapXML interprets nmap XML into one unreviewed observation per open port (ADR-0005). The location
// is "<host>:<port>/<proto>", which the topology graph parses into host → port nodes.
func NmapXML(data []byte) ([]model.Observation, error) {
	var run nmapRun
	if err := xml.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	var out []model.Observation
	for _, h := range run.Hosts {
		host := hostLabel(h.Addresses, h.Hostnames.Hostname)
		if host == "" {
			continue
		}
		for _, p := range h.Ports.Port {
			if p.State.State != "open" {
				continue
			}
			svc := strings.TrimSpace(strings.Join([]string{p.Service.Name, p.Service.Product, p.Service.Version}, " "))
			title := fmt.Sprintf("Open port %s/%s", p.PortID, p.Protocol)
			if svc != "" {
				title += " — " + svc
			}
			out = append(out, model.Observation{
				Origin:      model.OriginTool,
				ReviewState: model.ReviewUnreviewed,
				Title:       title,
				Severity:    "info",
				RuleID:      "nmap/open-port",
				Location:    fmt.Sprintf("%s:%s/%s", host, p.PortID, p.Protocol),
				Detail:      svc,
			})
		}
	}
	return out, nil
}

func hostLabel(addrs []struct {
	Addr     string `xml:"addr,attr"`
	AddrType string `xml:"addrtype,attr"`
}, names []struct {
	Name string `xml:"name,attr"`
}) string {
	if len(names) > 0 && names[0].Name != "" {
		return names[0].Name
	}
	// Prefer IPv4, else the first address.
	for _, a := range addrs {
		if a.AddrType == "ipv4" {
			return a.Addr
		}
	}
	if len(addrs) > 0 {
		return addrs[0].Addr
	}
	return ""
}
