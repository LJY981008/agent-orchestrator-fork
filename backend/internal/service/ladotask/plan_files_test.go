package ladotask

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func partsPlan(parts map[string][]string) domain.LadoPlan {
	var plan domain.LadoPlan
	for _, id := range []string{"a", "b", "c"} {
		if paths, ok := parts[id]; ok {
			plan.Parts = append(plan.Parts, domain.LadoPlanPart{ID: id, AffectedPaths: paths})
		}
	}
	return plan
}

func TestCountPartFilesInOneRepository(t *testing.T) {
	plan := partsPlan(map[string][]string{
		"a": {"backend/internal/**"},
		"b": {"frontend/src"},
		"c": {"docs/*.md"},
	})
	files := []string{
		"backend/internal/a.go", "backend/internal/x/b.go", "backend/main.go",
		"frontend/src/a.ts", "frontend/srcs/b.ts", "docs/a.md", "docs/sub/b.md", "README.md",
	}
	got := countPartFiles(plan, files, "")
	if want := map[string]int{"a": 2, "b": 1, "c": 1}; !maps.Equal(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
}

func TestCountPartFilesInAWorkspaceRepository(t *testing.T) {
	plan := partsPlan(map[string][]string{
		"a": {"back/internal/**"},
		"b": {"front/**"},
	})
	files := []string{"internal/a.go", "internal/b.go", "web/x.go"}
	got := countPartFiles(plan, files, "back")
	if want := map[string]int{"a": 2, "b": 0}; !maps.Equal(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
	if got := countPartFiles(plan, files, "/back/"); !maps.Equal(got, map[string]int{"a": 2, "b": 0}) {
		t.Fatalf("counts with slashes around the folder = %v", got)
	}
}

// A file under several of a part's paths counts once.
func TestCountPartFilesCountsAFileOnce(t *testing.T) {
	plan := partsPlan(map[string][]string{
		"a": {"backend/**", "backend/internal/**", "**/*.go", "./backend//internal"},
	})
	files := []string{"backend/internal/a.go", "backend/b.go", "tools/c.go", "web/d.ts"}
	if got := countPartFiles(plan, files, ""); !maps.Equal(got, map[string]int{"a": 3}) {
		t.Fatalf("counts = %v, want a: 3", got)
	}
}

func TestPartFilesCacheEmptiesWhenFull(t *testing.T) {
	var cache partFilesCache
	for i := range partFilesCacheSize {
		cache.put(domain.LadoPlanRevisionID(fmt.Sprint(i)), map[string]int{"a": i})
	}
	cache.put("failed", nil)
	if counts, ok := cache.get("failed"); !ok || counts != nil {
		t.Fatalf("failed revision = %v, %v, want kept as not counted", counts, ok)
	}
	if _, ok := cache.get("0"); ok {
		t.Fatal("the cache kept more than its size")
	}
}

// execGit lists every file of a commit, unquoted, and not the files added
// after it.
func TestExecGitListsTheFilesOfACommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	for _, name := range []string{"a.go", "sub/b c.go", "sub/파일.md"} {
		write(name)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "one")
	sha := run("rev-parse", "HEAD")
	write("later.go")
	run("add", "-A")
	run("commit", "-q", "-m", "two")

	files, err := execGit{}.ListFiles(context.Background(), dir, sha)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	slices.Sort(files)
	if want := []string{"a.go", "sub/b c.go", "sub/파일.md"}; !slices.Equal(files, want) {
		t.Fatalf("files = %q, want %q", files, want)
	}
	if _, err := (execGit{}).ListFiles(context.Background(), dir, strings.Repeat("0", 40)); err == nil {
		t.Fatal("ListFiles of a missing commit gave no error")
	}
}
