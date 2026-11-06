package importer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// LADO #22: import validation hints at a missing git-lfs without blocking.

func ladoSetGitLFSInstalled(t *testing.T, installed bool) {
	t.Helper()
	previous := ladoGitLFSInstalled
	ladoGitLFSInstalled = func(context.Context, string) bool { return installed }
	t.Cleanup(func() { ladoGitLFSInstalled = previous })
}

// ladoLFSRepo makes a committed repo whose .gitattributes asks for the lfs
// filter. Only .gitattributes is committed, so a git-lfs install with a global
// filter on this computer never turns a file into a pointer.
func ladoLFSRepo(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", ".gitattributes"},
		{"-c", "user.email=ao@example.com", "-c", "user.name=AO Test", "commit", "-m", "lfs"},
		{"remote", "add", "origin", "https://example.invalid/front.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

func TestLadoValidateWorkspaceImportFlagsMissingGitLFS(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "not installed", true: "installed"}[installed], func(t *testing.T) {
			ladoSetGitLFSInstalled(t, installed)
			ctx := context.Background()
			root := t.TempDir()
			gitRepoWithCommitWithOrigin(t, filepath.Join(root, "api"), "https://example.invalid/api.git")
			ladoLFSRepo(t, filepath.Join(root, "front"))
			svc := New(Deps{Store: newFakeStore()})

			result, err := svc.Validate(ctx, ImportValidationInput{ImportKind: ImportKindWorkspace, Path: root})
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !result.IsValid || result.NextStep != ImportNextStepContinue || len(result.ChildRepos) != 2 {
				t.Fatalf("result = %#v, want a valid workspace that continues", result)
			}
			for _, child := range result.ChildRepos {
				want := !installed && filepath.Base(child.RepoPath) == "front"
				if child.GitLFSMissing != want {
					t.Fatalf("%s gitLfsMissing = %v, want %v", child.RepoPath, child.GitLFSMissing, want)
				}
				if len(child.BlockingErrors) != 0 {
					t.Fatalf("%s blocking errors = %v, want none", child.RepoPath, child.BlockingErrors)
				}
			}
		})
	}
}

func TestLadoValidateProjectImportFlagsMissingGitLFS(t *testing.T) {
	ladoSetGitLFSInstalled(t, false)
	repo := t.TempDir()
	ladoLFSRepo(t, repo)
	svc := New(Deps{Store: newFakeStore()})

	result, err := svc.Validate(context.Background(), ImportValidationInput{ImportKind: ImportKindProject, Path: repo})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !result.Root.GitLFSMissing {
		t.Fatalf("root = %#v, want gitLfsMissing", result.Root)
	}
	if len(result.BlockingErrors) != 0 || result.NextStep == ImportNextStepError {
		t.Fatalf("result = %#v, want the hint not to block", result)
	}
}

func TestLadoValidateImportWithoutCommitSkipsGitLFSCheck(t *testing.T) {
	ladoSetGitLFSInstalled(t, false)
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	svc := New(Deps{Store: newFakeStore()})
	result, err := svc.Validate(context.Background(), ImportValidationInput{ImportKind: ImportKindProject, Path: repo})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if result.Root.GitLFSMissing {
		t.Fatalf("root = %#v, want no git-lfs hint for an unborn repository", result.Root)
	}
}
