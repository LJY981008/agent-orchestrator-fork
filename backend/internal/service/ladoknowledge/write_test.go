package ladoknowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestWriteNewNoteAndUpdate(t *testing.T) {
	e := newEnv(t)
	entry := e.mustWrite("app-1", "build", nil, note("빌드 방법", "pitfall", "a/b.go:3-9"))
	if entry.Revision != 1 || entry.Author != "app-1" || entry.Kind != domain.LadoKnowledgePitfall || entry.State != domain.LadoKnowledgeFresh {
		t.Fatalf("entry = %+v", entry)
	}
	f := e.svc.folder("app")
	data, err := os.ReadFile(f.notePath("build"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := domain.ParseLadoKnowledgeNote("build", data)
	if err != nil {
		t.Fatal(err)
	}
	head := gitIn(t, e.repo, "rev-parse", "HEAD")
	blob := gitIn(t, e.repo, "rev-parse", "HEAD:a/b.go")
	if stored.Revision != 1 || stored.Author != "app-1" || stored.Commit != head || !stored.Updated.Equal(t0) ||
		len(stored.Sources) != 1 || stored.Sources[0] != (domain.LadoKnowledgeSource{Path: "a/b.go", Blob: blob}) {
		t.Fatalf("stored = %+v", stored)
	}
	index, _ := os.ReadFile(f.indexPath())
	if !strings.Contains(string(index), "- [[build]] — 빌드 방법 (rev 1)") {
		t.Fatalf("index.md:\n%s", index)
	}
	var records map[string]noteRecord
	if _, err := readJSON(f.recordsPath(), &records); err != nil || records["build"].Revision != 1 || records["build"].SHA256 != sha256Hex(data) {
		t.Fatalf("records = %+v, %v", records, err)
	}

	// A note the writer's front matter says is revision 9 is still 2.
	content := strings.Replace(string(note("빌드 방법 2", "pitfall")), "kind:", "revision: 9\nauthor: me\nkind:", 1)
	entry = e.mustWrite("app-1", "build", intp(1), []byte(content))
	if entry.Revision != 2 || entry.Author != "app-1" || entry.State != domain.LadoKnowledgeUnchecked {
		t.Fatalf("update = %+v", entry)
	}
	_, err = e.write("app-1", "build", nil, note("again", "map"))
	if ae := wantCode(t, err, CodeExists); ae.Details["currentRevision"] != 2 {
		t.Fatalf("details = %v", ae.Details)
	}
	_, err = e.write("app-1", "build", intp(1), note("stale", "map"))
	// The CLI shows the envelope as it is, so the message names the revision.
	if ae := wantCode(t, err, CodeChanged); ae.Details["currentRevision"] != 2 || !strings.Contains(ae.Message, "revision 2") {
		t.Fatalf("message %q, details = %v", ae.Message, ae.Details)
	}
	_, err = e.write("app-1", "nothing", intp(1), note("x", "map"))
	if ae := wantCode(t, err, CodeChanged); ae.Details["currentRevision"] != 0 {
		t.Fatalf("details = %v", ae.Details)
	}
	if got := e.store.actions(AuditNoteWritten); len(got) != 2 || got[0].Actor != "session:app-1" || got[0].SubjectID != "app" {
		t.Fatalf("audit = %+v", got)
	}
	owner, err := readOwner(f)
	if err != nil || owner == nil || owner.ProjectID != "app" || owner.RootCommit != head || owner.Path != e.repo {
		t.Fatalf("owner = %+v, %v", owner, err)
	}
}

func TestWritePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX modes")
	}
	e := newEnv(t)
	e.mustWrite("app-1", "x", nil, note("x", "map"))
	f := e.svc.folder("app")
	for _, dir := range []string{string(f), f.notesDir(), f.metaDir()} {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", dir, info.Mode(), err)
		}
	}
	for _, file := range []string{f.notePath("x"), f.indexPath(), f.recordsPath(), f.ownerPath()} {
		if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", file, info.Mode(), err)
		}
	}
}

