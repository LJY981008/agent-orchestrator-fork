package project_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// LADO fork: clones into a workspace project (docs/LJY-PRD/TDD.md §5.1).

func newLadoWorkspaceManager(t *testing.T) (*project.Service, *sqlite.Store) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	configureCommitter(t)
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return project.New(store), store
}

// ladoRemote is a file URL to a new repository named name with one commit.
func ladoRemote(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	gitRepoWithCommitNoOrigin(t, dir)
	return (&url.URL{Scheme: "file", Path: dir}).String()
}

func ladoAPICode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestLadoCloneIntoNewWorkspace(t *testing.T) {
	ctx := context.Background()
	m, store := newLadoWorkspaceManager(t)
	back := ladoRemote(t, "back")
	parent := t.TempDir()
	command := "ssh -i '/keys/id_lado' -o IdentitiesOnly=yes"

	created, err := m.CloneIntoNewWorkspace(ctx, parent, " product ", project.LadoWorkspaceCloneInput{
		RemoteURL: back, GitConfig: []string{"core.sshCommand=" + command},
	})
	if err != nil {
		t.Fatalf("CloneIntoNewWorkspace: %v", err)
	}
	root := filepath.Join(parent, "product")
	if created.Kind != domain.ProjectKindWorkspace || created.Path != root || created.Name != "product" {
		t.Fatalf("project = %+v, want the workspace product at %s", created, root)
	}
	if len(created.WorkspaceRepos) != 1 || created.WorkspaceRepos[0].Name != "back" || created.WorkspaceRepos[0].Repo != back {
		t.Fatalf("workspace repos = %+v, want back with its origin", created.WorkspaceRepos)
	}
	if got := gitOut(t, filepath.Join(root, "back"), "config", "--local", "core.sshCommand"); got != command {
		t.Fatalf("back core.sshCommand = %q, want %q", got, command)
	}
	if _, err := os.Stat(filepath.Join(root, "back", ".git", ".ao-clone-prepared")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clone preparation marker left behind: %v", err)
	}
	if status := gitOut(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("workspace root is not clean:\n%s", status)
	}
	if listed, err := store.ListWorkspaceRepos(ctx, string(created.ID)); err != nil || len(listed) != 1 {
		t.Fatalf("registered children = %+v, %v", listed, err)
	}
}

