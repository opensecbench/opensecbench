// Package srcfile provides path-confined reads of a source_repo asset's on-disk tree. The confinement
// logic is security-critical (it is the boundary that keeps a caller-supplied relative path from escaping
// the asset root via traversal or symlink), so it lives in one place and is shared by both the analyst
// agent tools (pkg/analyst) and the HTTP source-viewer endpoints (pkg/api).
package srcfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConfinedPath resolves rel against root and refuses anything that escapes it — both lexically (".." /
// absolute paths) and via symlinks when the target already exists on disk.
func ConfinedPath(root, rel string) (string, error) {
	root = filepath.Clean(root)
	if strings.TrimSpace(rel) == "" {
		rel = "."
	}
	full := filepath.Clean(filepath.Join(root, rel))
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the asset root", rel)
	}
	// Defend against symlink escape when the target exists.
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		rroot, _ := filepath.EvalSymlinks(root)
		if rroot == "" {
			rroot = root
		}
		if resolved != rroot && !strings.HasPrefix(resolved, rroot+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q resolves outside the asset root", rel)
		}
	}
	return full, nil
}

// File is a file's contents plus enough metadata for a viewer to show size and a truncation notice.
type File struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Bytes     int64  `json:"bytes"`
	Lines     int    `json:"lines"`
	Truncated bool   `json:"truncated"`
}

// ReadFile returns a confined file's contents, capped at maxBytes. It rejects directories so the caller
// gets a clear error rather than a stream of bytes.
func ReadFile(root, rel string, maxBytes int) (File, error) {
	full, err := ConfinedPath(root, rel)
	if err != nil {
		return File{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return File{}, err
	}
	if info.IsDir() {
		return File{}, fmt.Errorf("%q is a directory", rel)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return File{}, err
	}
	content := string(data)
	truncated := false
	if maxBytes > 0 && len(content) > maxBytes {
		content = content[:maxBytes]
		truncated = true
	}
	return File{
		Path:      rel,
		Content:   content,
		Bytes:     info.Size(),
		Lines:     strings.Count(content, "\n") + 1,
		Truncated: truncated,
	}, nil
}

// Entry is one directory child in a tree listing.
type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"` // path relative to the asset root, usable as the next request's rel
	Dir  bool   `json:"dir"`
	Size int64  `json:"size,omitempty"`
}

// noiseDirs are excluded from tree listings — VCS metadata and dependency caches that would only clutter
// a source browser.
var noiseDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true}

// ListDir lists one confined directory's children, directories first then files, each alphabetical. Noise
// directories are omitted.
func ListDir(root, rel string) ([]Entry, error) {
	full, err := ConfinedPath(root, rel)
	if err != nil {
		return nil, err
	}
	children, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	base := strings.Trim(filepath.ToSlash(strings.TrimPrefix(filepath.Clean(rel), ".")), "/")
	out := make([]Entry, 0, len(children))
	for _, c := range children {
		if c.IsDir() && noiseDirs[c.Name()] {
			continue
		}
		e := Entry{Name: c.Name(), Dir: c.IsDir()}
		if base == "" {
			e.Path = c.Name()
		} else {
			e.Path = base + "/" + c.Name()
		}
		if !c.IsDir() {
			if info, err := c.Info(); err == nil {
				e.Size = info.Size()
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
