package ladodbchange

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

var _ ladoverify.DBChanges = (*Service)(nil)

const (
	testSession domain.SessionID  = "s1"
	testTask    domain.LadoTaskID = "t1"
	testPRURL                     = "https://github.com/o/app/pull/7"
)

// fakeLive is the host's view of the PR.
type fakeLive struct {
	mu     sync.Mutex
	head   string
	merged bool
	closed bool
	err    error
}

func (l *fakeLive) LivePR(context.Context, domain.PullRequest) (ladoverify.LivePR, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ladoverify.LivePR{HeadSHA: l.head, TargetBranch: "main", Merged: l.merged, Closed: l.closed}, l.err
}

// fakeStatuses is ladoverify's side: the merge gate's preflight and the
// status post.
type fakeStatuses struct {
	mu           sync.Mutex
	preflightErr error
	reposts      int
	heldAtRepost []bool
	store        *fakeStore
}

func (f *fakeStatuses) RepostIntegrationStatusOf(_ context.Context, _ domain.SessionID, _ string) (domain.LadoIntegrationApproval, error) {
	held, _ := f.store.LadoIntegrationStatusHeld(context.Background(), "ap1")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reposts++
	f.heldAtRepost = append(f.heldAtRepost, held)
	return domain.LadoIntegrationApproval{ID: "ap1"}, nil
}

func (f *fakeStatuses) PreflightMerge(context.Context, domain.PullRequest, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.preflightErr
}

// fakeMerger reads the PR's merge readiness and merges it: mergeErrs fail
// the first merges in turn, and a merge that succeeds marks the PR merged.
type fakeMerger struct {
	mu        sync.Mutex
	readiness domain.LadoMergeReadiness
	readErr   error
	mergeErrs []error
	merges    int
	reads     int
	store     *fakeStore
}

func (m *fakeMerger) LadoMergeReadiness(context.Context, string, int, string) (domain.LadoMergeReadiness, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	return m.readiness, m.readErr
}

func (m *fakeMerger) MergeLadoPR(_ context.Context, prURL string, _ int, _ string) error {
	m.mu.Lock()
	m.merges++
	var err error
	if len(m.mergeErrs) > 0 {
		err, m.mergeErrs = m.mergeErrs[0], m.mergeErrs[1:]
	}
	m.mu.Unlock()
	if err == nil {
		m.store.setMerged(prURL)
	}
	return err
}

func (f *fakeStore) setMerged(prURL string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr := f.prs[prURL]
	pr.Merged = true
	f.prs[prURL] = pr
}

// jobFixture is a project whose settings are on, a PR head with SQL files,
// its record, execution and approval, and the host's fakes.
type jobFixture struct {
	t        *testing.T
	repo     *testRepo
	store    *fakeStore
	s        *Service
	blobs    *fakeBlobs
	live     *fakeLive
	statuses *fakeStatuses
	merger   *fakeMerger
	logPath  string
	flagPath string
	head     string
	exec     domain.LadoExecution
	approval domain.LadoIntegrationApproval
	presence *presenceOK

	clockMu sync.Mutex
	now     time.Time
}

// defaultHeadFiles are the head's SQL files: two pre-merge files, the second
// failing while the flag file exists, and an after-deploy file.
func defaultHeadFiles() map[string]string {
	return map[string]string{
		"db/002_add.sql":  "ALTER TABLE a ADD b int;\n",
		"db/003_drop.sql": "-- lado:after-deploy\nDROP TABLE old;\n",
		"db/004_boom.sql": "-- boom\nALTER TABLE a ADD c int;\n",
	}
}