func TestConcurrentWritesWithSameBase(t *testing.T) {
	e := newEnv(t)
	e.mustWrite("app-1", "shared", nil, note("공유", "decision"))
	e.store.sessions["app-2"] = domain.SessionRecord{ID: "app-2", ProjectID: "app", Metadata: domain.SessionMetadata{WorkspacePath: e.repo}}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []domain.SessionID{"app-1", "app-2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.write(id, "shared", intp(1), note("공유 "+string(id), "decision"))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		if ae := wantCode(t, err, CodeChanged); ae.Details["currentRevision"] != 2 {
			t.Fatalf("details = %v", ae.Details)
		}
	}
	if ok != 1 {
		t.Fatalf("%d writes succeeded: %v", ok, errs)
	}
}

func TestExternalEditIsAdopted(t *testing.T) {
	e := newEnv(t)
	e.mustWrite("app-1", "n", nil, note("원래", "map"))
	f := e.svc.folder("app")
	writeFile(t, f.notePath("n"), string(note("옵시디언에서 고침", "map")))

	_, err := e.write("app-1", "n", intp(1), note("세션의 고침", "map"))
	if ae := wantCode(t, err, CodeChanged); ae.Details["currentRevision"] != 2 {
		t.Fatalf("details = %v", ae.Details)
	}
	if got := e.store.actions(AuditExternalEdit); len(got) != 1 || got[0].Actor != domain.LadoKnowledgeAuthorExternal {
		t.Fatalf("external edit events = %+v", got)
	}
	idx := e.index("app")
	if len(idx.Notes) != 1 || idx.Notes[0].Revision != 2 || idx.Notes[0].Author != domain.LadoKnowledgeAuthorExternal || idx.Notes[0].Title != "옵시디언에서 고침" {
		t.Fatalf("index = %+v", idx.Notes)
	}
	entry := e.mustWrite("app-1", "n", intp(2), note("합친 내용", "map"))
	if entry.Revision != 3 || entry.Author != "app-1" {
		t.Fatalf("entry = %+v", entry)
	}
	if got := e.store.actions(AuditExternalEdit); len(got) != 1 {
		t.Fatalf("the write itself was taken as an external edit: %+v", got)
	}

	// A note made in Obsidian, unknown to notes.json, is revision 1.
	writeFile(t, f.notePath("from-obsidian"), "---\ntitle: 옵시디언 노트\nkind: convention\n---\n본문\n")
	if idx := e.index("app"); stateOf(idx, "from-obsidian") != domain.LadoKnowledgeUnchecked {
		t.Fatalf("index = %+v", idx.Notes)
	}
	entry = e.mustWrite("app-1", "from-obsidian", intp(1), note("옵시디언 노트", "convention"))
	if entry.Revision != 2 {
		t.Fatalf("entry = %+v", entry)
	}

	// A note deleted by hand loses its record.
	if err := os.Remove(f.notePath("n")); err != nil {
		t.Fatal(err)
	}
	if idx := e.index("app"); stateOf(idx, "n") != "missing" {
		t.Fatalf("deleted note listed: %+v", idx.Notes)
	}
	e.mustWrite("app-1", "n", nil, note("다시", "map"))

	// A file without front matter keeps its slug as title.
	writeFile(t, f.notePath("loose"), "그냥 글\n")
	idx = e.index("app")
	for _, n := range idx.Notes {
		if n.Slug == "loose" && (n.Title != "loose" || n.Revision != 1) {
			t.Fatalf("loose = %+v", n)
		}
	}
}

