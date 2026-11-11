package ladodbchange

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const testDSN = "postgres://deploy:s3cr3t-pa55@db.internal:5432/app"

// onUpdate is an update that turns the root repository's settings on.
func onUpdate(base string) domain.LadoDBChangeSettingsUpdate {
	return domain.LadoDBChangeSettingsUpdate{
		Enabled: true, Paths: []string{"db/migrations/*.sql"}, Command: `psql "$LADO_DB_DSN" -v ON_ERROR_STOP=1 -f {file}`,
		TimeoutMinutes: 5, SecretNames: []string{"LADO_DB_DSN"},
		Secrets: domain.LadoDBChangeSecretsUpdate{Set: map[string]string{"LADO_DB_DSN": testDSN}}, Base: base,
	}
}

func apiCode(t *testing.T, err error) (string, map[string]any) {
	t.Helper()
	var e *apierr.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is no *apierr.Error", err)
	}
	return e.Code, e.Details
}

func TestUpdateSettingsRecordsTheDefaultBranchAsBaseline(t *testing.T) {
	repo := newTestRepo(t, map[string]string{
		"db/migrations/001_init.sql": "CREATE TABLE a (id int);\n",
		"db/migrations/002_drop.sql": "-- lado:after-deploy\nDROP TABLE b;\n",
		"db/seed/data.sql":           "INSERT INTO a VALUES (1);\n",
		"db/migrations/notes.md":     "not SQL\n",
	})
	// Another developer pushed a file the clone has not fetched yet.
	tip := repo.push(map[string]string{"db/migrations/003_more.sql": "ALTER TABLE a ADD b int;\n"}, "More")
	store := newFakeStore()
	store.addProject(testProject, repo.dir)
	s := newTestService(t, store)
	presence := &presenceOK{}

	view, err := s.UpdateDBChangeSettings(context.Background(), testProject, onUpdate(""), presence.fn)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"project.db_changes app:"}; !slices.Equal(presence.asked, want) {
		t.Fatalf("presence asked %v, want %v", presence.asked, want)
	}
	if len(view.Repositories) != 1 {
		t.Fatalf("view = %+v", view)
	}
	got := view.Repositories[0]
	if !got.Enabled || got.BaselineCommit != tip || !got.EnabledAt.Equal(t0) || !slices.Equal(got.StoredSecretNames, []string{"LADO_DB_DSN"}) {
		t.Fatalf("settings = %+v", got)
	}
	var paths []string
	for _, a := range store.applications {
		if a.Result != domain.LadoDBChangeResultBaseline || a.HeadSHA != tip || a.BlobSHA != repo.blob(tip, a.Path) {
			t.Fatalf("baseline row %+v", a)
		}
		paths = append(paths, a.Path+":"+string(a.Phase))
	}
	want := []string{"db/migrations/001_init.sql:pre_merge", "db/migrations/002_drop.sql:after_deploy", "db/migrations/003_more.sql:pre_merge"}
	if !slices.Equal(paths, want) {
		t.Fatalf("baseline = %v, want %v", paths, want)
	}
	actions := []string{}
	for _, ev := range store.audit {
		actions = append(actions, ev.Action)
		if strings.Contains(string(ev.Payload), "s3cr3t") || strings.Contains(string(ev.Payload), "psql") {
			t.Fatalf("audit %s holds the secret or the command: %s", ev.Action, ev.Payload)
		}
	}
	if !slices.Equal(actions, []string{domain.LadoAuditDBChangesChanged, domain.LadoAuditDBChangeBaseline}) {
		t.Fatalf("audit = %v", actions)
	}
	if view.SHA256 != domain.LadoDBChangeSettingsListSHA256([]domain.LadoDBChangeSettings{got.LadoDBChangeSettings}) {
		t.Fatal("view hash does not match its settings")
	}
	// The secret is in its file only, 0600 in a 0700 folder.
	values, err := s.Secrets(testProject, "")
	if err != nil || values["LADO_DB_DSN"] != testDSN {
		t.Fatalf("secrets = %v, %v", values, err)
	}
	data, _ := json.Marshal(view)
	if strings.Contains(string(data), "s3cr3t") {
		t.Fatalf("view holds the secret: %s", data)
	}
	if runtime.GOOS != "windows" {
		file, _ := os.Stat(s.secretsPath(testProject, ""))
		dir, _ := os.Stat(filepath.Dir(s.secretsPath(testProject, "")))
		if file.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Fatalf("modes = %v %v", file.Mode().Perm(), dir.Mode().Perm())
		}
	}
	if !strings.HasPrefix(s.secretsPath(testProject, ""), filepath.Join(s.deps.StateDir, "lado", "credentials", "db")) {
		t.Fatalf("secrets path %s", s.secretsPath(testProject, ""))
	}

	// Changing the paths while on records only the files new to them.
	update := onUpdate(got.SHA256())
	update.Paths = []string{"db/migrations/*.sql", "db/seed/*.sql"}
	update.Secrets = domain.LadoDBChangeSecretsUpdate{}
	view, err = s.UpdateDBChangeSettings(context.Background(), testProject, update, presence.fn)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(store.applications); n != 4 || store.applications[3].Path != "db/seed/data.sql" {
		t.Fatalf("applications after the paths changed = %+v", store.applications)
	}
	// Turning off keeps the baseline and asks for presence again.
	update = onUpdate(view.Repositories[0].SHA256())
	update.Enabled, update.Secrets = false, domain.LadoDBChangeSecretsUpdate{}
	view, err = s.UpdateDBChangeSettings(context.Background(), testProject, update, presence.fn)
	if err != nil {
		t.Fatal(err)
	}
	if off := view.Repositories[0]; off.Enabled || off.BaselineCommit != tip || len(presence.asked) != 3 || len(store.applications) != 4 {
		t.Fatalf("off = %+v, asked %v", off, presence.asked)
	}
}

