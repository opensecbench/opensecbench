// Package srcfile provides path-confined reads of a source_repo asset's on-disk tree. The confinement
// logic is security-critical (it is the boundary that keeps a caller-supplied relative path from escaping
// the asset root via traversal or symlink), so it lives in one place and is shared by both the analyst
// agent tools (pkg/analyst) and the HTTP source-viewer endpoints (pkg/api).
package srcfile

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GrepMatch is one content hit: a repo-relative path, 1-based line number, and the (trimmed) line text.
type GrepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Grep searches a source tree for a literal, case-insensitive substring, bounded so it stays fast enough
// to back an interactive search: it skips noise dirs and dotdirs, files larger than maxFileBytes, and
// binary files (NUL byte), and stops at maxMatches. Best-effort — unreadable entries are skipped.
func Grep(root, needle string, maxMatches, maxFileBytes int) []GrepMatch {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return nil
	}
	if maxMatches <= 0 {
		maxMatches = 100
	}
	if maxFileBytes <= 0 {
		maxFileBytes = 2 << 20
	}
	root = filepath.Clean(root)
	nb := []byte(needle)
	var out []GrepMatch
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if p != root && (noiseDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > int64(maxFileBytes) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(data, 0) >= 0 { // unreadable or binary
			return nil
		}
		if !bytes.Contains(bytes.ToLower(data), nb) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(strings.ToLower(line), needle) {
				text := strings.TrimSpace(line)
				if len(text) > 200 {
					text = text[:200] + "…"
				}
				out = append(out, GrepMatch{Path: rel, Line: i + 1, Text: text})
				if len(out) >= maxMatches {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	return out
}

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
