package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LADO #22 tests: the workspace asset policy reaches the adapter, skipped
// links are logged, and a setup failure records its short summary.

func newLadoAssetsManager(t *testing.T, assets *domain.WorkspaceAssetsConfig) (*Manager, *fakeWorkspace) {
	t.Helper()
	st := newFakeStore()
	cfg := testRoleAgents()
	cfg.WorkspaceAssets = assets
	st.projects["mer"] = domain.ProjectRecord{
		ID:     "mer",
		Path:   filepath.Join(string(filepath.Separator), "repo", "mer"),
		Kind:   domain.ProjectKindWorkspace,
		Config: cfg,
	}
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{
		{Name: "api", RelativePath: "services/api", DefaultBranch: "main"},
	}
	ws := &fakeWorkspace{path: filepath.Join(string(filepath.Separator), "managed", "mer-1")}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	return m, ws
}

func TestLadoWorkspaceAssetsPassedToAdapter(t *testing.T) {
	cases := []struct {
		name        string
		assets      *domain.WorkspaceAssetsConfig
		wantExclude []string
		wantKeep    bool
	}{
		{name: "no setting uses defaults", assets: nil, wantExclude: domain.DefaultWorkspaceAssetExcludes},
		{name: "keep without exclude keeps defaults", assets: &domain.WorkspaceAssetsConfig{AbsoluteLinks: domain.WorkspaceAssetLinksKeep}, wantExclude: domain.DefaultWorkspaceAssetExcludes, wantKeep: true},
		{name: "empty list copies everything", assets: &domain.WorkspaceAssetsConfig{Exclude: []string{}}, wantExclude: []string{}},
		{name: "custom list replaces defaults", assets: &domain.WorkspaceAssetsConfig{Exclude: []string{"data/raw"}, AbsoluteLinks: "skip"}, wantExclude: []string{"data/raw"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ws := newLadoAssetsManager(t, tc.assets)
			if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
				t.Fatal(err)
			}
			got := ws.lastProjectCfg.AssetExclude
			if got == nil {
				t.Fatal("AssetExclude is nil; the adapter would exclude nothing")
			}
			if !slices.Equal(got, tc.wantExclude) {
				t.Fatalf("AssetExclude = %q, want %q", got, tc.wantExclude)
			}
			if ws.lastProjectCfg.KeepAbsoluteAssetLinks != tc.wantKeep {
				t.Fatalf("KeepAbsoluteAssetLinks = %v, want %v", ws.lastProjectCfg.KeepAbsoluteAssetLinks, tc.wantKeep)
			}
		})
	}
}

func TestLadoWorkspaceAssetsSkippedLinksLogged(t *testing.T) {
	m, ws := newLadoAssetsManager(t, nil)
	var buf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	root := ws.path
	ws.projectCreateInfo = ports.WorkspaceProjectInfo{
		Root: ports.WorkspaceInfo{Path: root, Branch: "ao/mer-1", SessionID: "mer-1", ProjectID: "mer"},
		Worktrees: []ports.WorkspaceRepoInfo{{
			RepoName: domain.RootWorkspaceRepoName, Path: root, Branch: "ao/mer-1",
			BaseRef: "refs/remotes/origin/main", SessionID: "mer-1", ProjectID: "mer",
		}},
		SkippedAssetLinks:     []string{"tools/.venv/bin/python"},
		SkippedAssetLinkCount: 3,
	}
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"skipped workspace asset links", "tools/.venv/bin/python", "count=3", "sessionID=mer-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q lacks %q", out, want)
		}
	}
}

func TestLadoWorkspaceAssetsNoSkippedLinksNoWarning(t *testing.T) {
	m, _ := newLadoAssetsManager(t, nil)
	var buf bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "skipped workspace asset links") {
		t.Fatalf("unexpected warning: %s", buf.String())
	}
}

func TestLadoAsyncChatFailedStartRecordsSetupSummary(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	deferred := deferredBackground(m)
	ws := m.workspace.(*fakeWorkspace)
	gitOutput := strings.Repeat("/very/long/managed/path ", 40) + "\ngit-lfs filter-process: git-lfs: command not found"
	ws.createErr = fmt.Errorf("gitworktree: worktree add: %w", &ports.WorkspaceSetupError{
		Repo:    "front",
		Summary: "repository front uses Git LFS (.gitattributes has filter=lfs), but git-lfs is not installed on this computer",
		Fix:     "install Git LFS (https://git-lfs.com), run git lfs install, then start the session again",
		Err:     errors.Join(ports.ErrGitLFSMissing, errors.New(gitOutput)),
	})

	rec, _, _, err := m.Spawn(context.Background(), asyncChatSpawnConfig("do the thing"))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	(*deferred)[0]()

	stored := st.sessions[rec.ID]
	if stored.ProvisionState != domain.SessionProvisionFailed {
		t.Fatalf("provision state = %q, want failed", stored.ProvisionState)
	}
	want := "repository front uses Git LFS (.gitattributes has filter=lfs), but git-lfs is not installed on this computer. install Git LFS"
	if !strings.HasPrefix(stored.ProvisionError, want) {
		t.Fatalf("provision error = %q, want prefix %q", stored.ProvisionError, want)
	}
}

func TestLadoProvisionErrorWithoutSetupErrorUnchanged(t *testing.T) {
	err := errors.New("spawn mer-1: workspace: branch already checked out")
	if got := ladoProvisionError(err); got != err.Error() {
		t.Fatalf("provision error = %q, want %q", got, err.Error())
	}
}