func TestUpdateSettingsRefusesWhenTheBaselineFails(t *testing.T) {
	repo := newTestRepo(t, nil)
	store := newFakeStore()
	store.addProject(testProject, repo.dir)
	s := newTestService(t, store)
	// The origin is gone, so the fetch fails.
	if err := os.RemoveAll(repo.origin); err != nil {
		t.Fatal(err)
	}
	_, err := s.UpdateDBChangeSettings(context.Background(), testProject, onUpdate(""), (&presenceOK{}).fn)
	if code, details := apiCode(t, err); code != "LADO_DB_CHANGE_BASELINE_FAILED" || details["reason"] == "" {
		t.Fatalf("err = %v", err)
	}
	if len(store.settings) != 0 || len(store.applications) != 0 || len(store.audit) != 0 {
		t.Fatalf("something was saved: %+v", store)
	}
	if values, _ := s.Secrets(testProject, ""); len(values) != 0 {
		t.Fatalf("secrets were written: %v", values)
	}
}

func TestUpdateSettingsBaselineUsesTheConfiguredBranchWithoutOriginHead(t *testing.T) {
	repo := newTestRepo(t, map[string]string{"db/1.sql": "SELECT 1;\n"})
	repo.git(repo.dir, "remote", "set-head", "origin", "--delete")
	store := newFakeStore()
	store.addProject(testProject, repo.dir)
	s := newTestService(t, store)
	update := onUpdate("")
	update.Paths = []string{"db/*.sql"}
	if _, err := s.UpdateDBChangeSettings(context.Background(), testProject, update, (&presenceOK{}).fn); err == nil {
		t.Fatal("no default branch was accepted")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_BASELINE_FAILED" {
		t.Fatalf("err = %v", err)
	}
	p := store.projects[string(testProject)]
	p.Config.DefaultBranch = "main"
	store.projects[string(testProject)] = p
	view, err := s.UpdateDBChangeSettings(context.Background(), testProject, update, (&presenceOK{}).fn)
	if err != nil {
		t.Fatal(err)
	}
	if view.Repositories[0].BaselineCommit == "" || len(store.applications) != 1 {
		t.Fatalf("view = %+v, applications %+v", view, store.applications)
	}
}

