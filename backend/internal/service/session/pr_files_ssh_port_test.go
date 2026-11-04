package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// The observer stores a GitLab MR from an ssh:// origin on the web host
// (SSH port dropped), so the worktree remote must be identified the same way.
func TestPRWorkspaceRootMatchesGitLabSSHPortRemote(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "remote", "add", "origin", "ssh://git@host:30022/a/b/c.git")
	st := newFakeStore()
	rec := domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	pr := domain.PullRequest{Number: 1, URL: "https://host/a/b/c/-/merge_requests/1", Provider: "gitlab", Host: "host", Repo: "a/b/c"}

	svc := &Service{store: st, scm: newGitLabParserSCM(t, "host")}
	root, remote, err := svc.prWorkspaceRoot(context.Background(), rec, pr)
	if err != nil {
		t.Fatalf("prWorkspaceRoot = %v, want the ssh-port worktree", err)
	}
	if root != repo || remote != "origin" {
		t.Fatalf("prWorkspaceRoot = (%q, %q), want (%q, origin)", root, remote, repo)
	}
}

func TestPRWorkspaceRootMatchesGitLabSSHPortChildRepository(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, child, "init")
	runGit(t, child, "remote", "add", "origin", "https://other.test/x/y.git")
	runGit(t, child, "remote", "add", "gitlab", "ssh://git@host:30022/a/b/api.git")
	st := newFakeStore()
	st.worktrees["ao-1"] = []domain.SessionWorktreeRecord{{RepoName: "other", WorktreePath: filepath.Join(root, "missing")}, {RepoName: "child", WorktreePath: child}}
	rec := domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	pr := domain.PullRequest{Number: 3, URL: "https://host/a/b/api/-/merge_requests/3", Provider: "gitlab", Host: "host", Repo: "a/b/api"}

	svc := &Service{store: st, scm: newGitLabParserSCM(t, "host")}
	gotRoot, remote, err := svc.prWorkspaceRoot(context.Background(), rec, pr)
	if err != nil || gotRoot != child || remote != "gitlab" {
		t.Fatalf("prWorkspaceRoot = (%q, %q, %v), want (%q, gitlab)", gotRoot, remote, err, child)
	}
}

func TestPRWorkspaceRootKeepsProviderNeutralFallback(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "remote", "add", "origin", "https://example.test/acme/repo.git")
	rec := domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	// The GitLab provider does not allowlist example.test, so the
	// provider-neutral parse identifies the remote.
	svc := &Service{store: newFakeStore(), scm: newGitLabParserSCM(t, "host")}

	pr := domain.PullRequest{Number: 4, Provider: "gitlab", Host: "example.test", Repo: "acme/repo"}
	if root, remote, err := svc.prWorkspaceRoot(context.Background(), rec, pr); err != nil || root != repo || remote != "origin" {
		t.Fatalf("prWorkspaceRoot = (%q, %q, %v), want (%q, origin)", root, remote, err, repo)
	}

	pr.Host = "host"
	_, _, err := svc.prWorkspaceRoot(context.Background(), rec, pr)
	var apiErr *apierr.Error
	if err == nil || !errors.As(err, &apiErr) || apiErr.Code != "PR_SOURCE_REPOSITORY_NOT_FOUND" {
		t.Fatalf("prWorkspaceRoot for an unrelated host = %v, want PR_SOURCE_REPOSITORY_NOT_FOUND", err)
	}
}