func TestLadoCloneIntoNewWorkspaceKeepsAnExistingFolder(t *testing.T) {
	ctx := context.Background()
	m, _ := newLadoWorkspaceManager(t)
	parent := t.TempDir()
	existing := filepath.Join(parent, "product")
	if err := os.Mkdir(existing, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "notes.txt"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := m.CloneIntoNewWorkspace(ctx, parent, "product", project.LadoWorkspaceCloneInput{RemoteURL: ladoRemote(t, "back")})
	if ladoAPICode(err) != "LADO_WORKSPACE_FOLDER_EXISTS" {
		t.Fatalf("err = %v, want LADO_WORKSPACE_FOLDER_EXISTS", err)
	}
	if _, err := os.Stat(filepath.Join(existing, "notes.txt")); err != nil {
		t.Fatalf("the existing folder was touched: %v", err)
	}
}

func TestLadoCloneIntoNewWorkspaceRemovesTheFolderOnFailure(t *testing.T) {
	ctx := context.Background()
	m, _ := newLadoWorkspaceManager(t)
	parent := t.TempDir()
	missing := (&url.URL{Scheme: "file", Path: filepath.Join(t.TempDir(), "missing")}).String()
	if _, err := m.CloneIntoNewWorkspace(ctx, parent, "product", project.LadoWorkspaceCloneInput{RemoteURL: missing}); ladoAPICode(err) != "GIT_CLONE_FAILED" {
		t.Fatalf("err = %v, want GIT_CLONE_FAILED", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "product")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace folder left behind: %v", err)
	}
	if listed, err := m.List(ctx); err != nil || len(listed) != 0 {
		t.Fatalf("projects = %+v, %v; want none", listed, err)
	}
}

func TestLadoWorkspaceNames(t *testing.T) {
	ctx := context.Background()
	m, _ := newLadoWorkspaceManager(t)
	parent := t.TempDir()
	for name, code := range map[string]string{
		"":          "LADO_WORKSPACE_NAME_REQUIRED",
		"..":        "LADO_WORKSPACE_NAME_INVALID",
		"a/b":       "LADO_WORKSPACE_NAME_INVALID",
		`a\b`:       "LADO_WORKSPACE_NAME_INVALID",
		"what?":     "LADO_WORKSPACE_NAME_INVALID",
		"trailing.": "LADO_WORKSPACE_NAME_INVALID",
		"CON":       "LADO_WORKSPACE_NAME_INVALID",
		"lpt1.txt":  "LADO_WORKSPACE_NAME_INVALID",
		"tab\there": "LADO_WORKSPACE_NAME_INVALID",
	} {
		if _, err := m.CloneIntoNewWorkspace(ctx, parent, name, project.LadoWorkspaceCloneInput{RemoteURL: "file:///unused/back"}); ladoAPICode(err) != code {
			t.Errorf("name %q err = %v, want %s", name, err, code)
		}
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Fatalf("refused names created %d entries", len(entries))
	}
}

func TestLadoCloneIntoWorkspaceAddsARepository(t *testing.T) {
	ctx := context.Background()
	m, store := newLadoWorkspaceManager(t)
	parent := t.TempDir()
	created, err := m.CloneIntoNewWorkspace(ctx, parent, "product", project.LadoWorkspaceCloneInput{RemoteURL: ladoRemote(t, "back")})
	if err != nil {
		t.Fatalf("CloneIntoNewWorkspace: %v", err)
	}
	before, err := store.ListWorkspaceRepos(ctx, string(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	front := ladoRemote(t, "front")

	updated, err := m.CloneIntoWorkspace(ctx, created.ID, project.LadoWorkspaceCloneInput{RemoteURL: front})
	if err != nil {
		t.Fatalf("CloneIntoWorkspace: %v", err)
	}
	if len(updated.WorkspaceRepos) != 2 || updated.WorkspaceRepos[0].Name != "back" || updated.WorkspaceRepos[1].Name != "front" || updated.WorkspaceRepos[1].Repo != front {
		t.Fatalf("workspace repos = %+v, want back and front", updated.WorkspaceRepos)
	}
	after, err := store.ListWorkspaceRepos(ctx, string(created.ID))
	if err != nil || len(after) != 2 {
		t.Fatalf("registered children = %+v, %v", after, err)
	}
	if !after[0].RegisteredAt.Equal(before[0].RegisteredAt) || after[1].GitStatus != domain.GitStatusReady {
		t.Fatalf("children = %+v, want back untouched and front ready", after)
	}
	root := filepath.Join(parent, "product")
	ignored, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil || !strings.Contains(string(ignored), "/front/") {
		t.Fatalf(".gitignore = %q, %v; want /front/ ignored", ignored, err)
	}
	if status := gitOut(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("workspace root is not clean:\n%s", status)
	}
	if got := gitOut(t, root, "log", "-1", "--format=%s"); got != "chore: configure AO workspace ignores" {
		t.Fatalf("last workspace commit = %q, want the ignore commit", got)
	}
	if got, err := m.Get(ctx, created.ID); err != nil || got.Project == nil || got.Project.Name != "product" {
		t.Fatalf("Get = %+v, %v; want the project row unchanged", got, err)
	}

	if _, err := m.CloneIntoWorkspace(ctx, created.ID, project.LadoWorkspaceCloneInput{RemoteURL: front}); ladoAPICode(err) != "CLONE_DESTINATION_EXISTS" {
		t.Fatalf("second clone err = %v, want CLONE_DESTINATION_EXISTS", err)
	}
}

func TestLadoCloneIntoWorkspaceNeedsAWorkspace(t *testing.T) {
	ctx := context.Background()
	m, _ := newLadoWorkspaceManager(t)
	single, err := m.Clone(ctx, project.CloneInput{RemoteURL: ladoRemote(t, "solo"), DestinationParent: t.TempDir()})
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := m.CloneIntoWorkspace(ctx, single.ID, project.LadoWorkspaceCloneInput{RemoteURL: ladoRemote(t, "front")}); ladoAPICode(err) != "LADO_NOT_A_WORKSPACE" {
		t.Fatalf("err = %v, want LADO_NOT_A_WORKSPACE", err)
	}
	if _, err := m.CloneIntoWorkspace(ctx, "missing", project.LadoWorkspaceCloneInput{RemoteURL: ladoRemote(t, "front")}); ladoAPICode(err) != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want PROJECT_NOT_FOUND", err)
	}
}

func TestLadoCloneIntoWorkspaceRemovesTheCloneOnFailure(t *testing.T) {
	ctx := context.Background()
	m, store := newLadoWorkspaceManager(t)
	parent := t.TempDir()
	created, err := m.CloneIntoNewWorkspace(ctx, parent, "product", project.LadoWorkspaceCloneInput{RemoteURL: ladoRemote(t, "back")})
	if err != nil {
		t.Fatalf("CloneIntoNewWorkspace: %v", err)
	}
	root := filepath.Join(parent, "product")
	original, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	// A commit hook that refuses every commit makes the ignore commit fail.
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CloneIntoWorkspace(ctx, created.ID, project.LadoWorkspaceCloneInput{RemoteURL: ladoRemote(t, "front")}); ladoAPICode(err) != "WORKSPACE_PARENT_COMMIT_FAILED" {
		t.Fatalf("err = %v, want WORKSPACE_PARENT_COMMIT_FAILED", err)
	}
	if _, err := os.Stat(filepath.Join(root, "front")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clone left behind: %v", err)
	}
	if now, err := os.ReadFile(filepath.Join(root, ".gitignore")); err != nil || string(now) != string(original) {
		t.Fatalf(".gitignore = %q, %v; want it put back", now, err)
	}
	if status := gitOut(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("workspace root is not clean:\n%s", status)
	}
	if listed, err := store.ListWorkspaceRepos(ctx, string(created.ID)); err != nil || len(listed) != 1 {
		t.Fatalf("registered children = %+v, %v; want only back", listed, err)
	}
}