func TestWriteRefusals(t *testing.T) {
	e := newEnv(t)
	other := e.newRepo("other", map[string]string{"x.go": "package x\n"})
	e.store.projects["other"] = domain.ProjectRecord{ID: "other", Path: other}
	e.store.projects["plain"] = domain.ProjectRecord{ID: "plain", Path: other}
	e.store.lado["other"] = true
	e.store.sessions["other-1"] = domain.SessionRecord{ID: "other-1", ProjectID: "other", Metadata: domain.SessionMetadata{WorkspacePath: other}}
	e.store.sessions["ended"] = domain.SessionRecord{ID: "ended", ProjectID: "app", IsTerminated: true}
	e.store.sessions["plain-1"] = domain.SessionRecord{ID: "plain-1", ProjectID: "plain"}
	e.store.sessions["solo"] = domain.SessionRecord{ID: "solo"}
	e.store.sessions["critic"] = domain.SessionRecord{ID: "critic", ProjectID: "app",
		ClientRequestID: domain.LadoPlanWorkerClientRequestID("req-1", domain.LadoPlanRoleCritic, 1)}
	e.store.sessions["designer"] = domain.SessionRecord{ID: "designer", ProjectID: "app", Metadata: domain.SessionMetadata{WorkspacePath: e.repo},
		ClientRequestID: domain.LadoPlanWorkerClientRequestID("req-1", domain.LadoPlanRoleDesigner, 1)}

	cases := []struct {
		name    string
		session domain.SessionID
		slug    string
		content []byte
		code    string
	}{
		{"missing session", "nope", "a", note("t", "map"), CodeSessionNotFound},
		{"ended session", "ended", "a", note("t", "map"), CodeSessionEnded},
		{"not LADO mode", "plain-1", "a", note("t", "map"), CodeNotLadoProject},
		{"no project", "solo", "a", note("t", "map"), CodeNotLadoProject},
		{"critic", "critic", "a", note("t", "map"), CodeReadOnly},
		{"slug escape", "app-1", "../x", note("t", "map"), CodeInvalidNote},
		{"slug folder", "app-1", "a/b", note("t", "map"), CodeInvalidNote},
		{"pathspec magic", "app-1", "a", note("t", "map", ":!x"), CodeInvalidNote},
		{"absolute source", "app-1", "a", note("t", "map", "/etc/passwd"), CodeInvalidNote},
		{"missing source", "app-1", "a", note("t", "map", "no/such.go"), CodeInvalidNote},
		{"folder source", "app-1", "a", note("t", "map", "a"), CodeInvalidNote},
		{"bad kind", "app-1", "a", note("t", "story"), CodeInvalidNote},
		{"secret", "app-1", "a", append(note("t", "map"), []byte("token: ghp_"+strings.Repeat("x1", 20)+"\n")...), CodeSecret},
		{"private key", "app-1", "a", append(note("t", "map"), []byte("-----BEGIN RSA "+"PRIVATE KEY-----\n")...), CodeSecret},
		{"too large", "app-1", "a", append(note("t", "map"), []byte(strings.Repeat("x", domain.LadoKnowledgeNoteMaxBytes))...), CodeInvalidNote},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.write(c.session, c.slug, nil, c.content)
			wantCode(t, err, c.code)
		})
	}
	_, err := e.write("app-1", "a", intp(0), note("t", "map"))
	wantCode(t, err, CodeInvalidNote)
	if _, err := os.Stat(e.svc.Dir("app")); !os.IsNotExist(err) {
		t.Fatalf("a refused write made the folder: %v", err)
	}

	// The designer writes; a source with a line range is stored without it.
	entry := e.mustWrite("designer", "d", nil, note("설계", "decision", "a/b.go:12"))
	data, _ := os.ReadFile(e.svc.folder("app").notePath("d"))
	if entry.Revision != 1 || !strings.Contains(string(data), "path: a/b.go\n") || strings.Contains(string(data), ":12") {
		t.Fatalf("designer note:\n%s", data)
	}
	// A session writes in its own project only.
	e.mustWrite("other-1", "d", nil, note("다른 프로젝트", "map", "x.go"))
	if idx := e.index("app"); len(idx.Notes) != 1 || idx.Notes[0].Title != "설계" {
		t.Fatalf("app = %+v", idx.Notes)
	}
	if idx := e.index("other"); len(idx.Notes) != 1 || idx.Notes[0].Title != "다른 프로젝트" {
		t.Fatalf("other = %+v", idx.Notes)
	}
}

