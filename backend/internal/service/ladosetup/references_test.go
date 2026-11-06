package ladosetup

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// referenceRepo makes a repository at dir that tracks files with their
// content, and links named by their target, in one commit. The files under
// untracked are written but not committed.
func referenceRepo(t *testing.T, h *fakeHome, dir string, files map[string]string, links map[string]string, untracked map[string]string) {
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
	var add []string
	for rel, content := range files {
		h.write(filepath.Join(dir, filepath.FromSlash(rel)), content)
		add = append(add, rel)
	}
	for rel, target := range links {
		h.symlink(target, filepath.Join(dir, filepath.FromSlash(rel)))
		add = append(add, rel)
	}
	git(append([]string{"add", "--"}, add...)...)
	git("-c", "user.name=LADO", "-c", "user.email=lado@example.com", "-c", "commit.gpgsign=false",
		"commit", "-q", "--no-verify", "-m", "setup files")
	for rel, content := range untracked {
		h.write(filepath.Join(dir, filepath.FromSlash(rel)), content)
	}
}

func referencesOf(t *testing.T, deps Deps, id domain.ProjectID) []domain.LadoProjectReference {
	t.Helper()
	refs, err := New(deps).ProjectReferences(context.Background(), id)
	if err != nil {
		t.Fatalf("ProjectReferences(%s): %v", id, err)
	}
	return refs
}

func referenceAt(refs []domain.LadoProjectReference, rel string) (domain.LadoProjectReference, bool) {
	for _, ref := range refs {
		if ref.Path == rel {
			return ref, true
		}
	}
	return domain.LadoProjectReference{}, false
}

// Both tools' skills, the project's agents and both instruction files are
// listed with the tool that reads them by itself, their names and
// descriptions from the front matter, sorted by path; nothing else is.
func TestProjectReferencesListsSkillsAgentsAndInstructions(t *testing.T) {
	h := newFakeHome(t)
	app := filepath.Join(h.root, "work", "app")
	long := strings.Repeat("word ", 80)
	referenceRepo(t, h, app, map[string]string{
		".claude/skills/bundle/SKILL.md":       "---\nname: bundle-pipeline\ndescription: How bundles move.\nwhen_to_use: You change a bundle step.\n---\nbody\n",
		".claude/skills/bundle/scripts/run.sh": "echo hi\n",
		".claude/skills/bundle/reference.md":   "---\nname: not a skill\n---\n",
		".agents/skills/plan/SKILL.md":         "---\nname: |\n  plan\n  writer\ndescription: " + long + "\n---\n",
		".agents/skills/plain/SKILL.md":        "no front matter\n",
		".claude/agents/sync-job-debugger.md":  "---\nname: sync-job-debugger\ndescription: Finds why a sync job failed.\ntools: Read\n---\n",
		".claude/agents/unnamed.md":            "---\nname: 3\ndescription: Numbered.\n---\n",
		".claude/agents/sub/x.md":              "---\nname: nested\n---\n",
		".claude/commands/ship.md":             "ship\n",
		".claude/settings.json":                "{}\n",
		".mcp.json":                            "{}\n",
		".codex/config.toml":                   "\n",
		"CLAUDE.md":                            "rules\n",
		"service/CLAUDE.md":                    "service rules\n",
		"AGENTS.md":                            "codex rules\n",
		"NOTCLAUDE.md":                         "no\n",
		"README.md":                            "readme\n",
	}, nil, map[string]string{
		".claude/skills/mine/SKILL.md": "---\nname: mine\n---\n",
		".agents/skills/mine/SKILL.md": "---\nname: mine\n---\n",
		"CLAUDE.local.md":              "mine\n",
	})
	deps := h.deps()
	deps.Projects = fakeProjects{{ID: "app", Path: app}}
	refs := referencesOf(t, deps, "app")

	claude, codex := domain.LadoSetupToolClaudeCode, domain.LadoSetupToolCodex
	skill, agent, instructions := domain.LadoSetupKindSkill, domain.LadoSetupKindAgent, domain.LadoSetupKindInstructions
	want := []domain.LadoProjectReference{
		{Kind: skill, Loader: codex, Path: ".agents/skills/plain/SKILL.md", Name: "plain"},
		{Kind: skill, Loader: codex, Path: ".agents/skills/plan/SKILL.md", Name: "plan writer"},
		{Kind: agent, Loader: claude, Path: ".claude/agents/sync-job-debugger.md", Name: "sync-job-debugger", Description: "Finds why a sync job failed."},
		{Kind: agent, Loader: claude, Path: ".claude/agents/unnamed.md", Name: "unnamed", Description: "Numbered."},
		{Kind: skill, Loader: claude, Path: ".claude/skills/bundle/SKILL.md", Name: "bundle-pipeline", Description: "How bundles move. Use when: You change a bundle step."},
		{Kind: instructions, Loader: codex, Path: "AGENTS.md", Name: "AGENTS.md"},
		{Kind: instructions, Loader: claude, Path: "CLAUDE.md", Name: "CLAUDE.md"},
		{Kind: instructions, Loader: claude, Path: "service/CLAUDE.md", Name: "service/CLAUDE.md"},
	}
	if len(refs) != len(want) {
		t.Fatalf("references:\n%+v\nwant:\n%+v", refs, want)
	}
	for i, ref := range refs {
		w := want[i]
		if ref.Path == ".agents/skills/plan/SKILL.md" {
			// The long description is cut to 240 characters, on one line.
			if n := utf8.RuneCountInString(ref.Description); n != referenceDescriptionMax || !strings.HasSuffix(ref.Description, "…") ||
				!strings.HasPrefix(ref.Description, "word word") || strings.Contains(ref.Description, "  ") {
				t.Fatalf("the long description is %d characters: %q", n, ref.Description)
			}
			ref.Description = ""
		}
		if ref != w {
			t.Fatalf("reference %d is %+v, want %+v", i, ref, w)
		}
	}
}

