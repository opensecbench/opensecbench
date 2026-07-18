package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// graphNode/graphEdge are the view model for the interactive graph visualization (P8/P12).
type graphNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`            // project|application|asset|finding|host|endpoint
	Group string `json:"group,omitempty"` // e.g. severity or status, for coloring
	Meta  string `json:"meta,omitempty"`
}
type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type graphResp struct {
	Kind  string      `json:"kind"`
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

// projectGraph serves a node/edge graph for a project: kind=structure (project→apps→assets/findings)
// or kind=traffic (hosts→endpoints from captured HTTP exchanges).
func (s *Server) projectGraph(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "structure"
	}
	var resp graphResp
	var err error
	switch kind {
	case "structure":
		resp, err = s.structureGraph(r, projectID)
	case "traffic":
		resp, err = s.trafficGraph(r, projectID)
	case "topology":
		resp, err = s.topologyGraph(r, projectID)
	case "dependency":
		resp, err = s.dependencyGraph(r, projectID)
	default:
		writeErr(w, http.StatusBadRequest, "unknown graph kind "+kind)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) structureGraph(r *http.Request, projectID string) (graphResp, error) {
	ctx := r.Context()
	proj, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return graphResp{}, err
	}
	g := graphResp{Kind: "structure"}
	g.Nodes = append(g.Nodes, graphNode{ID: "p:" + proj.ID, Label: proj.Name, Kind: "project"})

	apps, err := s.store.ListApplicationsByProject(ctx, projectID)
	if err != nil {
		return graphResp{}, err
	}
	appNode := map[string]string{}
	for _, a := range apps {
		id := "a:" + a.ID
		appNode[a.ID] = id
		g.Nodes = append(g.Nodes, graphNode{ID: id, Label: a.Name, Kind: "application"})
		g.Edges = append(g.Edges, graphEdge{From: "p:" + proj.ID, To: id})

		assets, err := s.store.ListAssetsByApplication(ctx, a.ID)
		if err != nil {
			return graphResp{}, err
		}
		for _, as := range assets {
			nid := "as:" + as.ID
			g.Nodes = append(g.Nodes, graphNode{ID: nid, Label: shortLocation(as.Location), Kind: "asset", Group: as.Sensitivity, Meta: as.Type})
			g.Edges = append(g.Edges, graphEdge{From: id, To: nid})
		}
	}

	findings, err := s.store.ListFindings(ctx)
	if err != nil {
		return graphResp{}, err
	}
	for _, f := range findings {
		if f.ApplicationID == nil {
			continue
		}
		parent, ok := appNode[*f.ApplicationID]
		if !ok {
			continue // not in this project
		}
		nid := "f:" + f.ID
		g.Nodes = append(g.Nodes, graphNode{ID: nid, Label: f.Title, Kind: "finding", Group: f.Severity, Meta: f.Status})
		g.Edges = append(g.Edges, graphEdge{From: parent, To: nid})
	}
	return g, nil
}

func (s *Server) trafficGraph(r *http.Request, projectID string) (graphResp, error) {
	exchanges, err := s.store.ListExchangesByProject(r.Context(), projectID)
	if err != nil {
		return graphResp{}, err
	}
	g := graphResp{Kind: "traffic"}
	hostSeen := map[string]bool{}
	endpointSeen := map[string]bool{}
	const maxNodes = 300
	for _, ex := range exchanges {
		if len(g.Nodes) >= maxNodes {
			break
		}
		u, perr := url.Parse(ex.URL)
		if perr != nil || u.Host == "" {
			continue
		}
		hostID := "h:" + u.Host
		if !hostSeen[u.Host] {
			hostSeen[u.Host] = true
			g.Nodes = append(g.Nodes, graphNode{ID: hostID, Label: u.Host, Kind: "host"})
		}
		path := u.Path
		if path == "" {
			path = "/"
		}
		key := u.Host + " " + ex.Method + " " + path
		if endpointSeen[key] {
			continue
		}
		endpointSeen[key] = true
		epID := "e:" + key
		status := ""
		if ex.Status != nil {
			status = statusClass(*ex.Status)
		}
		g.Nodes = append(g.Nodes, graphNode{ID: epID, Label: ex.Method + " " + path, Kind: "endpoint", Group: status, Meta: ex.Origin})
		g.Edges = append(g.Edges, graphEdge{From: hostID, To: epID})
	}
	return g, nil
}

// topologyGraph builds host → open-port nodes from nmap observations (location "host:port/proto").
func (s *Server) topologyGraph(r *http.Request, projectID string) (graphResp, error) {
	obs, err := s.store.ListObservationsByProject(r.Context(), projectID)
	if err != nil {
		return graphResp{}, err
	}
	g := graphResp{Kind: "topology"}
	hostSeen := map[string]bool{}
	for _, o := range obs {
		if o.RuleID != "nmap/open-port" || o.Location == "" {
			continue
		}
		host, port, ok := strings.Cut(o.Location, ":")
		if !ok {
			continue
		}
		hostID := "h:" + host
		if !hostSeen[host] {
			hostSeen[host] = true
			g.Nodes = append(g.Nodes, graphNode{ID: hostID, Label: host, Kind: "host"})
		}
		pid := "port:" + o.Location
		g.Nodes = append(g.Nodes, graphNode{ID: pid, Label: port, Kind: "endpoint", Group: "2xx", Meta: o.Detail})
		g.Edges = append(g.Edges, graphEdge{From: hostID, To: pid})
	}
	return g, nil
}

// cycloneDX is the subset of a CycloneDX SBOM the dependency graph consumes.
type cycloneDX struct {
	Components []struct {
		BOMRef  string `json:"bom-ref"`
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"components"`
	Dependencies []struct {
		Ref       string   `json:"ref"`
		DependsOn []string `json:"dependsOn"`
	} `json:"dependencies"`
}

