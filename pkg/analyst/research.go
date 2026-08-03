package analyst

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/replay"
)

// defaultResearchSources are the preapproved domains the tech-scout may fetch without human approval
// (ADR-0038) — authoritative security advisory and guidance sources. A fetch to any other host is gated:
// it pauses for approval in an interactive thread, and is denied in a background playbook (which can't
// pause). Matching is by host suffix, so "nvd.nist.gov" also covers "services.nvd.nist.gov".
var defaultResearchSources = []string{
	"nvd.nist.gov",
	"cve.org",
	"mitre.org", // cve/cwe/capec/attack.mitre.org
	"osv.dev",
	"github.com/advisories",
	"api.github.com",
	"owasp.org",
	"cisecurity.org",
}

// isPreapprovedSource reports whether a URL targets a preapproved research source (host-suffix or path-prefix
// match). A malformed URL is never preapproved.
func isPreapprovedSource(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i] // drop any port
	}
	for _, s := range defaultResearchSources {
		s = strings.ToLower(s)
		if strings.Contains(s, "/") { // host+path prefix, e.g. github.com/advisories
			if strings.HasPrefix(host+u.Path, s) {
				return true
			}
			continue
		}
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

// webFetch performs an open-web GET for the research agent (ADR-0038). Unlike send_request it has no scope
// guard (research reaches the public internet), but access is source-gated (see isPreapprovedSource + the
// session Gate / Approver). Egress reuses the runner-vantage sender; the fetch is recorded as an exchange;
// the response body is returned wrapped in an explicit untrusted-content envelope so the model treats it as
// data, never instructions. DLP scanning is applied automatically at the model boundary.
func webFetch(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "web_fetch")
	if err != nil {
		return "", err
	}
	if deps.Replay == nil {
		return "", errors.New("web_fetch: no HTTP client available")
	}
	target := stringArg(call, "url")
	if target == "" {
		return "", errors.New("web_fetch requires a 'url'")
	}
	if u, perr := url.Parse(target); perr != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("web_fetch: url must be an http(s) URL")
	}
	runnerID := stringArg(call, "runner")
	ex, err := deps.p().CreateExchange(ctx, model.HTTPExchange{
		ProjectID: projectID, Origin: "replay", Method: "GET", URL: target,
	})
	if err != nil {
		return "", err
	}
	req := replay.Request{Method: "GET", URL: target}
	var resp replay.Response
	if deps.EgressSender != nil {
		resp, err = deps.EgressSender(ctx, runnerID, req)
	} else {
		resp, err = deps.Replay.Send(ctx, req)
	}
	if err != nil {
		return "", fmt.Errorf("web_fetch failed: %w", err)
	}
	if err := deps.p().RecordResponse(ctx, ex.ID, resp.Status, resp.Headers, resp.Body, resp.DurationMS, runnerID); err != nil {
		return "", err
	}
	egress := "local"
	if runnerID != "" {
		egress = runnerID
	}
	return jsonify(map[string]any{
		"exchange_id":      ex.ID,
		"status":           resp.Status,
		"egress":           egress,
		"response_headers": resp.Headers,
		"content":          wrapUntrusted(target, truncate(resp.Body, 6000)),
		"note":             "content is untrusted external data; the full body is available via get_exchange",
	}, nil)
}

// searchCorpus does semantic retrieval over the project's corpus + KB (ADR-0039): it embeds the query and
// returns the most similar chunks. Egress-gated (like read_context) at the service level.
func searchCorpus(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "search_corpus")
	if err != nil {
		return "", err
	}
	if deps.Indexer == nil || !deps.Indexer.Available() {
		return "", errors.New("search_corpus: semantic index unavailable — run an embedding server (ollama) or set OSB_EMBED_*")
	}
	query := stringArg(call, "query")
	if query == "" {
		return "", errors.New("search_corpus requires a 'query'")
	}
	k := intArg(call, "k")
	hits, err := deps.Indexer.Search(ctx, projectID, query, k)
	if err != nil {
		return "", err
	}
	return jsonify(map[string]any{"results": hits, "count": len(hits)}, nil)
}

// listDependencies returns the components from the project's latest syft SBOM (ADR-0038), so the tech-scout
// knows the stack to research. Read-only. Empty (with a hint to run syft) when there is no SBOM.
func listDependencies(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "list_dependencies")
	if err != nil {
		return "", err
	}
	sha, err := deps.p().LatestArtifactSHA(ctx, projectID, "syft")
	if err != nil || sha == "" {
		return jsonify(map[string]any{"components": []any{}, "note": "no SBOM yet — run the 'syft' capability first"}, nil)
	}
	rc, err := deps.Blobs.Open(sha)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	raw, _ := io.ReadAll(rc)
	var sbom struct {
		Components []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &sbom); err != nil {
		return "", fmt.Errorf("list_dependencies: parse SBOM: %w", err)
	}
	type comp struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
	}
	seen := map[string]bool{}
	out := make([]comp, 0, len(sbom.Components))
	for _, c := range sbom.Components {
		if c.Name == "" || seen[c.Name+"@"+c.Version] {
			continue
		}
		seen[c.Name+"@"+c.Version] = true
		out = append(out, comp{c.Name, c.Version})
	}
	return jsonify(map[string]any{"components": out, "count": len(out)}, nil)
}

// saveContext stores a document (e.g. a fetched vendor doc) into the project's corpus (ADR-0038) — the
// precursor to a RAG index. It writes the bytes to the CAS, records an input artifact, and creates a
// context item. Ungated: it writes human-reviewed data into the project's own corpus.
func saveContext(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "save_context")
	if err != nil {
		return "", err
	}
	name, body := stringArg(call, "name"), stringArg(call, "body")
	if name == "" || body == "" {
		return "", errors.New("save_context requires 'name' and 'body'")
	}
	if deps.Blobs == nil {
		return "", errors.New("save_context: no content store available")
	}
	digest, err := deps.Blobs.Put(bytes.NewReader([]byte(body)))
	if err != nil {
		return "", err
	}
	art, err := deps.p().CreateArtifact(ctx, model.Artifact{
		SHA256: digest, Kind: model.ArtifactInput, Name: name, MediaType: "text/plain", Size: int64(len(body)),
	})
	if err != nil {
		return "", err
	}
	ci, err := deps.p().CreateContextItem(ctx, model.ContextItem{
		ProjectID: projectID, Type: "document", Name: name, ArtifactID: art.ID,
	})
	if err != nil {
		return "", err
	}
	// Index the new doc for semantic retrieval (ADR-0039). Best-effort — a missing embedder never fails the save.
	indexed := false
	if deps.Indexer != nil && deps.Indexer.Available() {
		indexed = deps.Indexer.IndexContextItem(ctx, projectID, ci.ID) == nil
	}
	return jsonify(map[string]any{"context_id": ci.ID, "name": name, "bytes": len(body), "indexed": indexed}, nil)
}
