package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladodbchange"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// dbChangeSCM is the host of a database change task's merge request: it
// answers the merge request at head with passing CI, its review threads,
// and merges, refusing the first refusals merges as not mergeable.
type dbChangeSCM struct {
	mu         sync.Mutex
	head       string
	unresolved bool
	// reviewRequired is GitHub's mergeStateStatus BLOCKED with
	// reviewDecision REVIEW_REQUIRED: a required approval is missing.
	reviewRequired bool
	refusals       int
	merges         int
	merged         bool
}

func (s *dbChangeSCM) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := ""
	if s.merged {
		state = string(domain.PRStateMerged)
	}
	mergeability := ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true}
	if s.reviewRequired {
		mergeability = ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked), Blockers: []string{"review_required"}}
	}
	return []ports.SCMObservation{{
		Fetched:      true,
		PR:           ports.SCMPRObservation{URL: refs[0].URL, Number: refs[0].Number, HeadSHA: s.head, TargetBranch: "main", State: state, Merged: s.merged},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: s.head},
		Mergeability: mergeability,
	}}, nil
}

func (s *dbChangeSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reviewRequired {
		return ports.SCMReviewObservation{Decision: string(domain.ReviewRequired)}, nil
	}
	if !s.unresolved {
		return ports.SCMReviewObservation{}, nil
	}
	return ports.SCMReviewObservation{Threads: []ports.SCMReviewThreadObservation{{
		ID: "thread-1", Path: "db/002_add.sql", Comments: []ports.SCMReviewCommentObservation{{ID: "comment-1"}},
	}}}, nil
}

func (s *dbChangeSCM) MergePullRequest(context.Context, ports.SCMMergeRequest) (ports.SCMMergeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refusals > 0 {
		s.refusals--
		return ports.SCMMergeResult{}, errors.New("the host refused the merge: not mergeable (blocked)")
	}
	s.merges++
	s.merged = true
	return ports.SCMMergeResult{MergeCommitSHA: strings.Repeat("ef", 20)}, nil
}

func (s *dbChangeSCM) mergeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.merges
}

// dbChangeFixture is a LADO execution whose merge request adds SQL, over
// LADO's real store, verification loop and database change service, AO's
// real PR actions on a fake host, and a real repository with an origin.
type dbChangeFixture struct {
	ctx      context.Context
	store    *sqlite.Store
	db       *ladodbchange.Service
	actions  *prsvc.ActionService
	scm      *dbChangeSCM
	worker   domain.SessionID
	url      string
	repoDir  string
	baseTip  string
	head     string
	log      string
	flag     string
	nonce    int
	at       time.Time
	approval domain.LadoIntegrationApproval
}

