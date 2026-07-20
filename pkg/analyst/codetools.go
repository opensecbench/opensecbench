package analyst

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// Source-code read tools (ADR-0020). They let an agent actually read and navigate a source_repo asset —
// read a file, list a directory, grep, find files — instead of only running capabilities over it. Every
// call is project-scoped (the asset must belong to the thread's project) and path-confined to the asset
// root (no traversal, no symlink escape). They are auto-approved reads, but the data-egress guard blocks
// them for a private asset under a strict policy with an external provider (see service.executeFor).

const (
	maxReadBytes    = 64 * 1024       // per read_file response
	maxGrepMatches  = 100             // default grep_code cap
	maxFindResults  = 500             // find_files cap
	grepFileMaxSize = 2 * 1024 * 1024 // skip files larger than this when grepping
)

// skipDirs are noise directories excluded from grep_code / find_files walks.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true}

// resolveSourceAsset loads a source_repo asset, verifying it belongs to the thread's project and has a
// readable location.
func resolveSourceAsset(ctx context.Context, deps ExecDeps, tool string, call agent.ToolCall) (model.Asset, error) {
	projectID, err := requireProject(deps, tool)
	if err != nil {
		return model.Asset{}, err
	}
	assetID := stringArg(call, "asset")
	if assetID == "" {
		return model.Asset{}, fmt.Errorf("%s requires 'asset'", tool)
	}
	asset, err := deps.p().GetAsset(ctx, assetID)
	if err != nil {
		return model.Asset{}, err
	}
	if asset.Type != model.AssetSourceRepo {
		return model.Asset{}, fmt.Errorf("asset %s is %s; %s reads source_repo assets", asset.ID, asset.Type, tool)
	}
	app, err := deps.p().GetApplication(ctx, asset.ApplicationID)
	if err != nil {
		return model.Asset{}, err
	}
	if app.ProjectID != projectID {
		return model.Asset{}, errors.New("asset belongs to a different project")
	}
	if asset.Location == "" {
		return model.Asset{}, fmt.Errorf("asset %s has no location on disk", asset.ID)
	}
	return asset, nil
}

// confinedPath resolves rel against the asset root and refuses anything that escapes it.
func confinedPath(root, rel string) (string, error) {
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

// readFile returns a file's contents (optionally a line range), capped in size.
func readFile(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	asset, err := resolveSourceAsset(ctx, deps, "read_file", call)
	if err != nil {
		return "", err
	}
	rel := stringArg(call, "path")
	if rel == "" {
		return "", errors.New("read_file requires 'path'")
	}
	full, err := confinedPath(asset.Location, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory; use list_dir", rel)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}

	content := string(data)
	var truncated bool
	// Optional line window.
	if off, lim := intArg(call, "offset"), intArg(call, "limit"); off > 0 || lim > 0 {
		lines := strings.Split(content, "\n")
		start := off
		if start < 0 {
			start = 0
		}
		if start > len(lines) {
			start = len(lines)
		}
		end := len(lines)
		if lim > 0 && start+lim < end {
			end = start + lim
		}
		content = strings.Join(lines[start:end], "\n")
	}
	if len(content) > maxReadBytes {
		content = content[:maxReadBytes]
		truncated = true
	}
	return jsonify(map[string]any{
		"path":      rel,
		"bytes":     info.Size(),
		"content":   content,
		"truncated": truncated,
	}, nil)
}

// listDir lists one directory's entries.
func listDir(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	asset, err := resolveSourceAsset(ctx, deps, "list_dir", call)
	if err != nil {
		return "", err
	}
	full, err := confinedPath(asset.Location, stringArg(call, "path"))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return "", err
	}
	type entry struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size,omitempty"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		kind := "file"
		var size int64
		if e.IsDir() {
			kind = "dir"
		} else if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, entry{Name: e.Name(), Type: kind, Size: size})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return jsonify(out, nil)
}

// grepCode searches the asset tree for a regular expression.
func grepCode(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	asset, err := resolveSourceAsset(ctx, deps, "grep_code", call)
	if err != nil {
		return "", err
	}
	pattern := stringArg(call, "pattern")
	if pattern == "" {
		return "", errors.New("grep_code requires 'pattern'")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %w", err)
	}
	glob := stringArg(call, "glob")
	limit := intArg(call, "max")
	if limit <= 0 {
		limit = maxGrepMatches
	}

	root := filepath.Clean(asset.Location)
	type match struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}
	matches := []match{}
	truncated := false

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if glob != "" {
			if ok, _ := filepath.Match(glob, d.Name()); !ok {
				return nil
			}
		}
		if info, err := d.Info(); err == nil && info.Size() > grepFileMaxSize {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || !isProbablyText(data) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) {
				if len(matches) >= limit {
					truncated = true
					return filepath.SkipAll
				}
				matches = append(matches, match{Path: rel, Line: i + 1, Text: strings.TrimSpace(line)})
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		return "", walkErr
	}
	return jsonify(map[string]any{"matches": matches, "count": len(matches), "truncated": truncated}, nil)
}

// findFiles lists files whose name matches a glob (or all files, capped, when no glob).
func findFiles(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	asset, err := resolveSourceAsset(ctx, deps, "find_files", call)
	if err != nil {
		return "", err
	}
	glob := stringArg(call, "glob")
	limit := intArg(call, "max")
	if limit <= 0 {
		limit = maxFindResults
	}
	root := filepath.Clean(asset.Location)
	paths := []string{}
	truncated := false

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if glob != "" {
			if ok, _ := filepath.Match(glob, d.Name()); !ok {
				return nil
			}
		}
		if len(paths) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(root, p)
		paths = append(paths, rel)
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		return "", walkErr
	}
	sort.Strings(paths)
	return jsonify(map[string]any{"files": paths, "count": len(paths), "truncated": truncated}, nil)
}

// isProbablyText rejects binary blobs (NUL byte) and invalid UTF-8 so grep only scans source.
func isProbablyText(b []byte) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	if len(b) > 1024 {
		b = b[:1024]
	}
	return utf8.Valid(b)
}
