package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LADO #22: workspace asset exclude list and link policy.

type assetFixture struct {
	git      string
	tmp      string
	rootRepo string
	ws       *Workspace
}

func newAssetFixture(t *testing.T) assetFixture {
	t.Helper()
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	return assetFixture{git: git, tmp: tmp, rootRepo: rootRepo, ws: ws}
}

func (f assetFixture) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(f.rootRepo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f assetFixture) link(t *testing.T, rel, target string) {
	t.Helper()
	p := filepath.Join(f.rootRepo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

func (f assetFixture) create(t *testing.T, session string, assets []string, exclude []string, keep bool) (ports.WorkspaceProjectInfo, error) {
	t.Helper()
	configs := make([]ports.WorkspaceProjectAssetConfig, 0, len(assets))
	for _, rel := range assets {
		configs = append(configs, ports.WorkspaceProjectAssetConfig{RelativePath: rel, SourcePath: filepath.Join(f.rootRepo, filepath.FromSlash(rel))})
	}
	return f.ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: domain.SessionID(session), Kind: "worker", Branch: "ao/" + session,
		RootRepoPath:           f.rootRepo,
		Assets:                 configs,
		AssetExclude:           exclude,
		KeepAbsoluteAssetLinks: keep,
	})
}

// skipUnixLinkTargets skips tests whose link targets (/usr/bin/python3) are
// absolute only on Unix.
func skipUnixLinkTargets(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix absolute link targets")
	}
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists (err %v), want absent", path, err)
	}
}

func requirePresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s missing: %v", path, err)
	}
}

