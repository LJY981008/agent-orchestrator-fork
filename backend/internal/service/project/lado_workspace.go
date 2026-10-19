package project

// LADO fork: clones that go into a workspace project (docs/LJY-PRD/TDD.md
// §5.1). A LADO project can keep several repositories, each its own checkout,
// under one workspace folder, such as back/ and front/ beside each other.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// LadoWorkspaceCloneInput is a repository to clone into a workspace project.
type LadoWorkspaceCloneInput struct {
	RemoteURL string
	// GitConfig holds key=value pairs for the clone's local config, set before
	// its first fetch.
	GitConfig []string
}

// maxLadoWorkspaceNameRunes keeps a new workspace's folder name usable on
// every platform LADO supports.
const maxLadoWorkspaceNameRunes = 100

// CloneIntoNewWorkspace creates the folder name under parent, clones the
// repository into it, and registers the folder as a workspace project whose
// first child is the clone. On failure the folder is removed again.
func (m *Service) CloneIntoNewWorkspace(ctx context.Context, parent, name string, in LadoWorkspaceCloneInput) (Project, error) {
	name = strings.TrimSpace(name)
	if err := validateLadoWorkspaceName(name); err != nil {
		return Project{}, err
	}
	parentPath, err := normalizePath(parent)
	if err != nil {
		return Project{}, err
	}
	root := filepath.Join(parentPath, name)
	if err := validateRepositorySetupPathSafety(root); err != nil {
		return Project{}, err
	}
	if err := os.MkdirAll(parentPath, 0o750); err != nil {
		return Project{}, apierr.Invalid("CLONE_DESTINATION_UNAVAILABLE", "AO could not create the destination folder.", nil)
	}
	// Mkdir fails on an existing folder, so a folder LADO did not create is
	// never removed below.
	if err := os.Mkdir(root, 0o750); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Project{}, apierr.Conflict("LADO_WORKSPACE_FOLDER_EXISTS", "A folder with this name already exists in the selected destination.", map[string]any{"path": root})
		}
		return Project{}, apierr.Invalid("CLONE_DESTINATION_UNAVAILABLE", "AO could not create the workspace folder.", map[string]any{"path": root})
	}
	created := root
	defer func() {
		if created != "" {
			_ = os.RemoveAll(created)
		}
	}()

	prepared, err := m.prepareClone(ctx, CloneInput{RemoteURL: in.RemoteURL, DestinationParent: root, GitConfig: in.GitConfig}, false)
	if err != nil {
		return Project{}, err
	}
	if !repoHasCommit(ctx, prepared.Path) {
		return Project{}, apierr.Invalid("CLONE_EMPTY_REPOSITORY", "AO needs a repository with at least one commit.", nil)
	}
	removeClonePreparationMarker(prepared.Path)
	project, err := m.Add(ctx, AddInput{Path: root, Name: &name, AsWorkspace: true})
	if err != nil {
		return Project{}, err
	}
	created = ""
	return project, nil
}