func TestUpdateSettingsRefusals(t *testing.T) {
	repo := newTestRepo(t, nil)
	store := newFakeStore()
	store.addProject(testProject, repo.dir)
	s := newTestService(t, store)
	ctx := context.Background()

	// Invalid settings name every problem, the reserved secret names too.
	bad := onUpdate("")
	bad.Paths = []string{"../db/*.sql"}
	bad.Command = "psql"
	bad.SecretNames = []string{"PATH", "GIT_SSH_COMMAND", "lower"}
	bad.Secrets.Set = map[string]string{"OTHER": "value"}
	_, err := s.UpdateDBChangeSettings(ctx, testProject, bad, (&presenceOK{}).fn)
	code, details := apiCode(t, err)
	if code != "LADO_DB_CHANGE_INVALID" {
		t.Fatalf("err = %v", err)
	}
	problems, _ := details["problems"].([]map[string]string)
	var fields []string
	for _, p := range problems {
		fields = append(fields, p["field"]+":"+p["reason"])
	}
	for _, want := range []string{"paths[0]:parent_dir", "command:no_file_placeholder", "secretNames[0]:reserved", "secretNames[1]:reserved", "secretNames[2]:invalid", "secrets.set.OTHER:invalid"} {
		if !slices.Contains(fields, want) {
			t.Fatalf("problems %v lack %s", fields, want)
		}
	}

	// An unknown repository of a single-repository project.
	other := onUpdate("")
	other.Repo = "front"
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, other, (&presenceOK{}).fn); err == nil {
		t.Fatal("unknown repository accepted")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_INVALID" {
		t.Fatalf("err = %v", err)
	}

	// Without presence.
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, onUpdate(""), nil); err == nil {
		t.Fatal("no presence accepted")
	} else if code, _ := apiCode(t, err); code != "LADO_PRESENCE_REQUIRED" {
		t.Fatalf("err = %v", err)
	}

	// A base that is not the current settings'.
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, onUpdate("abc"), (&presenceOK{}).fn); err == nil {
		t.Fatal("stale base accepted")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_SETTINGS_CHANGED" {
		t.Fatalf("err = %v", err)
	}

	// A proof used before.
	store.usedProofs["nonce-1"] = true
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, onUpdate(""), (&presenceOK{}).fn); err == nil {
		t.Fatal("replayed proof accepted")
	} else if code, _ := apiCode(t, err); code != "LADO_PRESENCE_REPLAYED" {
		t.Fatalf("err = %v", err)
	}

	// A change the store finds stale after the check.
	store.failSet = domain.ErrLadoDBChangeSettingsChanged
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, onUpdate(""), (&presenceOK{n: 5}).fn); err == nil {
		t.Fatal("store conflict accepted")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_SETTINGS_CHANGED" {
		t.Fatalf("err = %v", err)
	}
	if values, _ := s.Secrets(testProject, ""); len(values) != 0 {
		t.Fatalf("secrets written for a refused change: %v", values)
	}
	if len(store.settings) != 0 {
		t.Fatalf("settings saved: %+v", store.settings)
	}
}

func TestUpdateSettingsSecretsOnlyAndNoChange(t *testing.T) {
	repo := newTestRepo(t, nil)
	store := newFakeStore()
	store.addProject(testProject, repo.dir)
	s := newTestService(t, store)
	ctx := context.Background()
	presence := &presenceOK{}
	view, err := s.UpdateDBChangeSettings(ctx, testProject, onUpdate(""), presence.fn)
	if err != nil {
		t.Fatal(err)
	}
	base := view.Repositories[0].SHA256()

	// The same settings without secrets change nothing and ask nothing.
	same := onUpdate(base)
	same.Secrets = domain.LadoDBChangeSecretsUpdate{}
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, same, presence.fn); err != nil || len(presence.asked) != 1 {
		t.Fatalf("no-op: %v, asked %v", err, presence.asked)
	}
	// Setting the same value again is no change either.
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, onUpdate(base), presence.fn); err != nil || len(presence.asked) != 1 {
		t.Fatalf("same value: %v, asked %v", err, presence.asked)
	}

	// A new value is a change that needs presence; the audit names it.
	next := onUpdate(base)
	next.Secrets.Set = map[string]string{"LADO_DB_DSN": "postgres://deploy:another-pass@db/app"}
	if _, err := s.UpdateDBChangeSettings(ctx, testProject, next, presence.fn); err != nil || len(presence.asked) != 2 {
		t.Fatalf("new value: %v, asked %v", err, presence.asked)
	}
	last := store.audit[len(store.audit)-1]
	if !strings.Contains(string(last.Payload), `"secretsSet":["LADO_DB_DSN"]`) || strings.Contains(string(last.Payload), "another-pass") {
		t.Fatalf("audit = %s", last.Payload)
	}

	// Dropping the name drops its value.
	drop := onUpdate(base)
	drop.SecretNames, drop.Secrets = nil, domain.LadoDBChangeSecretsUpdate{}
	view, err = s.UpdateDBChangeSettings(ctx, testProject, drop, presence.fn)
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Repositories[0].StoredSecretNames; len(got) != 0 {
		t.Fatalf("stored = %v", got)
	}
	if _, err := os.Stat(s.secretsPath(testProject, "")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secrets file left: %v", err)
	}
}

