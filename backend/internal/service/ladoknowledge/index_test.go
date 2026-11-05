package ladoknowledge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFreshness(t *testing.T) {
	e := newEnv(t)
	e.mustWrite("app-1", "fresh", nil, note("그대로", "map", "a/b.go"))
	e.mustWrite("app-1", "plain", nil, note("근거 없음", "convention"))

	// A note written on a branch before its change is committed.
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, e.repo, "worktree", "add", "-q", "-b", "feat", wt)
	e.store.sessions["app-2"] = domain.SessionRecord{ID: "app-2", ProjectID: "app", Metadata: domain.SessionMetadata{WorkspacePath: wt}}
	writeFile(t, filepath.Join(wt, "a", "b.go"), "package a\n\n// changed\n")
	writeFile(t, filepath.Join(wt, "new.go"), "package main\n")
	e.mustWrite("app-2", "changed", nil, note("바뀐 파일", "pitfall", "a/b.go", "new.go"))

	idx := e.index("app")
	if stateOf(idx, "fresh") != domain.LadoKnowledgeFresh || stateOf(idx, "plain") != domain.LadoKnowledgeUnchecked || stateOf(idx, "changed") != domain.LadoKnowledgeStale {
		t.Fatalf("before merge = %+v", idx.Notes)
	}
	if !strings.Contains(idx.Prompt, "- [[changed]] — 바뀐 파일 (rev 1) — 확인 필요") || strings.Contains(idx.Prompt, "그대로 (rev 1) — 확인 필요") {
		t.Fatalf("prompt:\n%s", idx.Prompt)
	}

	// The change lands on the base branch.
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-q", "-m", "change")
	gitIn(t, e.repo, "update-ref", "refs/remotes/origin/main", "feat")
	idx = e.index("app")
	if stateOf(idx, "changed") != domain.LadoKnowledgeFresh || stateOf(idx, "fresh") != domain.LadoKnowledgeStale {
		t.Fatalf("after merge = %+v", idx.Notes)
	}

	// The same tip and paths come from the cache: no second ls-tree.
	e.git.reset(false)
	e.index("app")
	if n := e.git.count("ls-tree"); n != 0 {
		t.Fatalf("ls-tree ran %d times on a cached tip", n)
	}

	// git failing leaves every note unchecked; the index still answers.
	e.git.reset(true)
	idx = e.index("app")
	for _, n := range idx.Notes {
		if n.State != domain.LadoKnowledgeUnchecked {
			t.Fatalf("git failing: %+v", idx.Notes)
		}
	}
}

func TestFreshnessWithoutSourcesCallsNoGit(t *testing.T) {
	e := newEnv(t)
	e.mustWrite("app-1", "a", nil, note("근거 없음", "convention"))
	// A source without a blob, as a hand edit leaves it.
	writeFile(t, e.svc.folder("app").notePath("b"), "---\ntitle: b\nkind: map\nsources: [a/b.go]\n---\n")
	e.git.reset(false)
	idx := e.index("app")
	if stateOf(idx, "a") != domain.LadoKnowledgeUnchecked || stateOf(idx, "b") != domain.LadoKnowledgeUnchecked {
		t.Fatalf("index = %+v", idx.Notes)
	}
	// Only the owner check (rev-list) runs; nothing compares freshness.
	if e.git.count("rev-parse") != 0 || e.git.count("ls-tree") != 0 {
		t.Fatalf("git ran: %v", e.git.calls)
	}
}

func TestFreshnessBaseRef(t *testing.T) {
	e := newEnv(t)
	e.mustWrite("app-1", "n", nil, note("t", "map", "a/b.go"))
	// Automatic default branch: origin/HEAD.
	gitIn(t, e.repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	e.store.projects["app"] = domain.ProjectRecord{ID: "app", Path: e.repo}
	if idx := e.index("app"); stateOf(idx, "n") != domain.LadoKnowledgeFresh {
		t.Fatalf("origin/HEAD = %+v", idx.Notes)
	}
	// No remote branch at all: HEAD.
	gitIn(t, e.repo, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	gitIn(t, e.repo, "update-ref", "-d", "refs/remotes/origin/main")
	if idx := e.index("app"); stateOf(idx, "n") != domain.LadoKnowledgeFresh {
		t.Fatalf("HEAD = %+v", idx.Notes)
	}
	// A workspace project is never compared.
	e.store.projects["app"] = domain.ProjectRecord{ID: "app", Path: e.repo, Kind: domain.ProjectKindWorkspace}
	if states, ok := e.svc.states(context.Background(), e.store.projects["app"], map[string][]domain.LadoKnowledgeSource{"n": {{Path: "a/b.go", Blob: "x"}}}); ok || states != nil {
		t.Fatalf("workspace states = %v %v", states, ok)
	}
}

func TestIndexOfMissingFolderAndUnknownProject(t *testing.T) {
	e := newEnv(t)
	idx := e.index("app")
	if idx.Dir != filepath.Join(e.state, "lado", "knowledge", "app") || idx.Foreign || len(idx.Notes) != 0 || idx.Prompt != "" {
		t.Fatalf("index = %+v", idx)
	}
	for _, id := range []domain.ProjectID{"nope", "../app", ""} {
		_, err := e.svc.Index(context.Background(), id)
		wantCode(t, err, CodeProjectNotFound)
	}
}
