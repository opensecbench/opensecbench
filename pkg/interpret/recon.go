package interpret

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// AssetCandidate is a discovered asset from a recon tool's output, ready for scope-checked ingestion
// into the asset inventory (ADR-0071). Unlike observations (which feed triage), candidates feed the
// asset graph — the engine upserts them as domain/host/endpoint/web_service assets.
type AssetCandidate struct {
	Type     string            // model.AssetDomain / AssetHost / AssetWebService / AssetEndpoint
	Location string            // the asset's identifier (domain name, IP, URL, …)
	Tags     []string          // auto-derived tags (e.g. technology, status code)
	Metadata map[string]string // tool-specific attributes
}

// CandidateLink declares a relationship between two candidates (by their Location values).
type CandidateLink struct {
	SourceType   string // asset type of source
	SourceLoc    string // location of source
	Relationship string // e.g. "resolves_to", "contains"
	TargetType   string // asset type of target
	TargetLoc    string // location of target
}

// ReconResult holds the parsed output of a recon tool.
type ReconResult struct {
	Assets []AssetCandidate
	Links  []CandidateLink
}

// Recon media types.
const (
	SubfinderMediaType = "application/x-subfinder-jsonl"
	DnsxMediaType      = "application/x-dnsx-jsonl"
	HttpxMediaType     = "application/x-httpx-jsonl"
	FfufMediaType      = "application/x-ffuf-json"
)

// subfinder JSONL record.
type subfinderRecord struct {
	Host   string `json:"host"`
	Source string `json:"source"`
}

// Subfinder parses subfinder's JSONL output into domain asset candidates.
func Subfinder(data []byte) (ReconResult, error) {
	var result ReconResult
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r subfinderRecord
		if err := json.Unmarshal(line, &r); err != nil || r.Host == "" {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(r.Host))
		if seen[host] {
			continue
		}
		seen[host] = true
		c := AssetCandidate{
			Type:     "domain",
			Location: host,
			Metadata: map[string]string{},
		}
		if r.Source != "" {
			c.Metadata["source"] = r.Source
		}
		result.Assets = append(result.Assets, c)
	}
	if err := sc.Err(); err != nil {
		return result, fmt.Errorf("interpret: read subfinder output: %w", err)
	}
	return result, nil
}

// dnsx JSONL record.
type dnsxRecord struct {
	Host       string   `json:"host"`
	A          []string `json:"a"`
	AAAA       []string `json:"aaaa"`
	CNAME      []string `json:"cname"`
	StatusCode string   `json:"status_code"`
}

// Dnsx parses dnsx's JSONL output into domain and host candidates with resolves_to links.
func Dnsx(data []byte) (ReconResult, error) {
	var result ReconResult
	seenDomain := map[string]bool{}
	seenHost := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r dnsxRecord
		if err := json.Unmarshal(line, &r); err != nil || r.Host == "" {
			continue
		}
		domain := strings.ToLower(strings.TrimSpace(r.Host))
		if !seenDomain[domain] {
			seenDomain[domain] = true
			c := AssetCandidate{
				Type:     "domain",
				Location: domain,
				Metadata: map[string]string{},
			}
			if r.StatusCode != "" {
				c.Metadata["dns_status"] = r.StatusCode
			}
			if len(r.CNAME) > 0 {
				c.Metadata["cname"] = strings.Join(r.CNAME, ",")
			}
			result.Assets = append(result.Assets, c)
		}

		for _, ip := range append(r.A, r.AAAA...) {
			ip = strings.TrimSpace(ip)
			if ip == "" || net.ParseIP(ip) == nil {
				continue
			}
			if !seenHost[ip] {
				seenHost[ip] = true
				result.Assets = append(result.Assets, AssetCandidate{
					Type:     "host",
					Location: ip,
				})
			}
			result.Links = append(result.Links, CandidateLink{
				SourceType:   "domain",
				SourceLoc:    domain,
				Relationship: "resolves_to",
				TargetType:   "host",
				TargetLoc:    ip,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return result, fmt.Errorf("interpret: read dnsx output: %w", err)
	}
	return result, nil
}

// httpx JSONL record.
type httpxRecord struct {
	Input      string   `json:"input"`
	URL        string   `json:"url"`
	StatusCode int      `json:"status_code"`
	Title      string   `json:"title"`
	Tech       []string `json:"tech"`
	Host       string   `json:"host"`
	Port       string   `json:"port"`
	Scheme     string   `json:"scheme"`
	WebServer  string   `json:"webserver"`
}

// Httpx parses httpx's JSONL output into web_service candidates.
func Httpx(data []byte) (ReconResult, error) {
	var result ReconResult
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r httpxRecord
		if err := json.Unmarshal(line, &r); err != nil || r.URL == "" {
			continue
		}
		loc := strings.TrimRight(r.URL, "/")
		if seen[loc] {
			continue
		}
		seen[loc] = true

		c := AssetCandidate{
			Type:     "web_service",
			Location: loc,
			Metadata: map[string]string{},
		}
		if r.StatusCode > 0 {
			c.Metadata["status_code"] = fmt.Sprintf("%d", r.StatusCode)
		}
		if r.Title != "" {
			c.Metadata["title"] = r.Title
		}
		if r.WebServer != "" {
			c.Metadata["webserver"] = r.WebServer
			c.Tags = append(c.Tags, strings.ToLower(strings.Split(r.WebServer, "/")[0]))
		}
		for _, t := range r.Tech {
			c.Tags = append(c.Tags, strings.ToLower(t))
		}

		result.Assets = append(result.Assets, c)

		if r.Input != "" {
			input := strings.ToLower(strings.TrimSpace(r.Input))
			if net.ParseIP(input) != nil {
				result.Links = append(result.Links, CandidateLink{
					SourceType:   "host",
					SourceLoc:    input,
					Relationship: "serves",
					TargetType:   "web_service",
					TargetLoc:    loc,
				})
			} else if input != "" {
				result.Links = append(result.Links, CandidateLink{
					SourceType:   "domain",
					SourceLoc:    input,
					Relationship: "serves",
					TargetType:   "web_service",
					TargetLoc:    loc,
				})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return result, fmt.Errorf("interpret: read httpx output: %w", err)
	}
	return result, nil
}

// ffuf JSON result structure (the top-level envelope, not individual results).
type ffufOutput struct {
	Results []ffufResult `json:"results"`
}

type ffufResult struct {
	Input            map[string]string `json:"input"`
	URL              string            `json:"url"`
	Status           int               `json:"status"`
	Length           int               `json:"length"`
	Words            int               `json:"words"`
	Lines            int               `json:"lines"`
	Redirectlocation string            `json:"redirectlocation"`
}

// Ffuf parses ffuf's JSON output into endpoint candidates under a web_service.
func Ffuf(data []byte) (ReconResult, error) {
	var out ffufOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return ReconResult{}, fmt.Errorf("interpret: parse ffuf output: %w", err)
	}
	var result ReconResult
	seen := map[string]bool{}
	for _, r := range out.Results {
		if r.URL == "" {
			continue
		}
		loc := fmt.Sprintf("GET %s", r.URL)
		if seen[loc] {
			continue
		}
		seen[loc] = true

		c := AssetCandidate{
			Type:     "endpoint",
			Location: loc,
			Metadata: map[string]string{
				"status_code": fmt.Sprintf("%d", r.Status),
				"length":      fmt.Sprintf("%d", r.Length),
			},
		}
		if r.Redirectlocation != "" {
			c.Metadata["redirect"] = r.Redirectlocation
		}
		result.Assets = append(result.Assets, c)
	}
	return result, nil
}
