package ladoknowledge

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestClaudeProjectDirName(t *testing.T) {
	for in, want := range map[string]string{
		"/home/code/project/x": "-home-code-project-x",
		"/a/.b":                "-a--b",
		// Each Hangul syllable is one UTF-16 code unit, so one '-'.
		"/home/u/프로젝트":           "-home-u-----",
		`C:\Users\me\repo`:       "C--Users-me-repo",
		"/x/emoji😀":              "-x-emoji--",
		"/home/code/my_app.v2-x": "-home-code-my-app-v2-x",
	} {
		if got := ClaudeProjectDirName(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"feedback_reply-in-korean": "feedback-reply-in-korean",
		"LADO  Plan__Writing":      "lado-plan-writing",
		"한글":                       "",
		strings.Repeat("ab-", 40):  strings.TrimRight(strings.Repeat("ab-", 40)[:64], "-"),
	} {
		if got := slugify(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

// memoryDir makes <ClaudeDir>/projects/<encoded path>/memory.
func (e *env) memoryDir(path string) string {
	dir := filepath.Join(e.state, "claude", "projects", ClaudeProjectDirName(path), "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

func TestImportClaudeMemory(t *testing.T) {
	e := newEnv(t)
	mem := e.memoryDir(e.repo)
	files := map[string]string{
		"MEMORY.md":          "- [a](a.md) — index\n",
		"project_goal.md":    "---\nname: project-goal\ndescription: 프로젝트의 목표\nmetadata:\n  type: project\n---\n목표는 이렇다.\n",
		"reference_docs.md":  "---\nname: docs\ndescription: 문서 위치\ntype: reference\n---\n여기.\n",
		"feedback_korean.md": "---\nname: korean\ndescription: 한국어로 답한다\nmetadata:\n  type: feedback\n---\n한국어.\n",
		"user_role.md":       "---\ntype: user\n---\n개발자.\n",
		"odd.md":             "front matter 없음\n",
		"existing.md":        "---\ndescription: 이미 있는 노트\ntype: project\n---\nx\n",
		"leaky.md":           "---\ndescription: 비밀\n---\nglpat-" + strings.Repeat("q", 24) + "\n",
		"한글.md":              "이름이 한글\n",
		"notes.txt":          "not markdown\n",
		"big.md":             strings.Repeat("x", domain.LadoKnowledgeNoteMaxBytes+1),
	}
	for name, content := range files {
		writeFile(t, filepath.Join(mem, name), content)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	writeFile(t, outside, "---\ndescription: 링크\n---\nx\n")
	if err := os.Symlink(outside, filepath.Join(mem, "linked.md")); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	before := map[string][]byte{}
	entries, _ := os.ReadDir(mem)
	for _, en := range entries {
		p := filepath.Join(mem, en.Name())
		if err := os.Chtimes(p, old, old); err != nil && en.Type()&os.ModeSymlink == 0 {
			t.Fatal(err)
		}
		before[en.Name()], _ = os.ReadFile(p)
	}
	e.mustWrite("app-1", "existing", nil, note("이미 있다", "map"))

	res, err := e.svc.ImportClaudeMemory(context.Background(), "app", "")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(res.Imported)
	if want := []string{"feedback-korean", "odd", "project-goal", "reference-docs", "user-role"}; !slices.Equal(res.Imported, want) {
		t.Fatalf("imported = %v, want %v", res.Imported, want)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.Name] = s.Reason
	}
	for name, reason := range map[string]string{"linked.md": SkipLink, "existing.md": SkipExists, "leaky.md": SkipSecret, "한글.md": SkipInvalidName, "big.md": SkipTooLarge} {
		if reasons[name] != reason {
			t.Errorf("%s skipped as %q, want %q (all: %v)", name, reasons[name], reason, res.Skipped)
		}
	}
	if _, ok := reasons["MEMORY.md"]; ok || len(res.Skipped) != 5 {
		t.Fatalf("skipped = %v", res.Skipped)
	}

	idx := e.index("app")
	byslug := map[string]domain.LadoKnowledgeEntry{}
	for _, n := range idx.Notes {
		byslug[n.Slug] = n
	}
	for slug, want := range map[string]struct {
		kind  domain.LadoKnowledgeKind
		title string
	}{
		"project-goal":    {domain.LadoKnowledgeDecision, "프로젝트의 목표"},
		"reference-docs":  {domain.LadoKnowledgeResearch, "문서 위치"},
		"feedback-korean": {domain.LadoKnowledgeConvention, "한국어로 답한다"},
		"user-role":       {domain.LadoKnowledgeConvention, "user_role"},
		"odd":             {domain.LadoKnowledgeResearch, "odd"},
	} {
		n := byslug[slug]
		if n.Kind != want.kind || n.Title != want.title || n.Revision != 1 || n.Author != domain.LadoKnowledgeAuthorClaudeMemory || n.State != domain.LadoKnowledgeUnchecked {
			t.Errorf("%s = %+v", slug, n)
		}
	}
	data, _ := os.ReadFile(e.svc.folder("app").notePath("project-goal"))
	if n, err := domain.ParseLadoKnowledgeNote("project-goal", data); err != nil || n.Commit != "" || len(n.Sources) != 0 || n.Body != "목표는 이렇다.\n" {
		t.Fatalf("imported note = %+v, %v\n%s", n, err, data)
	}

	// The memory folder is as it was.
	entries, _ = os.ReadDir(mem)
	if len(entries) != len(before) {
		t.Fatalf("memory folder now has %d entries", len(entries))
	}
	for _, en := range entries {
		p := filepath.Join(mem, en.Name())
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, before[en.Name()]) {
			t.Errorf("%s changed", en.Name())
		}
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() && !info.ModTime().Equal(old) {
			t.Errorf("%s mtime = %v", en.Name(), info.ModTime())
		}
	}
	if got := e.store.actions(AuditImported); len(got) != 1 || !strings.Contains(string(got[0].Payload), `"source":"claude_memory"`) {
		t.Fatalf("audit = %+v", got)
	}

	// Importing again skips every note it made.
	res, err = e.svc.ImportClaudeMemory(context.Background(), "app", "")
	if err != nil || len(res.Imported) != 0 {
		t.Fatalf("again = %+v, %v", res, err)
	}
}

func TestImportClaudeMemoryPaths(t *testing.T) {
	e := newEnv(t)
	// A path given picks the memory of an old location.
	old := "/home/u/옛 프로젝트"
	writeFile(t, filepath.Join(e.memoryDir(old), "fact.md"), "---\ndescription: 옛 사실\ntype: project\n---\nx\n")
	res, err := e.svc.ImportClaudeMemory(context.Background(), "app", old)
	if err != nil || !slices.Equal(res.Imported, []string{"fact"}) {
		t.Fatalf("import = %+v, %v", res, err)
	}
	_, err = e.svc.ImportClaudeMemory(context.Background(), "app", "relative/path")
	wantCode(t, err, CodeInvalidImport)

	// Without a path: the project's path, then with links resolved.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(e.repo, link); err != nil {
		t.Fatal(err)
	}
	e.store.projects["app"] = domain.ProjectRecord{ID: "app", Path: link}
	writeFile(t, filepath.Join(e.memoryDir(e.repo), "real.md"), "---\ndescription: 링크를 푼 경로\n---\nx\n")
	res, err = e.svc.ImportClaudeMemory(context.Background(), "app", "")
	if err != nil || !slices.Equal(res.Imported, []string{"real"}) {
		t.Fatalf("resolved import = %+v, %v", res, err)
	}

	// No memory folder at all.
	e.store.projects["bare"] = domain.ProjectRecord{ID: "bare", Path: "/no/such/프로젝트"}
	res, err = e.svc.ImportClaudeMemory(context.Background(), "bare", "")
	if err != nil || len(res.Imported) != 0 || len(res.Skipped) != 1 ||
		res.Skipped[0] != (ImportSkip{Name: "-no-such-----", Reason: SkipClaudeMemoryNotFound}) {
		t.Fatalf("not found = %+v, %v", res, err)
	}
	if _, err := os.Stat(e.svc.Dir("bare")); !os.IsNotExist(err) {
		t.Fatalf("a failed import made the folder: %v", err)
	}
}

func TestImportFromArchivedProject(t *testing.T) {
	e := newEnv(t)
	old := e.newRepo("old", map[string]string{"a/b.go": "package a\n"})
	e.store.projects["old"] = domain.ProjectRecord{ID: "old", Path: old}
	e.store.lado["old"] = true
	e.store.sessions["old-1"] = domain.SessionRecord{ID: "old-1", ProjectID: "old", Metadata: domain.SessionMetadata{WorkspacePath: old}}
	e.mustWrite("old-1", "kept", nil, note("옛 노트", "decision", "a/b.go"))
	e.mustWrite("old-1", "clash", nil, note("겹침", "map"))
	writeFile(t, e.svc.folder("old").notePath("broken"), "no front matter\n")
	e.mustWrite("app-1", "clash", nil, note("이미 있다", "map"))
	// The old project is archived; its knowledge folder stays.
	e.store.projects["old"] = domain.ProjectRecord{ID: "old", Path: old, ArchivedAt: t0}
	// A folder no project ever had, and an empty one.
	writeFile(t, filepath.Join(e.state, "lado", "knowledge", "orphan", "notes", "o.md"), "---\ntitle: o\nkind: map\n---\n")
	if err := os.MkdirAll(filepath.Join(e.state, "lado", "knowledge", "empty", "notes"), 0o700); err != nil {
		t.Fatal(err)
	}

	list, err := e.svc.Importable(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	want := []Importable{{ProjectID: "old", Notes: 3, OwnerPath: old}, {ProjectID: "orphan", Notes: 1}}
	if !slices.Equal(list, want) {
		t.Fatalf("importable = %+v, want %+v", list, want)
	}
	e.store.projects["old"] = domain.ProjectRecord{ID: "old", Path: old}
	if list, _ := e.svc.Importable(context.Background(), "app"); !list[0].Registered {
		t.Fatalf("registered = %+v", list)
	}

	res, err := e.svc.ImportProject(context.Background(), "app", "old")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Imported, []string{"kept"}) || len(res.Skipped) != 2 {
		t.Fatalf("import = %+v", res)
	}
	data, _ := os.ReadFile(e.svc.folder("app").notePath("kept"))
	n, err := domain.ParseLadoKnowledgeNote("kept", data)
	if err != nil || n.Author != "import:project:old" || n.Revision != 1 || len(n.Sources) != 1 || n.Sources[0].Blob == "" || n.Commit == "" {
		t.Fatalf("imported = %+v, %v", n, err)
	}
	// The same repository content: the blob still matches app's base.
	if idx := e.index("app"); stateOf(idx, "kept") != domain.LadoKnowledgeFresh {
		t.Fatalf("index = %+v", idx.Notes)
	}

	for _, from := range []domain.ProjectID{"../x", "app", "a/b", "..", ""} {
		_, err := e.svc.ImportProject(context.Background(), "app", from)
		wantCode(t, err, CodeInvalidImport)
	}
	_, err = e.svc.ImportProject(context.Background(), "app", "never")
	wantCode(t, err, CodeImportNotFound)
}

func TestImportMakesOwner(t *testing.T) {
	e := newEnv(t)
	writeFile(t, filepath.Join(e.memoryDir(e.repo), "a.md"), "---\ndescription: a\n---\nx\n")
	if _, err := e.svc.ImportClaudeMemory(context.Background(), "app", ""); err != nil {
		t.Fatal(err)
	}
	owner, err := readOwner(e.svc.folder("app"))
	if err != nil || owner == nil || owner.ProjectID != "app" || owner.RootCommit == "" {
		t.Fatalf("owner = %+v, %v", owner, err)
	}
}