func TestWriteNoteCap(t *testing.T) {
	e := newEnv(t)
	e.mustWrite("app-1", "first", nil, note("t", "map"))
	f := e.svc.folder("app")
	records := map[string]noteRecord{}
	for i := range domain.LadoKnowledgeMaxNotes {
		slug := fmt.Sprintf("n%04d", i)
		data := domain.RenderLadoKnowledgeNote(domain.LadoKnowledgeNote{Slug: slug, Title: "t", Kind: domain.LadoKnowledgeMap, Revision: 1})
		writeFile(t, f.notePath(slug), string(data))
		records[slug] = noteRecord{Revision: 1, SHA256: sha256Hex(data)}
	}
	if err := os.Remove(f.notePath("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(f.recordsPath(), records); err != nil {
		t.Fatal(err)
	}
	_, err := e.write("app-1", "one-more", nil, note("t", "map"))
	wantCode(t, err, CodeInvalidNote)
	e.mustWrite("app-1", "n0001", intp(1), note("갱신은 된다", "map"))
}

func TestForeignAndClaim(t *testing.T) {
	e := newEnv(t)
	e.mustWrite("app-1", "n", nil, note("원래 저장소의 노트", "map"))

	// The project's id now names another repository.
	moved := e.newRepo("elsewhere", map[string]string{"z.go": "package z\n"})
	e.store.projects["app"] = domain.ProjectRecord{ID: "app", Path: moved}
	e.store.sessions["app-1"] = domain.SessionRecord{ID: "app-1", ProjectID: "app", Metadata: domain.SessionMetadata{WorkspacePath: moved}}
	idx := e.index("app")
	if !idx.Foreign || idx.Owner == nil || idx.Owner.Path != e.repo || len(idx.Notes) != 0 || idx.Prompt != "" {
		t.Fatalf("foreign index = %+v", idx)
	}
	_, err := e.write("app-1", "m", nil, note("t", "map"))
	if ae := wantCode(t, err, CodeForeign); ae.Details["ownerPath"] != e.repo {
		t.Fatalf("details = %v", ae.Details)
	}
	_, err = e.svc.ImportProject(context.Background(), "app", "gone")
	wantCode(t, err, CodeImportNotFound)

	if err := e.svc.Claim(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	idx = e.index("app")
	if idx.Foreign || len(idx.Notes) != 1 || !strings.Contains(idx.Prompt, "[[n]]") {
		t.Fatalf("claimed index = %+v", idx)
	}
	e.mustWrite("app-1", "m", nil, note("t", "map"))
	if got := e.store.actions(AuditClaimed); len(got) != 1 || !strings.Contains(string(got[0].Payload), `"fromPath":"`+e.repo) {
		t.Fatalf("claimed events = %+v", got)
	}

	// The same repository moved to another folder is not foreign.
	clone := filepath.Join(t.TempDir(), "renamed")
	gitIn(t, "", "clone", "-q", moved, clone)
	e.store.projects["app"] = domain.ProjectRecord{ID: "app", Path: clone}
	if idx := e.index("app"); idx.Foreign {
		t.Fatalf("a moved repository is foreign")
	}
}

func TestForeignByPathWithoutRootCommit(t *testing.T) {
	e := newEnv(t)
	ws := t.TempDir()
	e.store.projects["ws"] = domain.ProjectRecord{ID: "ws", Path: ws, Kind: domain.ProjectKindWorkspace}
	e.store.lado["ws"] = true
	e.store.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Metadata: domain.SessionMetadata{WorkspacePath: ws}}
	e.mustWrite("ws-1", "n", nil, note("t", "map"))
	owner, _ := readOwner(e.svc.folder("ws"))
	if owner == nil || owner.RootCommit != "" || owner.Path != ws {
		t.Fatalf("owner = %+v", owner)
	}
	e.store.projects["ws"] = domain.ProjectRecord{ID: "ws", Path: t.TempDir(), Kind: domain.ProjectKindWorkspace}
	if idx := e.index("ws"); !idx.Foreign {
		t.Fatalf("a workspace project at another path is not foreign")
	}
}
