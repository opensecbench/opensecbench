package api

import (
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