// newDBChangeFixture seeds the execution: main has db/001_init.sql, the
// merge request's head adds files, the project's settings are on when on
// is true (with the command that appends each file to f.log, and fails a
// file that says FAIL while f.flag exists), and the head's record is
// approved.
func newDBChangeFixture(t *testing.T, on bool, files map[string]string) *dbChangeFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	useLadoStoredTokens(t, nil)
	cfg := config.Config{DataDir: t.TempDir(), StateDir: t.TempDir()}
	store := sqlitetest.MustOpen(t)
	f := &dbChangeFixture{ctx: ctx, store: store, at: time.Now().UTC().Truncate(time.Second)}
	f.worker = seedLadoVerifyExecution(t, store, cfg, f.at)

	f.repoDir, _ = ladoGateRepo(t)
	commit := func(message string) {
		ladoGit(t, f.repoDir, "add", "-A")
		ladoGit(t, f.repoDir, "-c", "user.name=LADO", "-c", "user.email=lado@example.com", "-c", "commit.gpgsign=false",
			"commit", "-q", "--no-verify", "-m", message)
	}
	writeFile := func(name, content string) {
		path := filepath.Join(f.repoDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("db/001_init.sql", "CREATE TABLE users (id int);\n")
	commit("Start the schema")
	ladoGit(t, f.repoDir, "push", "-q", "origin", "HEAD:refs/heads/main")
	ladoGit(t, f.repoDir, "remote", "set-head", "origin", "main")
	f.baseTip = ladoGit(t, f.repoDir, "rev-parse", "HEAD")
	ladoGit(t, f.repoDir, "checkout", "-q", "-b", "feature")
	for name, content := range files {
		writeFile(name, content)
	}
	commit("Add the columns")
	f.head = ladoGit(t, f.repoDir, "rev-parse", "HEAD")
	ladoGit(t, f.repoDir, "checkout", "-q", "main")
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: f.repoDir, RepoOriginURL: "https://gitlab.com/acme/demo.git", RegisteredAt: f.at}); err != nil {
		t.Fatalf("point the project at its repository: %v", err)
	}
	f.url = "https://gitlab.com/acme/demo/-/merge_requests/7"
	if err := store.WriteSCMObservation(ctx, domain.PullRequest{
		URL: f.url, SessionID: f.worker, Number: 7, HeadSHA: f.head, TargetBranch: "main", Title: "열을 더한다", UpdatedAt: f.at,
		Provider: "gitlab", Host: "gitlab.com", Repo: "acme/demo", Mergeability: domain.MergeMergeable,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatalf("write the merge request: %v", err)
	}

	log := slog.New(slog.DiscardHandler)
	lado := wireLadoWork(ctx, cfg, store, &recordingProjectCloner{}, ladoWorkDeps{}, log)
	f.scm = &dbChangeSCM{head: f.head}
	f.actions = prsvc.NewActionService(prsvc.ActionDeps{Store: store, Reader: f.scm, Merger: f.scm})
	installLadoMergeGate(f.actions, nil, lado.verify)
	f.db = wireLadoDBChanges(cfg, store, lado.blobs, lado.verify, f.actions, f.scm, log)

	scratch := t.TempDir()
	f.log, f.flag = filepath.Join(scratch, "applied.log"), filepath.Join(scratch, "fail")
	if on {
		command := fmt.Sprintf("if grep -q FAIL {file} && test -f '%s'; then echo 'ERROR: relation exists' >&2; exit 3; fi; cat {file} >> '%s'", f.flag, f.log)
		if _, err := f.db.UpdateDBChangeSettings(ctx, "mer", domain.LadoDBChangeSettingsUpdate{
			Enabled: true, Paths: []string{"db/*.sql"}, Command: command, TimeoutMinutes: 1,
		}, f.presence(t)); err != nil {
			t.Fatalf("turn the database changes on: %v", err)
		}
	}

	var items []domain.LadoVerifyItem
	for _, kind := range domain.LadoVerifyKinds() {
		item, err := domain.NewLadoVerifyItem(kind, domain.LadoVerifyPass, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	item, ok, err := f.db.Item(ctx, domain.LadoDBChangeInput{
		ProjectID: "mer", Dir: f.repoDir, Base: f.baseTip, Head: f.head,
		Plan: domain.LadoPlan{ChangeKinds: []string{domain.LadoChangeDBSchema}},
	})
	switch {
	case err != nil:
		t.Fatalf("Item: %v", err)
	case ok != on:
		t.Fatalf("Item ok = %v with the settings on = %v", ok, on)
	case ok:
		items = append(items, item)
	}
	record := domain.LadoVerification{ExecutionSessionID: f.worker, HeadSHA: f.head, PRURL: f.url, Items: items, ComputedAt: f.at}
	sum, err := record.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertLadoVerification(ctx, record); err != nil {
		t.Fatalf("UpsertLadoVerification: %v", err)
	}
	proof := domain.LadoPresenceProof{Nonce: "BBBBBBBBBBBBBBBBBBBBBB", Action: domain.LadoPresenceIntegrationApprove, SubjectID: string(f.worker) + "@" + f.head, ExpiresAt: f.at.Add(time.Minute), UsedAt: f.at}
	if err := store.ApproveLadoIntegrations(ctx, []domain.LadoIntegrationApproval{{
		ID: "lado-integration-1", ExecutionSessionID: f.worker, PRURL: f.url, HeadSHA: f.head, VerificationSHA256: sum,
		Actor: "human", ApprovedAt: f.at, StatusState: domain.LadoIntegrationStatusNotApplicable, BaseTipSHA: f.baseTip,
	}}, proof, domain.LadoAuditEvent{At: f.at, Actor: "human", Action: "integration.approved", SubjectKind: "execution", SubjectID: string(f.worker), Payload: []byte("{}")}); err != nil {
		t.Fatalf("ApproveLadoIntegrations: %v", err)
	}
	approvals, err := store.ListLadoIntegrationApprovals(ctx, f.worker, f.url, f.head)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals = %+v, %v", approvals, err)
	}
	f.approval = approvals[0]
	return f
}

// presence answers each confirmation with a new proof, as the desktop app
// signs one.
func (f *dbChangeFixture) presence(t *testing.T) func(action, subject string) (domain.LadoPresenceProof, error) {
	return func(action, subject string) (domain.LadoPresenceProof, error) {
		f.nonce++
		return domain.LadoPresenceProof{
			Nonce: fmt.Sprintf("N%021d", f.nonce), Action: action, SubjectID: subject,
			ExpiresAt: f.at.Add(time.Hour), UsedAt: f.at,
		}, nil
	}
}

// approved tells the database change service of the approval, as the
// verification loop does right after recording it, and runs its loop once.
func (f *dbChangeFixture) approved(t *testing.T) {
	t.Helper()
	exec, ok, err := f.store.LadoExecution(f.ctx, f.worker)
	if err != nil || !ok {
		t.Fatalf("LadoExecution = %v, %v", ok, err)
	}
	f.db.IntegrationApproved(f.ctx, exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick(t)
}

func (f *dbChangeFixture) tick(t *testing.T) {
	t.Helper()
	if err := f.db.Tick(f.ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
}

// run is the execution's one pre-merge run.
func (f *dbChangeFixture) run(t *testing.T) domain.LadoDBChangeRun {
	t.Helper()
	runs, err := f.store.ListLadoDBChangeRunsForExecution(f.ctx, f.worker)
	if err != nil || len(runs) != 1 || runs[0].Kind != domain.LadoDBChangeRunPreMerge {
		t.Fatalf("runs = %+v, %v; want one pre-merge run", runs, err)
	}
	return runs[0]
}

// applied is what the command appended, "" before any file ran.
func (f *dbChangeFixture) applied(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// merge is AO's merge route at the approved head.
func (f *dbChangeFixture) merge() error {
	_, err := f.actions.Merge(f.ctx, prsvc.MergeRequest{PRID: "7", PRURL: f.url, ExpectedHeadSHA: f.head})
	return err
}

// Approving a head with pre-merge SQL applies it with the real shell command
// in path order, leaves the baseline's file alone, and only then merges
// through AO's merge route; until then that route refuses the head. A merge
// the host refuses twice as not mergeable goes through on a later pass.
func TestLadoDBChangesApplyThenMergeOnApproval(t *testing.T) {
	f := newDBChangeFixture(t, true, map[string]string{
		"db/003_index.sql": "CREATE INDEX users_email ON users (email);\n",
		"db/002_add.sql":   "ALTER TABLE users ADD email text;\n",
	})
	var api *apierr.Error
	if err := f.merge(); !errors.As(err, &api) || api.Code != "LADO_DB_CHANGE_PENDING" || f.scm.mergeCount() != 0 {
		t.Fatalf("AO's merge before the SQL = %v after %d merges, want LADO_DB_CHANGE_PENDING before any", err, f.scm.mergeCount())
	}

	f.scm.refusals = 2
	f.approved(t)
	want := "ALTER TABLE users ADD email text;\nCREATE INDEX users_email ON users (email);\n"
	if got := f.applied(t); got != want {
		t.Fatalf("applied = %q, want the head's two files in path order without the baseline's", got)
	}
	if run := f.run(t); run.State != domain.LadoDBChangeRunMerging || f.scm.mergeCount() != 0 {
		t.Fatalf("after a refused merge the run is %s with %d merges, want merging and none", run.State, f.scm.mergeCount())
	}
	f.tick(t)
	f.tick(t)
	if run := f.run(t); run.State != domain.LadoDBChangeRunDone || f.scm.mergeCount() != 1 {
		t.Fatalf("after the refusals the run is %s with %d merges, want done and one", run.State, f.scm.mergeCount())
	}
	if got := f.applied(t); got != want {
		t.Fatalf("applied after the merge = %q, want each file once", got)
	}
	rows, err := f.store.ListLadoDBChangeApplications(f.ctx, "mer", "", []string{"db/001_init.sql", "db/002_add.sql", "db/003_index.sql"})
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]domain.LadoDBChangeResult{}
	for _, row := range rows {
		results[row.Path] = row.Result
	}
	if results["db/001_init.sql"] != domain.LadoDBChangeResultBaseline || results["db/002_add.sql"] != domain.LadoDBChangeResultApplied ||
		results["db/003_index.sql"] != domain.LadoDBChangeResultApplied {
		t.Fatalf("applications = %+v, want the baseline and two applied files", results)
	}
}

// A file that fails stops the run before the merge and tells the
// orchestrator; the developer's retry skips the file that applied.
func TestLadoDBChangesFailedFileDoesNotMergeAndRetrySkipsApplied(t *testing.T) {
	f := newDBChangeFixture(t, true, map[string]string{
		"db/002_add.sql":  "ALTER TABLE users ADD email text;\n",
		"db/003_fail.sql": "-- FAIL\nCREATE TABLE users (id int);\n",
	})
	if err := os.WriteFile(f.flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.approved(t)
	run := f.run(t)
	if run.State != domain.LadoDBChangeRunFailed || f.scm.mergeCount() != 0 || !strings.Contains(run.Error, "db/003_fail.sql") {
		t.Fatalf("after a failed file the run is %s (%q) with %d merges, want failed at db/003_fail.sql and none", run.State, run.Error, f.scm.mergeCount())
	}
	if !strings.Contains(run.Error, "exit code 3") {
		t.Errorf("the run's error %q does not say how the command failed", run.Error)
	}
	if got := f.applied(t); got != "ALTER TABLE users ADD email text;\n" {
		t.Fatalf("applied = %q, want only the first file", got)
	}
	notices, err := f.store.ListLadoOrchestratorNotices(f.ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(notices), "db_failed:") {
		t.Errorf("notices = %+v, want one of the failed SQL", notices)
	}
	if err := f.merge(); err == nil || f.scm.mergeCount() != 0 {
		t.Fatalf("AO's merge after the failure = %v after %d merges, want a refusal", err, f.scm.mergeCount())
	}

	if err := os.Remove(f.flag); err != nil {
		t.Fatal(err)
	}
	view, err := f.db.ExecutionDBChanges(f.ctx, f.worker)
	if err != nil || len(view.PRs) != 1 {
		t.Fatalf("ExecutionDBChanges = %+v, %v", view, err)
	}
	var confirmed []domain.LadoDBChangeFileRef
	for _, file := range view.PRs[0].Files {
		confirmed = append(confirmed, file.LadoDBChangeFileRef)
	}
	if _, err := f.db.ApplyDBChanges(f.ctx, domain.LadoDBChangeApplyRequest{
		SessionID: f.worker, PRURL: f.url, HeadSHA: f.head, Phase: domain.LadoDBChangePreMerge, Files: confirmed,
	}, f.presence(t)); err != nil {
		t.Fatalf("ApplyDBChanges: %v", err)
	}
	f.tick(t)
	if run := f.run(t); run.State != domain.LadoDBChangeRunDone || f.scm.mergeCount() != 1 {
		t.Fatalf("after the retry the run is %s with %d merges, want done and one", run.State, f.scm.mergeCount())
	}
	want := "ALTER TABLE users ADD email text;\n-- FAIL\nCREATE TABLE users (id int);\n"
	if got := f.applied(t); got != want {
		t.Fatalf("applied after the retry = %q, want the first file once and then the second", got)
	}
}

// A merge request with an unresolved review comment could not merge, so
// LADO runs no SQL: the run waits for the developer and the orchestrator
// hears why.
func TestLadoDBChangesUnresolvedCommentStopsBeforeAnySQL(t *testing.T) {
	f := newDBChangeFixture(t, true, map[string]string{"db/002_add.sql": "ALTER TABLE users ADD email text;\n"})
	f.scm.unresolved = true
	f.approved(t)
	run := f.run(t)
	if run.State != domain.LadoDBChangeRunAwaitingHuman || !strings.Contains(run.Error, domain.MergeBlockerUnresolvedComments) {
		t.Fatalf("run = %s (%q), want awaiting_human for the unresolved comments", run.State, run.Error)
	}
	if got := f.applied(t); got != "" || f.scm.mergeCount() != 0 {
		t.Fatalf("applied %q with %d merges, want no SQL and no merge", got, f.scm.mergeCount())
	}
	notices, err := f.store.ListLadoOrchestratorNotices(f.ctx, "mer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(notices), "db_preflight:") {
		t.Errorf("notices = %+v, want one of the blocked preflight", notices)
	}
}

// A PR that still lacks an approval the host requires is blocked
// (not_mergeable), which LADO's missing status could also cause; the review
// decision tells them apart, so LADO runs no SQL the merge could not follow.
func TestLadoDBChangesRequiredReviewStopsBeforeAnySQL(t *testing.T) {
	f := newDBChangeFixture(t, true, map[string]string{"db/002_add.sql": "ALTER TABLE users ADD email text;\n"})
	f.scm.mu.Lock()
	f.scm.reviewRequired = true
	f.scm.mu.Unlock()
	f.approved(t)
	run := f.run(t)
	if run.State != domain.LadoDBChangeRunAwaitingHuman || !strings.Contains(run.Error, domain.LadoDBChangeStopperReviewRequired) {
		t.Fatalf("run = %s (%q), want awaiting_human for the required review", run.State, run.Error)
	}
	if got := f.applied(t); got != "" || f.scm.mergeCount() != 0 {
		t.Fatalf("applied %q with %d merges, want no SQL and no merge", got, f.scm.mergeCount())
	}
}

// With the settings off the service changes nothing: the head's record has
// no db_changes item, the approval queues no run, and AO's merge route
// merges the approved head at once.
func TestLadoDBChangesOffKeepsTheMergeAsBefore(t *testing.T) {
	f := newDBChangeFixture(t, false, map[string]string{"db/002_add.sql": "ALTER TABLE users ADD email text;\n"})
	record, ok, err := f.store.GetLadoVerification(f.ctx, f.worker, f.head)
	if err != nil || !ok {
		t.Fatalf("GetLadoVerification = %v, %v", ok, err)
	}
	if _, has, err := record.DBChanges(); has || err != nil || len(record.Items) != len(domain.LadoVerifyKinds()) {
		t.Fatalf("record has %d items, db_changes %v (%v); want the eight items alone", len(record.Items), has, err)
	}
	f.approved(t)
	runs, err := f.store.ListLadoDBChangeRunsForExecution(f.ctx, f.worker)
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs = %+v, %v; want none", runs, err)
	}
	if err := f.merge(); err != nil || f.scm.mergeCount() != 1 || f.applied(t) != "" {
		t.Fatalf("AO's merge = %v after %d merges, want one merge and no SQL", err, f.scm.mergeCount())
	}
	summaries, err := f.db.ProjectDBChanges(f.ctx, "mer")
	if err != nil || len(summaries) != 0 {
		t.Fatalf("ProjectDBChanges = %+v, %v; want none", summaries, err)
	}
	if data, err := json.Marshal(record); err != nil || strings.Contains(string(data), "db_changes") {
		t.Fatalf("the record names db_changes: %s (%v)", data, err)
	}
}

// Without AO's PR actions (no usable SCM provider) the merge adapter refuses
// with an error, so a run that would merge stops instead of hanging.
func TestLadoDBChangeMergerWithoutPRActions(t *testing.T) {
	ctx := context.Background()
	merger := ladoDBChangeMerger{}
	if _, err := merger.LadoMergeReadiness(ctx, "https://gitlab.com/acme/demo/-/merge_requests/7", 7, strings.Repeat("ab", 20)); !errors.Is(err, errLadoDBChangeNoPRActions) {
		t.Errorf("LadoMergeReadiness = %v, want errLadoDBChangeNoPRActions", err)
	}
	if err := merger.MergeLadoPR(ctx, "https://gitlab.com/acme/demo/-/merge_requests/7", 7, strings.Repeat("ab", 20)); !errors.Is(err, errLadoDBChangeNoPRActions) {
		t.Errorf("MergeLadoPR = %v, want errLadoDBChangeNoPRActions", err)
	}
	if heads := ladoDBChangeLiveHeads(nil); heads != nil {
		t.Errorf("live heads without a reader = %v, want nil", heads)
	}
}