func TestSettingsViewOfAWorkspace(t *testing.T) {
	back := newTestRepo(t, map[string]string{"sql/1.sql": "SELECT 1;\n"})
	root := filepath.Dir(back.dir)
	store := newFakeStore()
	store.projects["ws"] = domain.ProjectRecord{ID: "ws", Path: root, Kind: domain.ProjectKindWorkspace}
	store.workspaces["ws"] = []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "back", RelativePath: "app"},
		{ProjectID: "ws", Name: "front", RelativePath: "front"},
	}
	// A repository the workspace no longer lists still shows.
	store.settings[settingsKey("ws", "old")] = domain.LadoDBChangeSettings{ProjectID: "ws", Repo: "old", Enabled: true, Paths: []string{"x/*.sql"}, Command: "x {file}"}
	s := newTestService(t, store)
	ctx := context.Background()
	presence := &presenceOK{}

	update := onUpdate("")
	update.Repo, update.Paths = "app", []string{"sql/*.sql"}
	view, err := s.UpdateDBChangeSettings(ctx, "ws", update, presence.fn)
	if err != nil {
		t.Fatal(err)
	}
	var repos []string
	for _, r := range view.Repositories {
		repos = append(repos, r.Repo+"="+r.Name)
	}
	if !slices.Equal(repos, []string{"app=back", "front=front", "old=old"}) {
		t.Fatalf("repos = %v", repos)
	}
	if !view.Repositories[0].Enabled || len(store.applications) != 1 || store.applications[0].Repo != "app" {
		t.Fatalf("view %+v, applications %+v", view.Repositories[0], store.applications)
	}
	dir, err := s.RepoDir(ctx, "ws", "app")
	if err != nil || dir != back.dir {
		t.Fatalf("RepoDir = %q, %v", dir, err)
	}
	if _, err := s.RepoDir(ctx, "ws", "nope"); err == nil {
		t.Fatal("RepoDir of an unknown repository")
	}
	// The repository the workspace dropped can be turned off, not on.
	old := store.settings[settingsKey("ws", "old")]
	on := domain.LadoDBChangeSettingsUpdate{Repo: "old", Enabled: true, Paths: old.Paths, Command: old.Command, Base: old.SHA256()}
	if _, err := s.UpdateDBChangeSettings(ctx, "ws", on, presence.fn); err == nil {
		t.Fatal("a dropped repository was turned on")
	}
	on.Enabled = false
	if _, err := s.UpdateDBChangeSettings(ctx, "ws", on, presence.fn); err != nil {
		t.Fatal(err)
	}
	// The secrets of two repositories are separate files.
	if s.secretsPath("ws", "app") == s.secretsPath("ws", "front") {
		t.Fatal("two repositories share a secrets file")
	}
}

func TestSettingsOfAnUnknownProject(t *testing.T) {
	s := newTestService(t, newFakeStore())
	if _, err := s.DBChangeSettings(context.Background(), "nope"); err == nil {
		t.Fatal("unknown project")
	} else if code, _ := apiCode(t, err); code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
}
