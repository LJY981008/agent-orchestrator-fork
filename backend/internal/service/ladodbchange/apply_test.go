package ladodbchange

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

func TestQuoteShellPath(t *testing.T) {
	quoted, err := quoteShellPath("/tmp/it's here/a.sql", false)
	if err != nil {
		t.Fatal(err)
	}
	// sh reads the quoted path back as it was.
	out, err := exec.Command("sh", "-c", "printf %s "+quoted).Output()
	if err != nil || string(out) != "/tmp/it's here/a.sql" {
		t.Fatalf("sh read %q, %v", out, err)
	}
	if got, err := quoteShellPath(`C:\ao\tmp\a.sql`, true); err != nil || got != `"C:\ao\tmp\a.sql"` {
		t.Fatalf("windows = %q, %v", got, err)
	}
	for _, bad := range []string{`C:\a"b.sql`, `C:\100%.sql`} {
		if _, err := quoteShellPath(bad, true); err == nil {
			t.Fatalf("%q was quoted for cmd", bad)
		}
	}
}

func TestApplyFileName(t *testing.T) {
	cases := map[string]string{
		"db/migrations/001_init.sql": "001_init.sql",
		"db/it's $(rm).sql":          "it_s___rm_.sql",
		"db/한글.sql":                  "__.sql",
		"..":                         "change.sql",
	}
	for in, want := range cases {
		if got := applyFileName(in); got != want {
			t.Errorf("applyFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandEnvTakesSecretsLast(t *testing.T) {
	store := newFakeStore()
	store.projects[string(testProject)] = domain.ProjectRecord{ID: string(testProject), Config: domain.ProjectConfig{Env: map[string]string{"PROJECT_VAR": "p", "LADO_DB_DSN": "project"}}}
	s := newTestService(t, store)
	s.deps.Env = func() []string {
		return []string{"PATH=/bin", "AO_SESSION_ID=x", "GITHUB_TOKEN=t", "lado_db_dsn=daemon"}
	}
	env, err := s.commandEnv(context.Background(), testProject, map[string]string{"LADO_DB_DSN": testDSN})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "PATH=/bin") || !slices.Contains(env, "PROJECT_VAR=p") || env[len(env)-1] != "LADO_DB_DSN="+testDSN {
		t.Fatalf("env = %v", env)
	}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "AO_") || name == "GITHUB_TOKEN" || (strings.EqualFold(name, "LADO_DB_DSN") && entry != "LADO_DB_DSN="+testDSN) {
			t.Fatalf("env keeps %q: %v", entry, env)
		}
	}
}

func TestApplyFileRunsTheBlobInAFolderOfItsOwn(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	var spec ladoverify.CommandSpec
	f.s.deps.Run = func(ctx context.Context, got ladoverify.CommandSpec) ladoverify.CommandResult {
		spec = got
		data, err := os.ReadFile(filepath.Join(got.Dir, "002_add.sql"))
		if err != nil || string(data) != "ALTER TABLE a ADD b int;\n" {
			t.Errorf("the command's file = %q, %v", data, err)
		}
		code := 0
		return ladoverify.CommandResult{Status: domain.LadoCheckRunPassed, ExitCode: &code, Output: []byte("ALTER TABLE " + testDSN + "\n")}
	}
	settings, _, _ := f.s.Enabled(context.Background(), testProject, "")
	run := domain.LadoDBChangeRun{ID: "r1", ProjectID: testProject, ExecutionSessionID: testSession, TaskID: testTask, PRURL: testPRURL, HeadSHA: f.head}
	file := domain.LadoDBChangeFileRef{Path: "db/002_add.sql", BlobSHA: f.repo.blob(f.head, "db/002_add.sql"), Phase: domain.LadoDBChangePreMerge}
	res, err := f.s.applyFile(context.Background(), applyTarget{run: run, settings: settings, dir: f.repo.dir, actor: "lado"}, file)
	if err != nil || !res.applied {
		t.Fatalf("applyFile = %+v, %v", res, err)
	}
	if !strings.HasPrefix(spec.Dir, filepath.Join(f.s.deps.StateDir, "lado", "tmp", "db-change-")) || spec.Timeout != settings.Timeout() ||
		!strings.Contains(spec.Command, "'"+filepath.Join(spec.Dir, "002_add.sql")+"'") || strings.Contains(spec.Command, "{file}") {
		t.Fatalf("spec = %+v", spec)
	}
	if !slices.Contains(spec.Env, "LADO_DB_DSN="+testDSN) || !slices.Contains(spec.Env, "PROJECT_VAR=from-project") {
		t.Fatalf("env = %v", spec.Env)
	}
	if _, err := os.Stat(spec.Dir); !os.IsNotExist(err) {
		t.Fatalf("the folder stays: %v", err)
	}
	last := f.store.applications[len(f.store.applications)-1]
	output, _ := f.blobs.Get(last.OutputSHA256, 1<<20)
	if last.Result != domain.LadoDBChangeResultApplied || strings.Contains(string(output), "s3cr3t-pa55") {
		t.Fatalf("application = %+v, output %q", last, output)
	}
	audit := f.store.audit[len(f.store.audit)-1]
	if audit.Action != domain.LadoAuditDBChangeApplied || strings.Contains(string(audit.Payload), "ALTER") ||
		!strings.Contains(string(audit.Payload), `"outputSha256":"`+last.OutputSHA256+`"`) {
		t.Fatalf("audit = %s %s", audit.Action, audit.Payload)
	}

	// A blob whose phase is not the file's does not run.
	ran := false
	f.s.deps.Run = func(context.Context, ladoverify.CommandSpec) ladoverify.CommandResult {
		ran = true
		return ladoverify.CommandResult{Status: domain.LadoCheckRunPassed}
	}
	wrong := domain.LadoDBChangeFileRef{Path: "db/003_drop.sql", BlobSHA: f.repo.blob(f.head, "db/003_drop.sql"), Phase: domain.LadoDBChangePreMerge}
	res, err = f.s.applyFile(context.Background(), applyTarget{run: run, settings: settings, dir: f.repo.dir, actor: "lado"}, wrong)
	if err != nil || res.applied || ran || !strings.Contains(res.message, "phase") {
		t.Fatalf("applyFile = %+v, %v, ran %v", res, err, ran)
	}

	// A secret without a value fails before anything runs or is recorded.
	before := len(f.store.applications)
	if err := os.Remove(f.s.secretsPath(testProject, "")); err != nil {
		t.Fatal(err)
	}
	res, err = f.s.applyFile(context.Background(), applyTarget{run: run, settings: settings, dir: f.repo.dir, actor: "lado"}, file)
	if err != nil || res.applied || ran || !strings.Contains(res.message, "LADO_DB_DSN") || len(f.store.applications) != before {
		t.Fatalf("applyFile = %+v, %v, ran %v", res, err, ran)
	}
}
