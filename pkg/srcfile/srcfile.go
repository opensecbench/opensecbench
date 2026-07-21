// Package srcfile provides path-confined reads of a source_repo asset's on-disk tree. The confinement
// logic is security-critical (it is the boundary that keeps a caller-supplied relative path from escaping
// the asset root via traversal or symlink), so it lives in one place and is shared by both the analyst
// agent tools (pkg/analyst) and the HTTP source-viewer endpoints (pkg/api).
package srcfile

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/url"
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

// normalizeRel strips a `file://` scheme (and any authority) and percent-decodes the remainder, so a
// SARIF-style "file:///src/app/x.go" location resolves the same as a plain path. Plain paths are returned
// untouched — we do not percent-decode those, since a literal "%" is a valid filename character.
func normalizeRel(rel string) string {
	rel = strings.TrimSpace(rel)
	if after, ok := strings.CutPrefix(rel, "file://"); ok {
		if i := strings.IndexByte(after, '/'); i > 0 { // drop an authority ("host" in file://host/path)
			after = after[i:]
		}
		if dec, err := url.PathUnescape(after); err == nil {
			after = dec
		}
		rel = after
	}
	return rel
}

// Resolve maps a caller-supplied path to a confined, existing path under root. It first tries the path as
// given; if that lexically escapes the root it returns the escape error (callers surface a 4xx), and if it
// simply doesn't exist it strips leading path segments one at a time — longest suffix first — and returns
// the first candidate that exists. That fallback recovers findings whose `location` carries a scanner's
// container-mount prefix: TruffleHog (`filesystem /src`) and govulncheck emit absolute "/src/app/x.go"
// paths that must be re-anchored to the on-disk repo root as "app/x.go". Every candidate is confined to
// root, so no fallback can escape it. Returns an error wrapping fs.ErrNotExist when nothing matches, which
// errors.Is(err, fs.ErrNotExist) detects so callers can map it to 404.
func Resolve(root, rel string) (string, error) {
	rel = normalizeRel(rel)
	// The path exactly as given wins when it exists — a real escape here is a hard error, not a not-found.
	full, err := ConfinedPath(root, rel)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(full); err == nil {
		return full, nil
	}
	// Not found as given: peel leading segments (a mount prefix like "src/") and try each shorter suffix,
	// most-specific first so we prefer "app/x.go" over a coincidental top-level "x.go".
	segs := strings.Split(strings.TrimLeft(filepath.ToSlash(rel), "/"), "/")
	for i := 1; i < len(segs); i++ {
		cand, err := ConfinedPath(root, strings.Join(segs[i:], "/"))
		if err != nil {
			continue
		}
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("resolve %q under asset root: %w", rel, fs.ErrNotExist)
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
// gets a clear error rather than a stream of bytes. The returned Path is re-anchored to the asset root, so
// a location carrying a mount prefix (e.g. "/src/app/x.go") is reported back as the clean "app/x.go".
func ReadFile(root, rel string, maxBytes int) (File, error) {
	full, err := Resolve(root, rel)
	if err != nil {
		return File{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return File{}, err
	}
	if disp, err := filepath.Rel(filepath.Clean(root), full); err == nil {
		rel = filepath.ToSlash(disp)
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
