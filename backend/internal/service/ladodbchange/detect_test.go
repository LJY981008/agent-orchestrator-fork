package ladodbchange

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// detectFixture is a repository whose main holds an applied SQL file and a
// feature branch, with settings turned on through the service.
type detectFixture struct {
	repo  *testRepo
	store *fakeStore
	s     *Service
	base  string
}

func newDetectFixture(t *testing.T) *detectFixture {
	t.Helper()
	repo := newTestRepo(t, map[string]string{
		"db/001_init.sql": "CREATE TABLE a (id int);\n",
		"db/002_seed.sql": "INSERT INTO a VALUES (1);\n",
		"README.md":       "# app\n",
	})
	store := newFakeStore()
	store.addProject(testProject, repo.dir)
	s := newTestService(t, store)
	update := onUpdate("")
	update.Paths = []string{"db/*.sql"}
	if _, err := s.UpdateDBChangeSettings(context.Background(), testProject, update, (&presenceOK{}).fn); err != nil {
		t.Fatal(err)
	}
	repo.git(repo.dir, "checkout", "-q", "-b", "feature")
	return &detectFixture{repo: repo, store: store, s: s, base: repo.git(repo.dir, "rev-parse", "origin/main")}
}

func (f *detectFixture) input(head string, kinds ...string) domain.LadoDBChangeInput {
	return domain.LadoDBChangeInput{
		ProjectID: testProject, Dir: f.repo.dir, Base: f.base, Head: head,
		Plan: domain.LadoPlan{ChangeKinds: kinds},
	}
}

func TestItemListsNewSQLFilesInPathOrder(t *testing.T) {
	f := newDetectFixture(t)
	head := f.repo.commit(f.repo.dir, map[string]string{
		"db/010_b.sql": "ALTER TABLE a ADD b int;\n",
		"db/003_a.sql": "\n  -- LADO:After-Deploy  \nDROP TABLE old;\n",
		"db/notes.txt": "not SQL\n",
		"other/x.sql":  "SELECT 1;\n",
		"README.md":    "# app, changed\n",
	}, nil, "Add SQL")
	item, ok, err := f.s.Item(context.Background(), f.input(head, domain.LadoChangeDBSchema))
	if err != nil || !ok {
		t.Fatalf("Item = %v, %v", ok, err)
	}
	if item.Kind != domain.LadoVerifyDBChanges || item.Outcome != domain.LadoVerifyPass || item.Code != "" {
		t.Fatalf("item = %+v", item)
	}
	record := domain.LadoVerification{Items: []domain.LadoVerifyItem{item}}
	details, ok, err := record.DBChanges()
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	want := []domain.LadoDBChangeFileRef{
		{Path: "db/003_a.sql", BlobSHA: f.repo.blob(head, "db/003_a.sql"), Phase: domain.LadoDBChangeAfterDeploy},
		{Path: "db/010_b.sql", BlobSHA: f.repo.blob(head, "db/010_b.sql"), Phase: domain.LadoDBChangePreMerge},
	}
	if len(details.Files) != 2 || details.Files[0] != want[0] || details.Files[1] != want[1] || len(details.Modified) != 0 || !details.Planned {
		t.Fatalf("details = %+v", details)
	}
	// The details are the record's: the same head gives the same bytes.
	again, _, _ := f.s.Item(context.Background(), f.input(head, domain.LadoChangeDBSchema))
	if string(again.Details) != string(item.Details) {
		t.Fatalf("details changed: %s vs %s", again.Details, item.Details)
	}

	// Pending pre-merge files: the pre-merge one until LADO applies it.
	pending, err := f.s.PendingPreMerge(context.Background(), testProject, details)
	if err != nil || len(pending) != 1 || pending[0].Path != "db/010_b.sql" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	f.store.applications = append(f.store.applications, domain.LadoDBChangeApplication{
		ProjectID: testProject, Path: "db/010_b.sql", BlobSHA: want[1].BlobSHA, Result: domain.LadoDBChangeResultFailed,
	})
	if pending, _ := f.s.PendingPreMerge(context.Background(), testProject, details); len(pending) != 1 {
		t.Fatalf("a failed application counts as applied: %+v", pending)
	}
	f.store.applications = append(f.store.applications, domain.LadoDBChangeApplication{
		ProjectID: testProject, Path: "db/010_b.sql", BlobSHA: want[1].BlobSHA, Result: domain.LadoDBChangeResultApplied,
	})
	if pending, err := f.s.PendingPreMerge(context.Background(), testProject, details); err != nil || len(pending) != 0 {
		t.Fatalf("pending after applied = %+v, %v", pending, err)
	}
}