// A name falls back to the folder or the file, every name is one line of at
// most 64 characters, and a multi-line description joins into one line.
func TestProjectReferencesNamesOnOneLine(t *testing.T) {
	h := newFakeHome(t)
	app := filepath.Join(h.root, "work", "app")
	referenceRepo(t, h, app, map[string]string{
		".claude/skills/empty/SKILL.md": "---\nname: \"  \"\ndescription: >-\n  first\n  second\n---\n",
		".claude/skills/long/SKILL.md":  "---\nname: " + strings.Repeat("n", 100) + "\ndescription: \"a\\tb\\u001bc\\nd\"\n---\n",
		".claude/agents/list.md":        "---\nname: [a, b]\nwhen_to_use: |\n  you debug\n  sync jobs\n---\n",
		".claude/agents/broken.md":      "---\nname: [unclosed\n---\n",
	}, nil, nil)
	deps := h.deps()
	deps.Projects = fakeProjects{{ID: "app", Path: app}}
	refs := referencesOf(t, deps, "app")
	for rel, want := range map[string][2]string{
		".claude/skills/empty/SKILL.md": {"empty", "first second"},
		".claude/skills/long/SKILL.md":  {strings.Repeat("n", referenceNameMax-1) + "…", "a b c d"},
		".claude/agents/list.md":        {"list", "Use when: you debug sync jobs"},
		".claude/agents/broken.md":      {"broken", ""},
	} {
		ref, ok := referenceAt(refs, rel)
		if !ok || ref.Name != want[0] || ref.Description != want[1] {
			t.Fatalf("%s is %+v (found %v), want name %q and description %q", rel, ref, ok, want[0], want[1])
		}
	}
}

// The project's agent rules file is left out however it is written, since AO
// puts its text into the instructions already.
func TestProjectReferencesLeavesOutTheAgentRulesFile(t *testing.T) {
	for _, rulesFile := range []string{"docs/CLAUDE.md", "./docs/CLAUDE.md", `docs\CLAUDE.md`, " docs//CLAUDE.md "} {
		t.Run(rulesFile, func(t *testing.T) {
			h := newFakeHome(t)
			app := filepath.Join(h.root, "work", "app")
			referenceRepo(t, h, app, map[string]string{"CLAUDE.md": "root\n", "docs/CLAUDE.md": "docs\n"}, nil, nil)
			deps := h.deps()
			deps.Projects = fakeProjects{{ID: "app", Path: app, Config: domain.ProjectConfig{AgentRulesFile: rulesFile}}}
			refs := referencesOf(t, deps, "app")
			if _, ok := referenceAt(refs, "docs/CLAUDE.md"); ok || len(refs) != 1 || refs[0].Path != "CLAUDE.md" {
				t.Fatalf("references with agentRulesFile %q: %+v", rulesFile, refs)
			}
		})
	}
}