func TestLadoWorkspaceAssetsDefaultExcludesSkipVenvAndNestedNodeModules(t *testing.T) {
	f := newAssetFixture(t)
	f.write(t, ".venv/pyvenv.cfg", "home = /usr/bin\n")
	f.write(t, "web/app.js", "app\n")
	f.write(t, "web/pkg/node_modules/dep/index.js", "dep\n")
	f.write(t, "web/pkg/__pycache__/x.pyc", "pyc\n")
	f.write(t, "web/pkg/keep.txt", "keep\n")

	info, err := f.create(t, "defaults", []string{".venv", "web"}, domain.DefaultWorkspaceAssetExcludes, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	root := info.Root.Path
	requireAbsent(t, filepath.Join(root, ".venv"))
	requireAbsent(t, filepath.Join(root, "web", "pkg", "node_modules"))
	requireAbsent(t, filepath.Join(root, "web", "pkg", "__pycache__"))
	requirePresent(t, filepath.Join(root, "web", "app.js"))
	requirePresent(t, filepath.Join(root, "web", "pkg", "keep.txt"))
}

func TestLadoWorkspaceAssetsEmptyExcludeCopiesEverything(t *testing.T) {
	f := newAssetFixture(t)
	f.write(t, ".venv/pyvenv.cfg", "home = /usr/bin\n")
	f.write(t, "web/pkg/node_modules/dep/index.js", "dep\n")

	info, err := f.create(t, "empty", []string{".venv", "web"}, []string{}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	requirePresent(t, filepath.Join(info.Root.Path, ".venv", "pyvenv.cfg"))
	requirePresent(t, filepath.Join(info.Root.Path, "web", "pkg", "node_modules", "dep", "index.js"))
}

func TestLadoWorkspaceAssetsSlashPatternMatchesFromRoot(t *testing.T) {
	f := newAssetFixture(t)
	f.write(t, "web/data/big.bin", "big\n")
	f.write(t, "other/data/small.txt", "small\n")

	info, err := f.create(t, "slash", []string{"web", "other"}, []string{"web/data"}, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	requireAbsent(t, filepath.Join(info.Root.Path, "web", "data"))
	requirePresent(t, filepath.Join(info.Root.Path, "other", "data", "small.txt"))
}

// outsideLinks builds the links a virtualenv and a shared library folder
// leave in a workspace: an absolute link, a relative link that resolves
// through it, and a relative link that leaves the workspace root.
func outsideLinks(t *testing.T, f assetFixture) string {
	t.Helper()
	skipUnixLinkTargets(t)
	shared := filepath.Join(filepath.Dir(f.rootRepo), "shared", "lib")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	f.write(t, "env/bin/activate", "# activate\n")
	f.link(t, "env/bin/python", "/usr/bin/python3")
	f.link(t, "env/bin/python3", "python")
	f.link(t, "env/bin/act", "activate")
	f.link(t, "web/lib", "../../shared/lib")
	return shared
}

func TestLadoWorkspaceAssetsSkipsOutsideLinksByDefault(t *testing.T) {
	f := newAssetFixture(t)
	outsideLinks(t, f)

	info, err := f.create(t, "skip", []string{"env", "web"}, nil, false)
	if err != nil {
		t.Fatalf("create with outside links must not fail: %v", err)
	}
	root := info.Root.Path
	requireAbsent(t, filepath.Join(root, "env", "bin", "python"))
	requireAbsent(t, filepath.Join(root, "env", "bin", "python3"))
	requireAbsent(t, filepath.Join(root, "web", "lib"))
	if target, err := os.Readlink(filepath.Join(root, "env", "bin", "act")); err != nil || target != "activate" {
		t.Fatalf("in-root link = %q, %v; want activate", target, err)
	}
	requirePresent(t, filepath.Join(root, "env", "bin", "activate"))

	want := []string{"env/bin/python", "env/bin/python3", "web/lib"}
	got := slices.Clone(info.SkippedAssetLinks)
	slices.Sort(got)
	if !slices.Equal(got, want) || info.SkippedAssetLinkCount != len(want) {
		t.Fatalf("skipped = %v (%d), want %v", info.SkippedAssetLinks, info.SkippedAssetLinkCount, want)
	}
}

func TestLadoWorkspaceAssetsKeepPointsWhereTheSourcePoints(t *testing.T) {
	f := newAssetFixture(t)
	shared := outsideLinks(t, f)

	info, err := f.create(t, "keep", []string{"env", "web"}, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	root := info.Root.Path
	if target, err := os.Readlink(filepath.Join(root, "env", "bin", "python")); err != nil || target != "/usr/bin/python3" {
		t.Fatalf("absolute link = %q, %v; want /usr/bin/python3", target, err)
	}
	wantShared, err := physicalAbs(shared)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(root, "web", "lib"))
	if err != nil {
		t.Fatalf("outside relative link missing: %v", err)
	}
	if target != wantShared {
		t.Fatalf("outside relative link = %q, want source-side target %q", target, wantShared)
	}
	if assetPathWithin(f.ws.managedRoot, target) {
		t.Fatalf("kept link %q points into the managed root %q", target, f.ws.managedRoot)
	}
	// The relative string resolved against the session path would have landed
	// inside the managed root; the kept link must not.
	if sessionSide := filepath.Clean(filepath.Join(root, "web", "../../shared/lib")); !assetPathWithin(f.ws.managedRoot, sessionSide) {
		t.Fatalf("test setup: session-side resolution %q is not in the managed root", sessionSide)
	}
	if target, err := os.Readlink(filepath.Join(root, "env", "bin", "python3")); err != nil || !filepath.IsAbs(target) || assetPathWithin(f.ws.managedRoot, target) {
		t.Fatalf("chained link = %q, %v; want an absolute source-side target", target, err)
	}
	if len(info.SkippedAssetLinks) != 0 || info.SkippedAssetLinkCount != 0 {
		t.Fatalf("skipped = %v (%d), want none", info.SkippedAssetLinks, info.SkippedAssetLinkCount)
	}
}

func TestLadoWorkspaceAssetsKeepNeverLinksManagedDataOrGit(t *testing.T) {
	f := newAssetFixture(t)
	managedTarget := filepath.Join(f.ws.managedRoot, "proj", "other-session")
	if err := os.MkdirAll(managedTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	f.link(t, "web/managed-abs", managedTarget)
	// rootRepo is <tmp>/root/repo, so ../../../managed is the managed root.
	f.link(t, "web/managed-rel", "../../../managed/proj/other-session")
	f.link(t, "web/repo-git", "../.git")
	f.link(t, "web/abs-git", filepath.Join(f.rootRepo, ".git", "config"))
	f.link(t, "web/abs-ao", filepath.Join(f.tmp, ".ao", "data"))
	f.write(t, "web/ok.txt", "ok\n")

	info, err := f.create(t, "keep-guard", []string{"web"}, nil, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, name := range []string{"managed-abs", "managed-rel", "repo-git", "abs-git", "abs-ao"} {
		requireAbsent(t, filepath.Join(info.Root.Path, "web", name))
	}
	requirePresent(t, filepath.Join(info.Root.Path, "web", "ok.txt"))
	if info.SkippedAssetLinkCount != 5 {
		t.Fatalf("skipped = %v (%d), want 5", info.SkippedAssetLinks, info.SkippedAssetLinkCount)
	}
}

func TestLadoWorkspaceAssetsSkippedListIsCapped(t *testing.T) {
	skipUnixLinkTargets(t)
	f := newAssetFixture(t)
	total := ports.MaxSkippedAssetLinks + 5
	for i := range total {
		f.link(t, fmt.Sprintf("env/link-%02d", i), "/usr/bin/python3")
	}
	info, err := f.create(t, "capped", []string{"env"}, nil, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(info.SkippedAssetLinks) != ports.MaxSkippedAssetLinks || info.SkippedAssetLinkCount != total {
		t.Fatalf("skipped = %d listed (%d total), want %d listed (%d total)",
			len(info.SkippedAssetLinks), info.SkippedAssetLinkCount, ports.MaxSkippedAssetLinks, total)
	}
}

func TestLadoWorkspaceAssetsCopyFailureHasSummary(t *testing.T) {
	f := newAssetFixture(t)
	f.write(t, "notes/a.txt", "a\n")
	f.write(t, "conflict.txt", "conflict\n")
	_, err := f.ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/assets-fail",
		RootRepoPath: f.rootRepo,
		Assets: []ports.WorkspaceProjectAssetConfig{
			{RelativePath: "notes", SourcePath: filepath.Join(f.rootRepo, "notes")},
			{RelativePath: "notes", SourcePath: filepath.Join(f.rootRepo, "conflict.txt")},
		},
	})
	if err == nil {
		t.Fatal("expected conflicting asset destination to fail")
	}
	summary, ok := ports.WorkspaceSetupSummary(err)
	if !ok || !strings.HasPrefix(summary, "could not copy workspace folder notes.") || !strings.Contains(summary, "Workspace folders") {
		t.Fatalf("summary = %q, %v", summary, ok)
	}
	if !strings.Contains(err.Error(), "copy workspace asset \"notes\"") {
		t.Fatalf("error text changed: %v", err)
	}
}
