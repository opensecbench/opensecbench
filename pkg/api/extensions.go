package api

import "net/http"

// extensionInfo is the JSON view of a loaded extension package.
type extensionInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Publisher     string   `json:"publisher"`
	Trusted       bool     `json:"trusted"`
	Digest        string   `json:"digest"`
	Capabilities  []string `json:"capabilities,omitempty"`
	Methodologies []string `json:"methodologies,omitempty"`
}

// listExtensions returns the loaded extension packages (metadata only).
func (s *Server) listExtensions(w http.ResponseWriter, _ *http.Request) {
	out := make([]extensionInfo, 0, len(s.exts))
	for _, e := range s.exts {
		info := extensionInfo{
			ID: e.Manifest.ID, Name: e.Manifest.Name, Version: e.Manifest.Version,
			Publisher: e.Manifest.Publisher, Trusted: e.Trusted, Digest: e.Digest,
		}
		for _, c := range e.Manifest.Capabilities {
			info.Capabilities = append(info.Capabilities, c.ID)
		}
		for _, m := range e.Manifest.Methodologies {
			info.Methodologies = append(info.Methodologies, m.ID)
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, out)
}
