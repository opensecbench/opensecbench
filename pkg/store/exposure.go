package store

import (
	"context"
	"net/url"
	"sort"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Exposure is a derived read of whether a project hosts a network-exposed service, with a compact summary
// of the surface (ADR-0030). It is computed from existing evidence — there is no manual "exposed" flag — so
// disposition routing can gate on `exposed` without new data entry. It is a point-in-time snapshot.
type Exposure struct {
	Exposed     bool     `json:"exposed"`
	OpenPorts   []string `json:"open_ports,omitempty"`  // nmap/open-port observation locations (host:port/proto)
	Endpoints   []string `json:"endpoints,omitempty"`   // distinct hosts seen in captured HTTP exchanges
	Deployments []string `json:"deployments,omitempty"` // cloud_deployment / infrastructure asset locations
}

const exposureListCap = 20 // keep the surface summary compact; the boolean is what routing needs

// ProjectExposure derives whether a project is network-exposed and a short surface summary from three
// signals: nmap open-port observations, captured proxy/replay exchanges, and cloud_deployment/infrastructure
// assets (ADR-0030). Any one signal marks the project exposed. Best-effort: a query error for one signal
// does not fail the others.
func (db *DB) ProjectExposure(ctx context.Context, projectID string) (Exposure, error) {
	var exp Exposure
	if projectID == "" {
		return exp, nil
	}

	// Open ports from nmap observations (rule_id "nmap/open-port", location "host:port/proto").
	if obs, err := db.ListObservationsByProject(ctx, projectID); err == nil {
		seen := map[string]bool{}
		for _, o := range obs {
			if o.RuleID == "nmap/open-port" && o.Location != "" && !seen[o.Location] {
				seen[o.Location] = true
				exp.OpenPorts = append(exp.OpenPorts, o.Location)
			}
		}
	}

	// Distinct hosts from captured HTTP exchanges (proof a host answered HTTP).
	if xs, err := db.ListExchangesByProject(ctx, projectID); err == nil {
		seen := map[string]bool{}
		for _, x := range xs {
			host := x.URL
			if u, perr := url.Parse(x.URL); perr == nil && u.Host != "" {
				host = u.Host
			}
			if host != "" && !seen[host] {
				seen[host] = true
				exp.Endpoints = append(exp.Endpoints, host)
			}
		}
	}

	// Deployment/infrastructure assets across the project's applications.
	if apps, err := db.ListApplicationsByProject(ctx, projectID); err == nil {
		seen := map[string]bool{}
		for _, app := range apps {
			assets, aerr := db.ListAssetsByApplication(ctx, app.ID)
			if aerr != nil {
				continue
			}
			for _, a := range assets {
				if a.Type != model.AssetCloudDeployment && a.Type != model.AssetInfrastructure {
					continue
				}
				loc := a.Location
				if loc == "" {
					loc = a.Type
				}
				if !seen[loc] {
					seen[loc] = true
					exp.Deployments = append(exp.Deployments, loc)
				}
			}
		}
	}

	sort.Strings(exp.OpenPorts)
	sort.Strings(exp.Endpoints)
	sort.Strings(exp.Deployments)
	exp.OpenPorts = cap20(exp.OpenPorts)
	exp.Endpoints = cap20(exp.Endpoints)
	exp.Deployments = cap20(exp.Deployments)
	exp.Exposed = len(exp.OpenPorts) > 0 || len(exp.Endpoints) > 0 || len(exp.Deployments) > 0
	return exp, nil
}

func cap20(s []string) []string {
	if len(s) > exposureListCap {
		return s[:exposureListCap]
	}
	return s
}