func TestItemOutcomes(t *testing.T) {
	ctx := context.Background()
	t.Run("nothing", func(t *testing.T) {
		f := newDetectFixture(t)
		head := f.repo.commit(f.repo.dir, map[string]string{"README.md": "changed\n"}, nil, "Docs")
		item, ok, err := f.s.Item(ctx, f.input(head))
		if err != nil || !ok || item.Outcome != domain.LadoVerifyPass || item.Code != domain.LadoCodeNoDBChanges {
			t.Fatalf("item = %+v, %v, %v", item, ok, err)
		}
	})
	t.Run("unplanned", func(t *testing.T) {
		f := newDetectFixture(t)
		head := f.repo.commit(f.repo.dir, map[string]string{"db/003.sql": "SELECT 1;\n"}, nil, "SQL")
		item, _, err := f.s.Item(ctx, f.input(head, domain.LadoChangeAuth))
		if err != nil || item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeDBChangeUnplanned {
			t.Fatalf("item = %+v, %v", item, err)
		}
	})
	t.Run("applied modified and deleted", func(t *testing.T) {
		f := newDetectFixture(t)
		applied := f.repo.blob(f.base, "db/001_init.sql")
		head := f.repo.commit(f.repo.dir, map[string]string{"db/001_init.sql": "CREATE TABLE a (id bigint);\n"}, []string{"db/002_seed.sql"}, "Edit")
		item, _, err := f.s.Item(ctx, f.input(head, domain.LadoChangeDBSchema))
		if err != nil || item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeDBChangeAppliedModified {
			t.Fatalf("item = %+v, %v", item, err)
		}
		details, _, _ := domain.LadoVerification{Items: []domain.LadoVerifyItem{item}}.DBChanges()
		if len(details.Files) != 0 || len(details.Modified) != 2 {
			t.Fatalf("details = %+v", details)
		}
		if m := details.Modified[0]; m.Path != "db/001_init.sql" || m.AppliedBlobSHA != applied || m.HeadBlobSHA != f.repo.blob(head, "db/001_init.sql") || m.Deleted {
			t.Fatalf("modified = %+v", m)
		}
		if m := details.Modified[1]; m.Path != "db/002_seed.sql" || !m.Deleted || m.HeadBlobSHA != "" {
			t.Fatalf("deleted = %+v", m)
		}
	})
	t.Run("secrets missing", func(t *testing.T) {
		f := newDetectFixture(t)
		f.s.secretsMu.Lock()
		_ = f.s.writeSecrets(testProject, "", nil)
		f.s.secretsMu.Unlock()
		head := f.repo.commit(f.repo.dir, map[string]string{"db/003.sql": "SELECT 1;\n"}, nil, "SQL")
		item, _, err := f.s.Item(ctx, f.input(head, domain.LadoChangeDBSchema))
		if err != nil || item.Outcome != domain.LadoVerifyFail || item.Code != domain.LadoCodeDBChangeSecretsMissing {
			t.Fatalf("item = %+v, %v", item, err)
		}
		// No SQL file, no secret needed.
		head = f.repo.commit(f.repo.dir, nil, []string{"db/003.sql"}, "No SQL")
		if item, _, _ := f.s.Item(ctx, f.input(head)); item.Code != domain.LadoCodeNoDBChanges {
			t.Fatalf("item = %+v", item)
		}
	})
	t.Run("merge base", func(t *testing.T) {
		// Base names main after another merge: only the head's own files
		// count, not main's new ones.
		f := newDetectFixture(t)
		head := f.repo.commit(f.repo.dir, map[string]string{"db/005.sql": "SELECT 5;\n"}, nil, "Mine")
		f.repo.push(map[string]string{"db/004.sql": "SELECT 4;\n"}, "Theirs")
		f.repo.git(f.repo.dir, "fetch", "-q", "origin")
		in := f.input(head, domain.LadoChangeDBSchema)
		in.Base = f.repo.git(f.repo.dir, "rev-parse", "origin/main")
		item, _, err := f.s.Item(ctx, in)
		details, _, _ := domain.LadoVerification{Items: []domain.LadoVerifyItem{item}}.DBChanges()
		if err != nil || len(details.Files) != 1 || details.Files[0].Path != "db/005.sql" {
			t.Fatalf("details = %+v, %v", details, err)
		}
	})
}