func newJobFixture(t *testing.T, headFiles map[string]string) *jobFixture {
	t.Helper()
	f := &jobFixture{t: t, now: t0, presence: &presenceOK{}}
	f.repo = newTestRepo(t, map[string]string{"db/001_init.sql": "CREATE TABLE a (id int);\n"})
	f.store = newFakeStore()
	f.store.addProject(testProject, f.repo.dir)
	f.store.projects[string(testProject)] = domain.ProjectRecord{
		ID: string(testProject), Path: f.repo.dir, DisplayName: "app",
		Config: domain.ProjectConfig{Env: map[string]string{"PROJECT_VAR": "from-project"}},
	}
	f.store.tasks[testTask] = domain.LadoTask{ID: testTask, ProjectID: testProject}
	f.blobs = &fakeBlobs{}
	f.live = &fakeLive{}
	f.statuses = &fakeStatuses{store: f.store}
	f.merger = &fakeMerger{store: f.store, readiness: domain.LadoMergeReadiness{
		Blockers: []string{domain.MergeBlockerCIPending, domain.MergeBlockerNotMergeable}, Mergeability: domain.MergeBlocked,
	}}
	n := 0
	f.s = New(Deps{
		Store: f.store, Blobs: f.blobs, StateDir: t.TempDir(), Statuses: f.statuses, MergeChecker: f.merger, LiveHeads: f.live,
		Clock: f.clock, NewID: func() string { n++; return fmt.Sprintf("id-%d", n) },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	work := t.TempDir()
	f.logPath, f.flagPath = filepath.Join(work, "applied.log"), filepath.Join(work, "fail")
	update := onUpdate("")
	update.Paths = []string{"db/*.sql"}
	update.Command = fmt.Sprintf(`if [ -f '%s' ] && grep -q boom {file}; then echo "connect $LADO_DB_DSN failed: boom" >&2; exit 3; fi; cat {file} >> '%s'`, f.flagPath, f.logPath)
	if _, err := f.s.UpdateDBChangeSettings(context.Background(), testProject, update, f.presence.fn); err != nil {
		t.Fatal(err)
	}

	f.repo.git(f.repo.dir, "checkout", "-q", "-b", "feature")
	f.head = f.repo.commit(f.repo.dir, headFiles, nil, "Add SQL")
	f.live.head = f.head
	item, ok, err := f.s.Item(context.Background(), f.input())
	if err != nil || !ok {
		t.Fatalf("Item = %v, %v", ok, err)
	}
	f.store.records[string(testSession)+"\x00"+f.head] = domain.LadoVerification{
		ExecutionSessionID: testSession, HeadSHA: f.head, PRURL: testPRURL, Items: []domain.LadoVerifyItem{item},
	}
	f.exec = domain.LadoExecution{
		SessionID: testSession, TaskID: testTask,
		PR: domain.LadoExecutionPR{URL: testPRURL, Number: 7, HeadSHA: f.head, TargetBranch: "main"},
	}
	f.store.executions[testSession] = f.exec
	f.store.prs[testPRURL] = domain.PullRequest{URL: testPRURL, SessionID: testSession, Number: 7, HeadSHA: f.head, TargetBranch: "main"}
	f.approval = domain.LadoIntegrationApproval{
		ID: "ap1", ExecutionSessionID: testSession, PRURL: testPRURL, HeadSHA: f.head, TargetBranch: "main", ApprovedAt: t0,
	}
	f.store.approvals = append(f.store.approvals, f.approval)
	// ladoverify held the status when it recorded the approval.
	if _, err := f.s.HoldIntegrationStatus(context.Background(), f.approval, f.store.records[string(testSession)+"\x00"+f.head]); err != nil {
		t.Fatalf("HoldIntegrationStatus: %v", err)
	}
	return f
}

func (f *jobFixture) clock() time.Time {
	f.clockMu.Lock()
	defer f.clockMu.Unlock()
	return f.now
}

func (f *jobFixture) advance(d time.Duration) {
	f.clockMu.Lock()
	defer f.clockMu.Unlock()
	f.now = f.now.Add(d)
}

func (f *jobFixture) input() domain.LadoDBChangeInput {
	return domain.LadoDBChangeInput{
		ProjectID: testProject, Dir: f.repo.dir, Base: "refs/remotes/origin/main", Head: f.head,
		Plan: domain.LadoPlan{ChangeKinds: []string{domain.LadoChangeDBSchema}},
	}
}

func (f *jobFixture) tick() {
	f.t.Helper()
	if err := f.s.Tick(context.Background()); err != nil {
		f.t.Fatalf("Tick: %v", err)
	}
}

// run returns the only run of kind.
func (f *jobFixture) run(kind domain.LadoDBChangeRunKind) domain.LadoDBChangeRun {
	f.t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	var found []domain.LadoDBChangeRun
	for _, run := range f.store.runs {
		if run.Kind == kind {
			found = append(found, run)
		}
	}
	if len(found) != 1 {
		f.t.Fatalf("runs of %s = %+v", kind, found)
	}
	return found[0]
}

func (f *jobFixture) applied() string {
	data, err := os.ReadFile(f.logPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		f.t.Fatal(err)
	}
	return string(data)
}

func (f *jobFixture) noticeKeys() []string {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	var keys []string
	for _, n := range f.store.notices {
		keys = append(keys, strings.SplitN(n.Key, ":", 2)[0])
	}
	return keys
}

func (f *jobFixture) auditActions() []string {
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	var actions []string
	for _, e := range f.store.audit {
		actions = append(actions, e.Action)
	}
	return actions
}

func (f *jobFixture) held() bool {
	held, _ := f.store.LadoIntegrationStatusHeld(context.Background(), "ap1")
	return held
}

func (f *jobFixture) apply(phase domain.LadoDBChangePhase, files ...string) (domain.LadoDBChangeRun, error) {
	req := domain.LadoDBChangeApplyRequest{SessionID: testSession, PRURL: testPRURL, HeadSHA: f.head, Phase: phase}
	for _, path := range files {
		blob := f.repo.blob(f.head, path)
		req.Files = append(req.Files, domain.LadoDBChangeFileRef{Path: path, BlobSHA: blob, Phase: phase})
	}
	return f.s.ApplyDBChanges(context.Background(), req, f.presence.fn)
}

func TestApprovedSQLAppliesInPathOrderThenMerges(t *testing.T) {
	f := newJobFixture(t, map[string]string{
		"db/010_b.sql":    "ALTER TABLE a ADD b int;\n",
		"db/002_a.sql":    "ALTER TABLE a ADD a int;\n",
		"db/003_drop.sql": "-- lado:after-deploy\nDROP TABLE old;\n",
	})
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	run := f.run(domain.LadoDBChangeRunPreMerge)
	if run.State != domain.LadoDBChangeRunQueued || run.Trigger != domain.LadoDBChangeTriggerApproval || run.ApprovalID != "ap1" {
		t.Fatalf("queued run = %+v", run)
	}
	// A second approval of the same head makes no second run.
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.run(domain.LadoDBChangeRunPreMerge)

	f.tick()
	if got, want := f.applied(), "ALTER TABLE a ADD a int;\nALTER TABLE a ADD b int;\n"; got != want {
		t.Fatalf("applied = %q, want %q (baseline and after-deploy files are not run)", got, want)
	}
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone || run.Attempts != 1 {
		t.Fatalf("run = %+v", run)
	}
	if f.merger.merges != 1 || f.statuses.reposts != 1 || f.held() || !slices.Equal(f.statuses.heldAtRepost, []bool{false}) {
		t.Fatalf("merges %d, reposts %d (held then %v), held %v", f.merger.merges, f.statuses.reposts, f.statuses.heldAtRepost, f.held())
	}
	if got := f.auditActions(); slices.Index(got, domain.LadoAuditDBChangeApplied) < 0 || slices.Contains(got, domain.LadoAuditDBChangeFailed) {
		t.Fatalf("audit = %v", got)
	}
	pending, err := f.s.PendingPreMerge(context.Background(), testProject, mustDetails(t, f))
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after the run = %v, %v", pending, err)
	}
	// The approval's status is no longer held.
	if held, err := f.s.HoldIntegrationStatus(context.Background(), f.approval, f.store.records[string(testSession)+"\x00"+f.head]); err != nil || held {
		t.Fatalf("held = %v, %v", held, err)
	}
	if len(f.store.applications) != 3 { // one baseline row and two applied files
		t.Fatalf("applications = %+v", f.store.applications)
	}
	for _, a := range f.store.applications[1:] {
		if a.Result != domain.LadoDBChangeResultApplied || a.ExitCode == nil || *a.ExitCode != 0 || a.RunID != run.ID || a.Actor != "lado" {
			t.Fatalf("application = %+v", a)
		}
	}
}

func mustDetails(t *testing.T, f *jobFixture) domain.LadoDBChangesDetails {
	t.Helper()
	details, ok, err := f.store.records[string(testSession)+"\x00"+f.head].DBChanges()
	if err != nil || !ok {
		t.Fatalf("details = %v, %v", ok, err)
	}
	return details
}

func TestPreflightStopsBeforeAnySQL(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(f *jobFixture)
		reason  string
	}{
		{"unresolved comments", func(f *jobFixture) {
			f.merger.readiness = domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerUnresolvedComments}}
		}, domain.MergeBlockerUnresolvedComments},
		{"changes requested", func(f *jobFixture) {
			f.merger.readiness = domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerChangesRequested}}
		}, domain.MergeBlockerChangesRequested},
		{"conflict", func(f *jobFixture) {
			f.merger.readiness = domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerNotMergeable}, Mergeability: domain.MergeConflicting}
		}, domain.MergeBlockerNotMergeable},
		{"failing CI", func(f *jobFixture) {
			f.merger.readiness = domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerCIFailing}}
		}, domain.MergeBlockerCIFailing},
		{"merge gate", func(f *jobFixture) {
			f.statuses.preflightErr = apierr.Conflict("LADO_INTEGRATION_MERGE_ORDER", "merge the others first", nil)
		}, "LADO_INTEGRATION_MERGE_ORDER"},
		{"head moved", func(f *jobFixture) { f.live.head = strings.Repeat("e", 40) }, "head_changed"},
		{"closed", func(f *jobFixture) { f.live.closed = true }, domain.MergeBlockerClosed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newJobFixture(t, defaultHeadFiles())
			c.prepare(f)
			f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
			f.tick()
			if f.applied() != "" || f.merger.merges != 0 || !f.held() {
				t.Fatalf("applied %q, merges %d, held %v", f.applied(), f.merger.merges, f.held())
			}
			run := f.run(domain.LadoDBChangeRunPreMerge)
			if run.State != domain.LadoDBChangeRunAwaitingHuman || !strings.Contains(run.Error, c.reason) || run.Attempts != 0 {
				t.Fatalf("run = %+v", run)
			}
			if got := f.noticeKeys(); !slices.Equal(got, []string{"db_preflight"}) {
				t.Fatalf("notices = %v", got)
			}
			if !strings.Contains(f.store.notices[0].Text, c.reason) && c.name != "merge gate" {
				t.Fatalf("notice = %q", f.store.notices[0].Text)
			}
		})
	}
}

