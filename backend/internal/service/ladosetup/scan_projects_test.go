package ladosetup

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// gitRepo makes a repository at dir that tracks the files, written with
// their names as content, in one commit.
func gitRepo(t *testing.T, h *fakeHome, dir string, files ...string) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	h.mkdir(dir)
	git("init", "-q", "-b", "main")
	for _, f := range files {
		h.write(filepath.Join(dir, filepath.FromSlash(f)), f)
	}
	git(append([]string{"add", "--"}, files...)...)
	git("-c", "user.name=LADO", "-c", "user.email=lado@example.com", "-c", "commit.gpgsign=false",
		"commit", "-q", "--no-verify", "-m", "setup files")
}

// HARNESS §15 item 14, first half, and plan decisions k and l: a project's
// tracked setup files are listed read-only and never adoptable; its
// untracked personal files are listed as files worktrees never load. A
// project git cannot read keeps its error, and the others are still read.
func TestRepoTrackedFilesAreNeverAdoptable(t *testing.T) {
	h := newFakeHome(t)
	app := filepath.Join(h.root, "work", "app")
	tracked := []string{
		"CLAUDE.md", "docs/CLAUDE.md", "AGENTS.md", "service/AGENTS.md", ".mcp.json", ".claude/settings.json",
		".claude/skills/release/SKILL.md", ".claude/agents/reviewer.md", ".claude/commands/ship.md", ".codex/config.toml",
	}
	gitRepo(t, h, app, append([]string{"README.md", "NOTCLAUDE.md"}, tracked...)...)
	h.write(filepath.Join(app, ".claude", "settings.local.json"), `{"model": "haiku"}`)
	h.write(filepath.Join(app, "CLAUDE.local.md"), "mine")
	// A repository that tracks a personal file shows it as tracked.
	lib := filepath.Join(h.root, "work", "lib")
	gitRepo(t, h, lib, "CLAUDE.local.md")
	// A .git file naming no repository fails git wherever the test runs.
	notRepo := h.mkdir(filepath.Join(h.root, "work", "plain"))
	h.write(filepath.Join(notRepo, ".git"), "gitdir: "+filepath.Join(h.root, "missing")+"\n")
	gone := filepath.Join(h.root, "work", "gone")
	scratch := h.mkdir(filepath.Join(h.root, "work", "scratch"))
	h.write(filepath.Join(scratch, "CLAUDE.local.md"), "scratch")

	deps := h.deps()
	deps.Projects = fakeProjects{
		{ID: "app", DisplayName: "App", Path: app},
		{ID: "lib", Path: lib},
		{ID: "plain", Path: notRepo},
		{ID: "gone", Path: gone},
		{ID: "scratch", Path: scratch, Kind: domain.ProjectKindScratch},
	}
	inv := mustScan(t, deps)
	project := domain.LadoSetupLocationProject

	for _, rel := range tracked {
		var e domain.LadoSetupEntry
		for _, candidate := range inv.Entries {
			if candidate.Location == project && candidate.ProjectID == "app" && candidate.Name == rel {
				e = candidate
			}
		}
		wantStatus(t, e, domain.LadoSetupStatusRepoTracked, domain.LadoSetupReasonRepoTracked)
		if !e.Tracked || e.Path != filepath.Join(app, filepath.FromSlash(rel)) || e.SHA256 != sumOf([]byte(rel)) || e.Size != int64(len(rel)) {
			t.Fatalf("%s is %+v, want tracked at its path with its content", rel, e)
		}
		if e.DefaultAction != "" || e.ItemID != "" {
			t.Fatalf("%s proposes %q for item %q", rel, e.DefaultAction, e.ItemID)
		}
	}
	for rel, want := range map[string][2]string{
		"CLAUDE.md":                       {string(domain.LadoSetupToolClaudeCode), string(domain.LadoSetupKindInstructions)},
		".claude/settings.json":           {string(domain.LadoSetupToolClaudeCode), string(domain.LadoSetupKindSetting)},
		".mcp.json":                       {string(domain.LadoSetupToolClaudeCode), string(domain.LadoSetupKindMCPServer)},
		".claude/skills/release/SKILL.md": {string(domain.LadoSetupToolClaudeCode), string(domain.LadoSetupKindSkill)},
		"service/AGENTS.md":               {string(domain.LadoSetupToolCodex), string(domain.LadoSetupKindInstructions)},
		".codex/config.toml":              {string(domain.LadoSetupToolCodex), ""},
	} {
		e := entryAt(t, inv, project, domain.LadoSetupKind(want[1]), rel)
		if string(e.Tool) != want[0] {
			t.Fatalf("%s belongs to %s, want %s", rel, e.Tool, want[0])
		}
	}
	for _, e := range inv.Entries {
		if e.Name == "README.md" || e.Name == "NOTCLAUDE.md" {
			t.Fatalf("a file no tool loads is listed: %+v", e)
		}
		if e.Location == project && e.Status == domain.LadoSetupStatusAdoptable {
			t.Fatalf("a project file is adoptable: %+v", e)
		}
	}

	local := entryAt(t, inv, project, domain.LadoSetupKindSetting, ".claude/settings.local.json")
	wantStatus(t, local, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonWorktreesDoNotLoad)
	if local.Tracked || local.ProjectID != "app" || local.Path != filepath.Join(app, ".claude", "settings.local.json") {
		t.Fatalf("settings.local.json is %+v", local)
	}
	var appLocal, libLocal domain.LadoSetupEntry
	for _, e := range inv.Entries {
		if e.Name == claudeLocalMD {
			switch e.ProjectID {
			case "app":
				appLocal = e
			case "lib":
				libLocal = e
			default:
				t.Fatalf("CLAUDE.local.md of project %q is listed", e.ProjectID)
			}
		}
	}
	wantStatus(t, appLocal, domain.LadoSetupStatusNotManaged, domain.LadoSetupReasonWorktreesDoNotLoad)
	wantStatus(t, libLocal, domain.LadoSetupStatusRepoTracked, domain.LadoSetupReasonRepoTracked)

	if len(inv.Projects) != 4 {
		t.Fatalf("projects %+v, want app, lib, plain and gone", inv.Projects)
	}
	for _, p := range inv.Projects {
		switch {
		case p.ID == "app" && (p.Name != "App" || p.Path != app || p.Error != ""):
			t.Fatalf("project app is %+v", p)
		case p.ID == "lib" && (p.Name != "lib" || p.Error != ""):
			t.Fatalf("project lib is %+v", p)
		case p.ID == "plain" && !strings.Contains(p.Error, "git ls-files"):
			t.Fatalf("project plain is %+v, want the git error", p)
		case p.ID == "gone" && !strings.Contains(p.Error, "read the project folder"):
			t.Fatalf("project gone is %+v, want the missing folder", p)
		}
	}
}
