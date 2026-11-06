package domain

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// LADO #22: settings for the non-repository folders (assets) a workspace
// project copies into every session workspace.

// WorkspaceAssetsConfig controls how a workspace project's asset folders are
// copied into each session workspace.
type WorkspaceAssetsConfig struct {
	// Exclude lists glob patterns for paths that are not copied. A pattern
	// without a slash matches a path segment at any depth (".venv",
	// "node_modules"); a pattern with a slash matches a leading part of the
	// path relative to the workspace root ("tools/cache"). A null or missing
	// list uses DefaultWorkspaceAssetExcludes; an empty list copies
	// everything. It has no omitempty so an empty list round-trips as [].
	Exclude []string `json:"exclude"`
	// AbsoluteLinks decides what happens to a symbolic link whose target is
	// absolute or resolves outside the workspace root: "skip" (the default)
	// leaves it out of the session workspace, "keep" recreates it pointing at
	// the same target as the source link.
	AbsoluteLinks string `json:"absoluteLinks,omitempty" enum:"skip,keep"`
}

// Values of WorkspaceAssetsConfig.AbsoluteLinks.
const (
	WorkspaceAssetLinksSkip = "skip"
	WorkspaceAssetLinksKeep = "keep"
)

// DefaultWorkspaceAssetExcludes is the exclude list used when a project sets
// none: virtual environments, dependency folders and caches that are large,
// rebuildable and often hold absolute symbolic links. The settings screen
// shows a copy of this list; keep the two in step.
var DefaultWorkspaceAssetExcludes = []string{
	".venv",
	"venv",
	"node_modules",
	"__pycache__",
	".cache",
	".pytest_cache",
	".mypy_cache",
	".ruff_cache",
	".tox",
	".next",
	".turbo",
	".parcel-cache",
}

// Limits for WorkspaceAssetsConfig.Exclude.
const (
	WorkspaceAssetExcludeMaxItems = 100
	WorkspaceAssetExcludeMaxLen   = 200
)

// ResolvedWorkspaceAssets returns the exclude patterns and link policy a
// session workspace uses. A missing config or a null exclude list yields a
// copy of DefaultWorkspaceAssetExcludes; a non-nil empty list yields an empty
// non-nil list (copy everything).
func (c ProjectConfig) ResolvedWorkspaceAssets() (exclude []string, keepAbsoluteLinks bool) {
	assets := c.WorkspaceAssets
	if assets == nil || assets.Exclude == nil {
		exclude = slices.Clone(DefaultWorkspaceAssetExcludes)
	} else {
		exclude = append([]string{}, assets.Exclude...)
	}
	if assets != nil {
		keepAbsoluteLinks = assets.AbsoluteLinks == WorkspaceAssetLinksKeep
	}
	return exclude, keepAbsoluteLinks
}

// WorkspaceAssetExcluded reports whether rel, a path relative to the
// workspace root (either separator), matches one of patterns. Patterns
// without a slash are matched against every path segment; patterns with a
// slash are matched against each leading part of rel, so a match on a folder
// also excludes everything below it. Malformed patterns never match.
func WorkspaceAssetExcluded(rel string, patterns []string) bool {
	segments := workspaceAssetSegments(rel)
	if len(segments) == 0 {
		return false
	}
	for _, raw := range patterns {
		pattern := strings.TrimRight(raw, "/")
		if pattern == "" {
			continue
		}
		if !strings.Contains(pattern, "/") {
			for _, seg := range segments {
				if ok, err := path.Match(pattern, seg); err == nil && ok {
					return true
				}
			}
			continue
		}
		depth := strings.Count(pattern, "/") + 1
		if depth > len(segments) {
			continue
		}
		if ok, err := path.Match(pattern, strings.Join(segments[:depth], "/")); err == nil && ok {
			return true
		}
	}
	return false
}

func workspaceAssetSegments(rel string) []string {
	rel = strings.ReplaceAll(rel, `\`, "/")
	var out []string
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." {
			continue
		}
		out = append(out, seg)
	}
	return out
}

func validateWorkspaceAssets(c *WorkspaceAssetsConfig) error {
	if c == nil {
		return nil
	}
	switch c.AbsoluteLinks {
	case "", WorkspaceAssetLinksSkip, WorkspaceAssetLinksKeep:
	default:
		return fmt.Errorf("workspaceAssets.absoluteLinks: unknown value %q (want skip or keep)", c.AbsoluteLinks)
	}
	if len(c.Exclude) > WorkspaceAssetExcludeMaxItems {
		return fmt.Errorf("workspaceAssets.exclude: at most %d patterns", WorkspaceAssetExcludeMaxItems)
	}
	for i, pattern := range c.Exclude {
		if err := validateWorkspaceAssetPattern(pattern); err != nil {
			return fmt.Errorf("workspaceAssets.exclude[%d] %q: %w", i, pattern, err)
		}
	}
	return nil
}

func validateWorkspaceAssetPattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return errors.New("must not be empty")
	}
	if strings.TrimSpace(pattern) != pattern {
		return errors.New("must not have leading or trailing whitespace")
	}
	if utf8.RuneCountInString(pattern) > WorkspaceAssetExcludeMaxLen {
		return fmt.Errorf("must be at most %d characters", WorkspaceAssetExcludeMaxLen)
	}
	if strings.ContainsRune(pattern, '\x00') {
		return errors.New("must not contain NUL")
	}
	if strings.HasPrefix(pattern, "/") || strings.HasPrefix(pattern, `\`) ||
		len(pattern) >= 2 && pattern[1] == ':' && isASCIILetter(pattern[0]) {
		return errors.New("must be relative to the workspace root")
	}
	trimmed := strings.TrimRight(pattern, "/")
	for _, seg := range strings.Split(trimmed, "/") {
		switch seg {
		case "..":
			return errors.New("must not contain '..'")
		case "", ".":
			return errors.New("must not contain empty or '.' path segments")
		}
	}
	if _, err := path.Match(trimmed, ""); err != nil {
		return errors.New("invalid glob pattern")
	}
	return nil
}

func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