func TestItemWithSettingsOff(t *testing.T) {
	f := newDetectFixture(t)
	head := f.repo.commit(f.repo.dir, map[string]string{"db/003.sql": "SELECT 1;\n"}, nil, "SQL")
	ctx := context.Background()
	// Another repository has no settings.
	in := f.input(head)
	in.Repo = "front"
	if _, ok, err := f.s.Item(ctx, in); ok || err != nil {
		t.Fatalf("Item without settings = %v, %v", ok, err)
	}
	current := f.store.settings[settingsKey(testProject, "")]
	update := onUpdate(current.SHA256())
	update.Paths, update.Enabled, update.Secrets = current.Paths, false, domain.LadoDBChangeSecretsUpdate{}
	if _, err := f.s.UpdateDBChangeSettings(ctx, testProject, update, (&presenceOK{n: 10}).fn); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.s.Item(ctx, f.input(head)); ok || err != nil {
		t.Fatalf("Item with settings off = %v, %v", ok, err)
	}
	details := domain.LadoDBChangesDetails{Files: []domain.LadoDBChangeFileRef{{Path: "db/003.sql", Phase: domain.LadoDBChangePreMerge}}}
	if pending, err := f.s.PendingPreMerge(ctx, testProject, details); err != nil || len(pending) != 0 {
		t.Fatalf("pending with settings off = %+v, %v", pending, err)
	}
}

func TestItemErrors(t *testing.T) {
	f := newDetectFixture(t)
	ctx := context.Background()
	if _, _, err := f.s.Item(ctx, f.input("not-a-sha")); err == nil {
		t.Fatal("a head that is no commit id")
	}
	head := f.repo.commit(f.repo.dir, map[string]string{"db/003.sql": "SELECT 1;\n"}, nil, "SQL")
	f.store.readErr = errors.New("disk gone")
	if _, _, err := f.s.Item(ctx, f.input(head)); err == nil || !strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("err = %v", err)
	}
}

func TestFileContent(t *testing.T) {
	f := newDetectFixture(t)
	big := strings.Repeat("가", domain.LadoDBChangeFileContentMax/3+10)
	head := f.repo.commit(f.repo.dir, map[string]string{"db/big.sql": big, "db/small.sql": "SELECT 1;\n"}, nil, "Big")
	ctx := context.Background()
	small, err := f.s.FileContent(ctx, f.repo.dir, domain.LadoDBChangeFileRef{Path: "db/small.sql", BlobSHA: f.repo.blob(head, "db/small.sql")})
	if err != nil || small.Content != "SELECT 1;\n" || small.Truncated {
		t.Fatalf("small = %+v, %v", small, err)
	}
	cut, err := f.s.FileContent(ctx, f.repo.dir, domain.LadoDBChangeFileRef{Path: "db/big.sql", BlobSHA: f.repo.blob(head, "db/big.sql")})
	if err != nil || !cut.Truncated || len(cut.Content) > domain.LadoDBChangeFileContentMax || strings.ContainsRune(cut.Content, '�') {
		t.Fatalf("cut = %d bytes truncated %v, %v", len(cut.Content), cut.Truncated, err)
	}
	if _, err := f.s.FileContent(ctx, f.repo.dir, domain.LadoDBChangeFileRef{BlobSHA: "--help"}); err == nil {
		t.Fatal("a blob that is no object id")
	}
}