func TestPreflightThatCannotReadTheHostWaits(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	f.live.err = errors.New("network down")
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunQueued {
		t.Fatalf("run = %+v", run)
	}
	f.advance(preflightGiveUp + time.Minute)
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunAwaitingHuman || !strings.Contains(run.Error, "network down") {
		t.Fatalf("run = %+v", run)
	}
	if f.applied() != "" {
		t.Fatalf("applied %q", f.applied())
	}
}

func TestFailedFileStopsTheMergeAndARetrySkipsAppliedFiles(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	if err := os.WriteFile(f.flagPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick()
	run := f.run(domain.LadoDBChangeRunPreMerge)
	if run.State != domain.LadoDBChangeRunFailed || !strings.Contains(run.Error, "db/004_boom.sql") || !strings.Contains(run.Error, "exit code 3") {
		t.Fatalf("run = %+v", run)
	}
	if f.applied() != "ALTER TABLE a ADD b int;\n" || f.merger.merges != 0 || f.statuses.reposts != 0 || !f.held() {
		t.Fatalf("applied %q, merges %d, reposts %d, held %v", f.applied(), f.merger.merges, f.statuses.reposts, f.held())
	}
	if got := f.noticeKeys(); !slices.Equal(got, []string{"db_failed"}) || !strings.Contains(f.store.notices[0].Text, "db/004_boom.sql") {
		t.Fatalf("notices = %v %+v", got, f.store.notices)
	}
	if got := f.auditActions(); slices.Index(got, domain.LadoAuditDBChangeFailed) < 0 {
		t.Fatalf("audit = %v", got)
	}

	// The secret's value and its password are nowhere LADO keeps them.
	failed := f.store.applications[len(f.store.applications)-1]
	if failed.Result != domain.LadoDBChangeResultFailed || failed.OutputSHA256 == "" {
		t.Fatalf("failed application = %+v", failed)
	}
	output, err := f.blobs.Get(failed.OutputSHA256, 1<<20)
	if err != nil || !strings.Contains(string(output), Redacted) || !strings.Contains(string(output), "boom") {
		t.Fatalf("output = %q, %v", output, err)
	}
	for _, place := range []string{string(output), failed.Message, run.Error, fmt.Sprint(f.store.audit), fmt.Sprint(f.store.notices)} {
		if strings.Contains(place, "s3cr3t-pa55") || strings.Contains(place, testDSN) {
			t.Fatalf("a secret leaked: %q", place)
		}
	}

	// A retry needs presence and the developer's confirmation of the files
	// left, and skips the file applied already.
	if err := os.Remove(f.flagPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ApplyDBChanges(context.Background(), domain.LadoDBChangeApplyRequest{
		SessionID: testSession, PRURL: testPRURL, HeadSHA: f.head, Phase: domain.LadoDBChangePreMerge,
	}, nil); err == nil {
		t.Fatal("a retry without files or presence was queued")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_FILES_CHANGED" {
		t.Fatalf("code = %s", code)
	}
	if _, err := f.s.ApplyDBChanges(context.Background(), domain.LadoDBChangeApplyRequest{
		SessionID: testSession, PRURL: testPRURL, HeadSHA: f.head, Phase: domain.LadoDBChangePreMerge,
		Files: []domain.LadoDBChangeFileRef{{Path: "db/004_boom.sql", BlobSHA: f.repo.blob(f.head, "db/004_boom.sql"), Phase: domain.LadoDBChangePreMerge}},
	}, nil); err == nil {
		t.Fatal("a retry without presence was queued")
	} else if code, _ := apiCode(t, err); code != "LADO_PRESENCE_REQUIRED" {
		t.Fatalf("code = %s", code)
	}
	queued, err := f.apply(domain.LadoDBChangePreMerge, "db/004_boom.sql")
	if err != nil || queued.State != domain.LadoDBChangeRunQueued {
		t.Fatalf("ApplyDBChanges = %+v, %v", queued, err)
	}
	f.tick()
	if got := f.applied(); got != "ALTER TABLE a ADD b int;\n-- boom\nALTER TABLE a ADD c int;\n" {
		t.Fatalf("applied = %q", got)
	}
	run = f.run(domain.LadoDBChangeRunPreMerge)
	if run.State != domain.LadoDBChangeRunDone || run.Attempts != 2 || f.merger.merges != 1 || f.held() {
		t.Fatalf("run = %+v, merges %d, held %v", run, f.merger.merges, f.held())
	}
	if last := f.store.applications[len(f.store.applications)-1]; last.Actor != "human" || last.Path != "db/004_boom.sql" {
		t.Fatalf("retried application = %+v", last)
	}
	if got := f.auditActions(); slices.Index(got, LadoAuditDBChangeRequested) < 0 {
		t.Fatalf("audit = %v", got)
	}
}

func TestPresenceIsAskedFirstForTheRun(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	f.presence.asked = nil
	if err := os.WriteFile(f.flagPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick()
	if _, err := f.apply(domain.LadoDBChangePreMerge, "db/004_boom.sql"); err != nil {
		t.Fatal(err)
	}
	want := domain.LadoPresenceDBChangeApply + " " + domain.LadoDBChangeApplySubject(testSession, f.head, domain.LadoDBChangePreMerge)
	if !slices.Equal(f.presence.asked, []string{want}) {
		t.Fatalf("asked = %v", f.presence.asked)
	}
	// A run that is queued already cannot be queued again.
	if _, err := f.apply(domain.LadoDBChangePreMerge, "db/004_boom.sql"); err == nil {
		t.Fatal("a queued run was queued again")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_RUN_STATE" {
		t.Fatalf("code = %s", code)
	}
}

func TestApplyRequestsThatLADORefuses(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	ctx := context.Background()
	if _, err := f.apply(domain.LadoDBChangePreMerge, "db/002_add.sql", "db/004_boom.sql"); err == nil {
		t.Fatal("an open PR without an approval run was queued")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_RUN_STATE" {
		t.Fatalf("code = %s", code)
	}
	if _, err := f.apply(domain.LadoDBChangeAfterDeploy, "db/003_drop.sql"); err == nil {
		t.Fatal("after-deploy SQL of an open PR was queued")
	}
	if _, err := f.s.ApplyDBChanges(ctx, domain.LadoDBChangeApplyRequest{SessionID: testSession, PRURL: testPRURL, HeadSHA: "nope", Phase: "later"}, f.presence.fn); err == nil {
		t.Fatal("an invalid request was queued")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_INVALID" {
		t.Fatalf("code = %s", code)
	}
	if _, err := f.s.ApplyDBChanges(ctx, domain.LadoDBChangeApplyRequest{SessionID: "other", PRURL: testPRURL, HeadSHA: f.head, Phase: domain.LadoDBChangePreMerge}, f.presence.fn); err == nil {
		t.Fatal("another execution's request was queued")
	} else if code, _ := apiCode(t, err); code != "LADO_EXECUTION_NOT_FOUND" {
		t.Fatalf("code = %s", code)
	}
	if len(f.store.runs) != 0 {
		t.Fatalf("runs = %+v", f.store.runs)
	}
}

func TestRefusedMergeIsTriedAgain(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	refused := errors.New("pull request preconditions failed: not_mergeable")
	f.merger.mergeErrs = []error{refused, refused}
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunMerging {
		t.Fatalf("run = %+v", run)
	}
	f.advance(15 * time.Second)
	f.tick()
	f.advance(15 * time.Second)
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone || f.merger.merges != 3 {
		t.Fatalf("run = %+v, merges %d", run, f.merger.merges)
	}
	if f.applied() != "ALTER TABLE a ADD b int;\n-- boom\nALTER TABLE a ADD c int;\n" || len(f.store.notices) != 0 {
		t.Fatalf("applied %q, notices %+v", f.applied(), f.store.notices)
	}
}

func TestRefusedMergeStopsAfterTheRetryWindowOrABlocker(t *testing.T) {
	refused := errors.New("refused")
	t.Run("window", func(t *testing.T) {
		f := newJobFixture(t, defaultHeadFiles())
		f.merger.mergeErrs = []error{refused, refused}
		f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
		f.tick()
		f.advance(mergeRetryFor + time.Second)
		f.tick()
		if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunMergeFailed || !strings.Contains(run.Error, "refused") {
			t.Fatalf("run = %+v", run)
		}
		if got := f.noticeKeys(); !slices.Equal(got, []string{"db_merge_failed"}) {
			t.Fatalf("notices = %v", got)
		}
		// A retry merges without running the SQL again.
		if _, err := f.apply(domain.LadoDBChangePreMerge); err != nil {
			t.Fatal(err)
		}
		f.tick()
		if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone || run.Attempts != 1 {
			t.Fatalf("run = %+v", run)
		}
		if strings.Count(f.applied(), "ALTER TABLE a ADD b int;") != 1 {
			t.Fatalf("applied = %q", f.applied())
		}
	})
	t.Run("blocker", func(t *testing.T) {
		f := newJobFixture(t, defaultHeadFiles())
		f.merger.mergeErrs = []error{refused, refused}
		f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
		f.tick() // the preflight passes, the files apply, the merge is refused while nothing blocks
		if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunMerging {
			t.Fatalf("run = %+v", run)
		}
		// Comments arrive after the status was posted: LADO stops.
		f.merger.readiness = domain.LadoMergeReadiness{Blockers: []string{domain.MergeBlockerUnresolvedComments}}
		f.tick()
		if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunMergeFailed || !strings.Contains(run.Error, domain.MergeBlockerUnresolvedComments) {
			t.Fatalf("run = %+v", run)
		}
	})
	t.Run("merged by a person first", func(t *testing.T) {
		f := newJobFixture(t, defaultHeadFiles())
		f.merger.mergeErrs = []error{refused, refused}
		f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
		f.tick()
		f.merger.readiness = domain.LadoMergeReadiness{Merged: true, Blockers: []string{domain.MergeBlockerMerged}}
		f.tick()
		if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone || len(f.store.notices) != 0 {
			t.Fatalf("run = %+v, notices %+v", run, f.store.notices)
		}
	})
}

func TestRunsWaitForTheMergeOrder(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	first := domain.LadoExecutionPR{URL: "https://github.com/o/back/pull/1", Number: 1, HeadSHA: strings.Repeat("a", 40), TargetBranch: "main"}
	f.exec.PRRepos = []string{"back", "app"}
	f.exec.PRs = []domain.LadoExecutionTrackedPR{{PR: first, Repo: "back", Position: 0}, {PR: f.exec.PR, Repo: "app", Position: 1}}
	f.store.executions[testSession] = f.exec
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunQueued || f.applied() != "" || f.merger.reads != 0 {
		t.Fatalf("run = %+v, applied %q, reads %d", run, f.applied(), f.merger.reads)
	}
	f.exec.PRs[0].PR.Merged = true
	f.store.executions[testSession] = f.exec
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone {
		t.Fatalf("run = %+v", run)
	}
}

func TestApprovalsWithoutPendingSQLQueueNothing(t *testing.T) {
	f := newJobFixture(t, map[string]string{"db/003_drop.sql": "-- lado:after-deploy\nDROP TABLE old;\n"})
	revoked := f.approval
	revoked.RevokedAt = t0
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval, revoked})
	if len(f.store.runs) != 0 {
		t.Fatalf("runs = %+v", f.store.runs)
	}
}

func TestLeftoversOfAStoppedDaemon(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	run := f.run(domain.LadoDBChangeRunPreMerge)
	f.store.runs[0].State = domain.LadoDBChangeRunApplying
	f.store.applications = append(f.store.applications, domain.LadoDBChangeApplication{
		ID: "left", ProjectID: testProject, Path: "db/002_add.sql", BlobSHA: f.repo.blob(f.head, "db/002_add.sql"),
		Phase: domain.LadoDBChangePreMerge, Result: domain.LadoDBChangeResultRunning, RunID: run.ID, StartedAt: t0,
	})
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunFailed || !strings.Contains(run.Error, "interrupted") {
		t.Fatalf("run = %+v", run)
	}
	if left := f.store.applications[len(f.store.applications)-1]; left.Result != domain.LadoDBChangeResultInterrupted {
		t.Fatalf("leftover = %+v", left)
	}
	// An interrupted file is not applied: a retry runs it again.
	if _, err := f.apply(domain.LadoDBChangePreMerge, "db/002_add.sql", "db/004_boom.sql"); err != nil {
		t.Fatal(err)
	}
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone || !strings.Contains(f.applied(), "ADD b int") {
		t.Fatalf("run = %+v, applied %q", run, f.applied())
	}
}

func TestStartRunsTheLoopUntilItsContextEnds(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	f.s.deps.Interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	f.s.Start(ctx)
	f.s.IntegrationApproved(ctx, f.exec, []domain.LadoIntegrationApproval{f.approval})
	deadline := time.Now().Add(20 * time.Second)
	for f.run(domain.LadoDBChangeRunPreMerge).State != domain.LadoDBChangeRunDone {
		if time.Now().After(deadline) {
			t.Fatalf("run = %+v", f.run(domain.LadoDBChangeRunPreMerge))
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	f.s.Wait()
}

func TestSettingsTurnedOffStopARun(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	settings := f.store.settings[settingsKey(testProject, "")]
	settings.Enabled = false
	f.store.settings[settingsKey(testProject, "")] = settings
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunAwaitingHuman || f.applied() != "" {
		t.Fatalf("run = %+v, applied %q", run, f.applied())
	}
}
