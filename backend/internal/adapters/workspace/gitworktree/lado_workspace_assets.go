package gitworktree

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LADO #22: workspace assets are copied with an exclude list and a link
// policy instead of failing the whole session start on the first link that
// points outside the workspace (a virtualenv's bin/python, for example).
//
// The copy only touches the file system; it never runs git.

const ladoAssetCopyFix = "exclude it in Project settings → Scripts → Workspace folders, or remove it"

// ladoAssetCopy carries one CreateWorkspaceProject asset copy.
type ladoAssetCopy struct {
	sourceRoot      string
	destinationRoot string
	managedRoot     string
	// exclude holds domain.WorkspaceAssetExcluded patterns. The caller has
	// already resolved project defaults; nil excludes nothing.
	exclude []string
	// keepOutsideLinks recreates links whose target is absolute or outside
	// the workspace root, pointing where the source link points.
	keepOutsideLinks bool

	skipped      []string
	skippedCount int
}

// ladoCopyWorkspaceAssets copies assets from the canonical workspace root to
// a session workspace root. It returns the asset links it left out (relative
// to the workspace root, at most ports.MaxSkippedAssetLinks) and their count.
func (w *Workspace) ladoCopyWorkspaceAssets(sourceRoot, destinationRoot string, assets []ports.WorkspaceProjectAssetConfig, exclude []string, keepOutsideLinks bool) ([]string, int, error) {
	c := &ladoAssetCopy{
		sourceRoot:       filepath.Clean(sourceRoot),
		destinationRoot:  filepath.Clean(destinationRoot),
		managedRoot:      w.managedRoot,
		exclude:          exclude,
		keepOutsideLinks: keepOutsideLinks,
	}
	for _, asset := range assets {
		if err := c.copyAsset(asset); err != nil {
			return c.skipped, c.skippedCount, &ports.WorkspaceSetupError{
				Summary: "could not copy workspace folder " + asset.RelativePath,
				Fix:     ladoAssetCopyFix,
				Err:     err,
			}
		}
	}
	return c.skipped, c.skippedCount, nil
}

func (c *ladoAssetCopy) copyAsset(asset ports.WorkspaceProjectAssetConfig) error {
	rel, err := cleanRelativePath(asset.RelativePath)
	if err != nil || hasWorkspaceManagedComponent(rel) {
		return fmt.Errorf("gitworktree: workspace asset %q: %w", asset.RelativePath, ErrUnsafePath)
	}
	if domain.WorkspaceAssetExcluded(rel, c.exclude) {
		return nil
	}
	source, err := physicalAbs(asset.SourcePath)
	if err != nil || !assetPathWithin(c.sourceRoot, source) {
		return fmt.Errorf("gitworktree: workspace asset %q source: %w", asset.RelativePath, ErrUnsafePath)
	}
	destination := filepath.Join(c.destinationRoot, filepath.FromSlash(rel))
	if !assetPathWithin(c.destinationRoot, destination) {
		return fmt.Errorf("gitworktree: workspace asset %q destination: %w", asset.RelativePath, ErrUnsafePath)
	}
	if err := c.copyTree(source, destination); err != nil {
		return fmt.Errorf("gitworktree: copy workspace asset %q: %w", asset.RelativePath, err)
	}
	return nil
}

func (c *ladoAssetCopy) copyTree(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return c.copyLink(source, destination)
	}
	if info.IsDir() {
		if existing, err := os.Lstat(destination); err == nil {
			if !existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("cannot overlay directory on non-directory %q", destination)
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
				return err
			}
		} else {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".git" || entry.Name() == ".ao" {
				continue
			}
			childDestination := filepath.Join(destination, entry.Name())
			if domain.WorkspaceAssetExcluded(c.rel(childDestination), c.exclude) {
				continue
			}
			if err := c.copyTree(filepath.Join(source, entry.Name()), childDestination); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported file type at %q", source)
	}
	return copyAssetFile(source, destination, info.Mode().Perm())
}

// copyLink recreates the link at source, or records it as skipped when the
// policy leaves it out. A skipped link is not a failure.
func (c *ladoAssetCopy) copyLink(source, destination string) error {
	target, err := os.Readlink(source)
	if err != nil {
		return err
	}
	linkTarget, ok := c.linkTarget(source, target)
	if !ok {
		c.skip(destination)
		return nil
	}
	if existing, err := os.Lstat(destination); err == nil {
		if existing.IsDir() {
			return fmt.Errorf("cannot overlay symlink on directory %q", destination)
		}
		if err := os.Remove(destination); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(linkTarget, destination)
}

// linkTarget decides what the session copy of the link at source points to.
// A relative target that resolves, all the way, inside the workspace root is
// kept as the same string. Any other link is left out unless the policy keeps
// such links; a kept link points where the source link points, as an absolute
// path, because a relative string would resolve against the session path and
// reach another session's worktree or AO's data. A kept link whose final
// target passes through .git/.ao or lies in AO's managed root is still left
// out.
func (c *ladoAssetCopy) linkTarget(source, target string) (string, bool) {
	if !filepath.IsAbs(target) {
		resolved := filepath.Clean(filepath.Join(filepath.Dir(source), target))
		if assetPathWithin(c.sourceRoot, resolved) {
			if hasWorkspaceManagedComponent(relativeOrEmpty(c.sourceRoot, resolved)) {
				return "", false
			}
			if physical, err := filepath.EvalSymlinks(resolved); err == nil && assetPathWithin(c.sourceRoot, physical) {
				if hasWorkspaceManagedComponent(relativeOrEmpty(c.sourceRoot, physical)) {
					return "", false
				}
				return target, true
			}
		}
	}
	if !c.keepOutsideLinks {
		return "", false
	}
	absolute := filepath.Clean(target)
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Clean(filepath.Join(filepath.Dir(source), target))
	}
	if !c.keptTargetAllowed(absolute) {
		return "", false
	}
	return absolute, true
}

func (c *ladoAssetCopy) keptTargetAllowed(target string) bool {
	candidates := []string{target}
	if physical, err := filepath.EvalSymlinks(target); err == nil {
		candidates = append(candidates, filepath.Clean(physical))
	}
	for _, candidate := range candidates {
		if hasWorkspaceManagedComponent(candidate) ||
			assetPathWithin(c.managedRoot, candidate) ||
			assetPathWithin(c.destinationRoot, candidate) {
			return false
		}
	}
	return true
}

func (c *ladoAssetCopy) skip(destination string) {
	c.skippedCount++
	if len(c.skipped) < ports.MaxSkippedAssetLinks {
		c.skipped = append(c.skipped, c.rel(destination))
	}
}

// rel is destination relative to the session workspace root, slash separated.
func (c *ladoAssetCopy) rel(destination string) string {
	return filepath.ToSlash(relativeOrEmpty(c.destinationRoot, destination))
}

func copyAssetFile(source, destination string, perm os.FileMode) error {
	if existing, err := os.Lstat(destination); err == nil {
		if !existing.Mode().IsRegular() {
			return fmt.Errorf("cannot overlay file on non-regular path %q", destination)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func assetPathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func relativeOrEmpty(root, path string) string {
	rel, _ := filepath.Rel(root, path)
	return rel
}

func hasWorkspaceManagedComponent(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if component == ".git" || component == ".ao" {
			return true
		}
	}
	return false
}