// CloneIntoWorkspace clones a repository into a registered workspace
// project's folder and registers it as one more child. The workspace root's
// .gitignore gains the child in a commit of its own. On failure the clone is
// removed again.
func (m *Service) CloneIntoWorkspace(ctx context.Context, id domain.ProjectID, in LadoWorkspaceCloneInput) (Project, error) {
	row, ok, err := m.store.GetProject(ctx, string(id))
	if err != nil {
		return Project{}, apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project")
	}
	if !ok || !row.ArchivedAt.IsZero() {
		return Project{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	if row.Kind != domain.ProjectKindWorkspace {
		return Project{}, apierr.Invalid("LADO_NOT_A_WORKSPACE", "Choose a workspace project to clone into.", map[string]any{"projectId": row.ID})
	}
	name, err := cloneRepositoryName(strings.TrimSpace(in.RemoteURL))
	if err != nil {
		return Project{}, err
	}
	if name == domain.RootWorkspaceRepoName {
		return Project{}, apierr.Invalid("WORKSPACE_CHILD_RESERVED_NAME", "Child repository name is reserved for internal use", map[string]any{"name": name})
	}
	if !isGitRepo(row.Path) {
		return Project{}, apierr.Invalid("LADO_WORKSPACE_ROOT_INVALID", "The workspace folder is missing or is no longer a Git repository.", map[string]any{"path": row.Path})
	}

	prepared, err := m.prepareClone(ctx, CloneInput{RemoteURL: in.RemoteURL, DestinationParent: row.Path, GitConfig: in.GitConfig}, false)
	if err != nil {
		return Project{}, err
	}
	cloned := prepared.Path
	defer func() {
		if cloned != "" {
			_ = os.RemoveAll(cloned)
		}
	}()
	if !repoHasCommit(ctx, cloned) {
		return Project{}, apierr.Invalid("CLONE_EMPTY_REPOSITORY", "AO needs a repository with at least one commit.", nil)
	}
	if err := validateWorkspaceChild(ctx, cloned); err != nil {
		return Project{}, err
	}

	m.addMu.Lock()
	defer m.addMu.Unlock()
	child := domain.WorkspaceRepoRecord{
		ProjectID:     domain.ProjectID(row.ID),
		Name:          name,
		RelativePath:  filepath.ToSlash(name),
		RepoOriginURL: resolveGitOriginURL(cloned),
		DefaultBranch: resolveWorkspaceChildDefaultBranch(ctx, cloned),
		RegisteredAt:  time.Now(),
		GitStatus:     domain.GitStatusReady,
	}
	if err := m.ignoreLadoWorkspaceChild(ctx, row.Path, child); err != nil {
		return Project{}, err
	}
	if err := m.store.AddLadoWorkspaceRepo(ctx, child); err != nil {
		return Project{}, apierr.Internal("PROJECT_ADD_FAILED", "Failed to register the workspace repository")
	}
	removeClonePreparationMarker(cloned)
	cloned = ""

	project := m.projectFromRow(ctx, row)
	if repos, err := m.store.ListWorkspaceRepos(ctx, row.ID); err == nil {
		project.WorkspaceRepos = workspaceReposFromRecords(row.Path, repos)
	}
	return project, nil
}

// ignoreLadoWorkspaceChild adds a new child to the workspace root's
// .gitignore and commits it, the way registration does. When that fails
// before the commit, the .gitignore is put back.
func (m *Service) ignoreLadoWorkspaceChild(ctx context.Context, root string, child domain.WorkspaceRepoRecord) error {
	path := filepath.Join(root, ".gitignore")
	original, readErr := os.ReadFile(path)
	headBefore, _ := gitOutput(ctx, root, "rev-parse", "HEAD")
	err := adoptWorkspaceParent(ctx, root, []domain.WorkspaceRepoRecord{child})
	if err == nil {
		return nil
	}
	if headAfter, _ := gitOutput(ctx, root, "rev-parse", "HEAD"); headAfter == headBefore {
		if readErr == nil {
			_ = os.WriteFile(path, original, 0o600)
		} else {
			_ = os.Remove(path)
		}
		if _, resetErr := gitOutput(ctx, root, "reset", "-q", "--", ".gitignore"); resetErr != nil {
			m.logger.Warn("project: could not unstage the workspace .gitignore", "path", root, "error", resetErr)
		}
	}
	return err
}

// validateLadoWorkspaceName accepts a folder name that is valid on Windows,
// macOS and Linux.
func validateLadoWorkspaceName(name string) error {
	if name == "" {
		return apierr.Invalid("LADO_WORKSPACE_NAME_REQUIRED", "Name the workspace folder.", nil)
	}
	invalid := name == "." || name == ".." || strings.HasSuffix(name, ".") ||
		utf8.RuneCountInString(name) > maxLadoWorkspaceNameRunes ||
		strings.ContainsAny(name, `/\<>:"|?*`) || isWindowsDeviceName(name)
	for _, r := range name {
		if unicode.IsControl(r) {
			invalid = true
		}
	}
	if invalid {
		return apierr.Invalid("LADO_WORKSPACE_NAME_INVALID", "Use a folder name without / \\ < > : \" | ? * that does not end with a period.", map[string]any{"name": name})
	}
	return nil
}

// isWindowsDeviceName reports names Windows reserves for devices, with or
// without an extension.
func isWindowsDeviceName(name string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}