// dependencyGraph parses the project's latest syft SBOM into a component/dependency graph.
func (s *Server) dependencyGraph(r *http.Request, projectID string) (graphResp, error) {
	g := graphResp{Kind: "dependency"}
	sha, err := s.store.LatestArtifactSHA(r.Context(), projectID, "syft")
	if err != nil {
		return g, nil // no SBOM yet → empty graph
	}
	rc, err := s.cas.Open(sha)
	if err != nil {
		return g, nil
	}
	defer func() { _ = rc.Close() }()
	raw, _ := io.ReadAll(rc)

	var sbom cycloneDX
	if err := json.Unmarshal(raw, &sbom); err != nil {
		return g, nil
	}
	const maxNodes = 400
	label := map[string]string{} // ref -> label
	for _, c := range sbom.Components {
		if len(g.Nodes) >= maxNodes {
			break
		}
		ref := c.BOMRef
		if ref == "" {
			ref = c.Name
		}
		l := c.Name
		if c.Version != "" {
			l += "@" + c.Version
		}
		label[ref] = l
		g.Nodes = append(g.Nodes, graphNode{ID: "c:" + ref, Label: l, Kind: "asset", Meta: c.Version})
	}
	for _, d := range sbom.Dependencies {
		if _, ok := label[d.Ref]; !ok {
			continue
		}
		for _, dep := range d.DependsOn {
			if _, ok := label[dep]; !ok {
				continue
			}
			g.Edges = append(g.Edges, graphEdge{From: "c:" + d.Ref, To: "c:" + dep})
		}
	}
	return g, nil
}

// statusClass buckets an HTTP status for coloring.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return ""
	}
}

func shortLocation(loc string) string {
	loc = strings.TrimRight(loc, "/")
	if i := strings.LastIndexAny(loc, "/\\"); i >= 0 && i < len(loc)-1 {
		return loc[i+1:]
	}
	return loc
}