// A tracked SKILL.md that is a link to a file outside the repository is
// listed by its folder's name, and nothing of the file it points at is read.
func TestProjectReferencesDoesNotFollowLinks(t *testing.T) {
	h := newFakeHome(t)
	outside := h.write(filepath.Join(h.root, "elsewhere", "SKILL.md"), "---\nname: outside\ndescription: Secret text.\n---\n")
	app := filepath.Join(h.root, "work", "app")
	referenceRepo(t, h, app, nil, map[string]string{".claude/skills/linked/SKILL.md": outside}, nil)
	deps := h.deps()
	deps.Projects = fakeProjects{{ID: "app", Path: app}}
	refs := referencesOf(t, deps, "app")
	ref, ok := referenceAt(refs, ".claude/skills/linked/SKILL.md")
	if !ok || ref.Name != "linked" || ref.Description != "" {
		t.Fatalf("the linked skill is %+v (found %v), want its folder's name without a description", ref, ok)
	}
}

// A file larger than the read limit is listed without a description.
func TestProjectReferencesSkipsLargeFiles(t *testing.T) {
	h := newFakeHome(t)
	app := filepath.Join(h.root, "work", "app")
	big := "---\nname: big\ndescription: Big.\n---\n" + strings.Repeat("x", referenceReadLimit)
	referenceRepo(t, h, app, map[string]string{".claude/skills/big/SKILL.md": big}, nil, nil)
	deps := h.deps()
	deps.Projects = fakeProjects{{ID: "app", Path: app}}
	refs := referencesOf(t, deps, "app")
	if len(refs) != 1 || refs[0].Name != "big" || refs[0].Description != "" {
		t.Fatalf("the large skill is %+v", refs)
	}
}

// Scratch, workspace, archived and unknown projects have no list; a folder
// git cannot read is an error.
func TestProjectReferencesProjectsWithoutAList(t *testing.T) {
	h := newFakeHome(t)
	app := filepath.Join(h.root, "work", "app")
	referenceRepo(t, h, app, map[string]string{"CLAUDE.md": "rules\n"}, nil, nil)
	broken := h.mkdir(filepath.Join(h.root, "work", "broken"))
	h.write(filepath.Join(broken, ".git"), "gitdir: "+filepath.Join(h.root, "missing")+"\n")
	deps := h.deps()
	deps.Projects = fakeProjects{
		{ID: "scratch", Path: app, Kind: domain.ProjectKindScratch},
		{ID: "workspace", Path: app, Kind: domain.ProjectKindWorkspace},
		{ID: "archived", Path: app, ArchivedAt: time.Unix(1, 0)},
		{ID: "broken", Path: broken},
		{ID: "nofolder"},
	}
	for _, id := range []domain.ProjectID{"scratch", "workspace", "archived", "unknown"} {
		if refs := referencesOf(t, deps, id); refs != nil {
			t.Fatalf("project %s lists %+v, want nil", id, refs)
		}
	}
	for _, id := range []domain.ProjectID{"broken", "nofolder"} {
		if refs, err := New(deps).ProjectReferences(context.Background(), id); err == nil {
			t.Fatalf("project %s lists %+v, want an error", id, refs)
		}
	}
	if refs, err := New(h.deps()).ProjectReferences(context.Background(), "app"); err != nil || refs != nil {
		t.Fatalf("without projects: %+v, %v", refs, err)
	}
}

// Paths with a line break or another control character never reach the list.
func TestProjectReferencesSkipsControlCharacterPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file names cannot hold control characters")
	}
	h := newFakeHome(t)
	app := filepath.Join(h.root, "work", "app")
	referenceRepo(t, h, app, map[string]string{
		".claude/skills/a\nb/SKILL.md": "---\nname: x\n---\n",
		"x\x1b/CLAUDE.md":              "rules\n",
		"CLAUDE.md":                    "rules\n",
	}, nil, nil)
	deps := h.deps()
	deps.Projects = fakeProjects{{ID: "app", Path: app}}
	refs := referencesOf(t, deps, "app")
	if len(refs) != 1 || refs[0].Path != "CLAUDE.md" {
		t.Fatalf("references: %+v", refs)
	}
}
