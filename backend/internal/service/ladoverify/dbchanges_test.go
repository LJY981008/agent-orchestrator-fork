package ladoverify

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// fakeDBChanges is the database change service as the loop asks it: it
// answers what the test sets and records what it was asked.
type fakeDBChanges struct {
	mu sync.Mutex
	// off answers Item with ok false; itemErr fails it; item is the item
	// it answers otherwise (dbChangesTestItem when zero).
	off     bool
	itemErr error
	item    domain.LadoVerifyItem
	inputs  []domain.LadoDBChangeInput
	// pending answers PendingPreMerge, pendingErr fails it.
	pending    []domain.LadoDBChangeFileRef
	pendingErr error
	pendingAsk []domain.LadoDBChangesDetails
	// hold answers HoldIntegrationStatus, holdErr fails it.
	hold     bool
	holdErr  error
	holdAsks []domain.LadoIntegrationApproval
	// approved holds the approvals IntegrationApproved was told of.
	approved [][]domain.LadoIntegrationApproval
	// settleErr fails Settled; settled holds what it was told.
	settleErr error
	settled   []fakeSettled
}

type fakeSettled struct {
	exec   domain.LadoExecution
	record *domain.LadoVerification
	in     *domain.LadoDBChangeInput
}

var dbChangesTestFile = domain.LadoDBChangeFileRef{Path: "db/001_add.sql", BlobSHA: strings.Repeat("ab", 20), Phase: domain.LadoDBChangePreMerge}

func dbChangesTestItem(t *testing.T) domain.LadoVerifyItem {
	t.Helper()
	item, err := domain.NewLadoVerifyItem(domain.LadoVerifyDBChanges, domain.LadoVerifyPass, "",
		domain.LadoDBChangesDetails{Files: []domain.LadoDBChangeFileRef{dbChangesTestFile}, Planned: true})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func (f *fakeDBChanges) Item(_ context.Context, in domain.LadoDBChangeInput) (domain.LadoVerifyItem, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	switch {
	case f.itemErr != nil:
		return domain.LadoVerifyItem{}, false, f.itemErr
	case f.off:
		return domain.LadoVerifyItem{}, false, nil
	}
	return f.item, true, nil
}

func (f *fakeDBChanges) PendingPreMerge(_ context.Context, _ domain.ProjectID, details domain.LadoDBChangesDetails) ([]domain.LadoDBChangeFileRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingAsk = append(f.pendingAsk, details)
	return f.pending, f.pendingErr
}

func (f *fakeDBChanges) HoldIntegrationStatus(_ context.Context, approval domain.LadoIntegrationApproval, record domain.LadoVerification) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, has, _ := record.DBChanges(); !has {
		return false, errors.New("asked about a record without db_changes")
	}
	f.holdAsks = append(f.holdAsks, approval)
	return f.hold, f.holdErr
}

func (f *fakeDBChanges) IntegrationApproved(_ context.Context, _ domain.LadoExecution, approvals []domain.LadoIntegrationApproval) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved = append(f.approved, approvals)
}

func (f *fakeDBChanges) Settled(_ context.Context, exec domain.LadoExecution, record *domain.LadoVerification, in *domain.LadoDBChangeInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settled = append(f.settled, fakeSettled{exec: exec, record: record, in: in})
	return f.settleErr
}

func (f *fakeDBChanges) set(change func(*fakeDBChanges)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeDBChanges) read(read func(*fakeDBChanges)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	read(f)
}

func newFakeDBChanges(t *testing.T) *fakeDBChanges {
	return &fakeDBChanges{item: dbChangesTestItem(t)}
}

// #97: the db_changes item follows the eight items only in a repository
// whose settings are on; without the service or with the settings off the
// record and its hash stay as they were.
func TestDBChangesItemOnlyWhenTheSettingsAreOn(t *testing.T) {
	f := newVerifyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	f.tick()
	before := f.record(exec.SessionID, verifyHead1)
	if len(before.Items) != len(domain.LadoVerifyKinds()) {
		t.Fatalf("record without the service has %d items, want %d", len(before.Items), len(domain.LadoVerifyKinds()))
	}
	sum := verifySHA(t, before)

	db := newFakeDBChanges(t)
	db.set(func(f *fakeDBChanges) { f.off = true })
	f.svc.SetDBChanges(db)
	f.clock.advance(time.Minute)
	f.tick()
	if off := f.record(exec.SessionID, verifyHead1); len(off.Items) != len(domain.LadoVerifyKinds()) || verifySHA(t, off) != sum {
		t.Fatalf("record with the settings off = %d items, hash %s; want the same %d items and hash %s", len(off.Items), verifySHA(t, off), len(before.Items), sum)
	}
	var in domain.LadoDBChangeInput
	db.read(func(f *fakeDBChanges) { in = f.inputs[len(f.inputs)-1] })
	plan := f.reads.plans[exec.PlanRevisionID]
	if in.ProjectID != "p1" || in.Repo != "" || in.Dir != f.reads.project("p1").Path || in.Base != verifyBase || in.Head != verifyHead1 ||
		!slices.Equal(in.Plan.AffectedPaths, plan.AffectedPaths) {
		t.Errorf("input = %+v, want project p1's root at the merge base and head with the plan", in)
	}

	db.set(func(f *fakeDBChanges) { f.off = false })
	f.clock.advance(time.Minute)
	f.tick()
	on := f.record(exec.SessionID, verifyHead1)
	if len(on.Items) != len(domain.LadoVerifyKinds())+1 {
		t.Fatalf("record with the settings on has %d items, want %d", len(on.Items), len(domain.LadoVerifyKinds())+1)
	}
	if last := on.Items[len(on.Items)-1]; last.Kind != domain.LadoVerifyDBChanges || on.Items[len(on.Items)-2].Kind != domain.LadoVerifyEvidence {
		t.Errorf("items end with %s, %s; want evidence, db_changes", on.Items[len(on.Items)-2].Kind, last.Kind)
	}
	if details, has, err := on.DBChanges(); err != nil || !has || len(details.Files) != 1 || details.Files[0] != dbChangesTestFile {
		t.Errorf("db_changes details = %+v, %v, %v; want the head's file", details, has, err)
	}
	if verifySHA(t, on) == sum {
		t.Error("the db_changes item did not change the record's hash")
	}

	// An item the service cannot compute waits.
	db.set(func(f *fakeDBChanges) { f.itemErr = errors.New("settings unreadable") })
	f.clock.advance(time.Minute)
	f.tick()
	waiting := verifyItemOf(t, f.record(exec.SessionID, verifyHead1), domain.LadoVerifyDBChanges)
	if waiting.Outcome != domain.LadoVerifyWait || waiting.Code != domain.LadoValidatingVerifying {
		t.Errorf("db_changes item after a failed read = %s %s, want wait verifying", waiting.Outcome, waiting.Code)
	}

	// In a workspace the repository is the child's folder.
	ws, dir := f.addWorkspaceExecution("2", verifyHead2)
	db.set(func(f *fakeDBChanges) { f.itemErr = nil })
	f.tick()
	f.record(ws.SessionID, verifyHead2)
	db.read(func(f *fakeDBChanges) {
		i := slices.IndexFunc(f.inputs, func(in domain.LadoDBChangeInput) bool { return in.Head == verifyHead2 })
		if i < 0 || f.inputs[i].Repo != "api" || f.inputs[i].Dir != dir || f.inputs[i].ProjectID != "ws" {
			t.Errorf("workspace inputs = %+v, want repository api in %s", f.inputs, dir)
		}
	})
}

// #97: an approved head whose pre-merge SQL is not applied yet gets no
// lado/integration status and no attempt counts, at the approval and in
// the retries, until the hold is released and the status posted again.
func TestDBChangesHoldTheIntegrationStatus(t *testing.T) {
	f := newApproveFixture(t)
	db := newFakeDBChanges(t)
	db.set(func(f *fakeDBChanges) { f.hold = true })
	f.svc.SetDBChanges(db)
	exec := f.add("1", verifyHead1)
	preview := f.preview(exec)
	result, err := f.approve(exec, preview.SHA256, preview.Confirm, "nonce-held")
	if err != nil {
		t.Fatalf("approval = %v", err)
	}
	a := result.Approval
	if a == nil || a.StatusState != domain.LadoIntegrationStatusPending || a.StatusAttempts != 0 {
		t.Fatalf("held approval = %+v, want pending without an attempt", a)
	}
	if stored := f.store.approval(a.ID); stored.StatusState != domain.LadoIntegrationStatusPending || stored.StatusAttempts != 0 {
		t.Errorf("stored approval = %s after %d attempts, want pending after none", stored.StatusState, stored.StatusAttempts)
	}
	if posts := f.host.postsMade(); len(posts) != 0 {
		t.Errorf("posts = %+v, want none while held", posts)
	}
	db.read(func(f *fakeDBChanges) {
		if len(f.holdAsks) != 1 || f.holdAsks[0].ID != a.ID {
			t.Errorf("hold asked about %+v, want the approval", f.holdAsks)
		}
		if len(f.approved) != 1 || len(f.approved[0]) != 1 || f.approved[0][0].ID != a.ID {
			t.Errorf("IntegrationApproved told %+v, want the approval", f.approved)
		}
	})

	// The loop's retry posts nothing either.
	f.clock.advance(statusRetryAfter)
	f.tick()
	if posts, stored := f.host.postsMade(), f.store.approval(a.ID); len(posts) != 0 || stored.StatusAttempts != 0 {
		t.Errorf("after a retry: %d posts, %d attempts; want none", len(posts), stored.StatusAttempts)
	}

	// A hold that cannot be checked posts nothing.
	db.set(func(f *fakeDBChanges) { f.hold, f.holdErr = false, errors.New("store down") })
	f.clock.advance(statusRetryAfter)
	if err := f.svc.Tick(f.ctx); err == nil || !strings.Contains(err.Error(), "store down") {
		t.Errorf("Tick with a failing hold = %v, want its error", err)
	}
	if posts, stored := f.host.postsMade(), f.store.approval(a.ID); len(posts) != 0 || stored.StatusAttempts != 0 {
		t.Errorf("after a failing hold: %d posts, %d attempts; want none", len(posts), stored.StatusAttempts)
	}

	// Once applied, the service posts the status again.
	db.set(func(f *fakeDBChanges) { f.holdErr = nil })
	posted, err := f.svc.RepostIntegrationStatusOf(f.ctx, exec.SessionID, exec.PR.URL)
	if err != nil || posted.StatusState != domain.LadoIntegrationStatusPosted || posted.StatusAttempts != 1 {
		t.Fatalf("repost = %+v, %v; want posted after one attempt", posted, err)
	}
	if posts := f.host.postsMade(); len(posts) != 1 || posts[0].status.State != "success" {
		t.Errorf("posts = %+v, want the success", posts)
	}

	// A record without db_changes is never held.
	var asked int
	db.set(func(f *fakeDBChanges) { f.off, f.hold, asked = true, true, len(f.holdAsks) })
	plain := f.add("2", verifyHead3)
	p := f.preview(plain)
	result, err = f.approve(plain, p.SHA256, p.Confirm, "nonce-plain")
	if err != nil || result.Approval.StatusState != domain.LadoIntegrationStatusPosted {
		t.Fatalf("approval without db_changes = %+v, %v; want posted", result.Approval, err)
	}
	db.read(func(f *fakeDBChanges) {
		if len(f.holdAsks) != asked {
			t.Errorf("hold asked %d more times, want none about a head without db_changes", len(f.holdAsks)-asked)
		}
	})
}

// #97: the merge gate refuses an approved head with pre-merge SQL not
// applied last, after the approval; PreflightMerge asks all but that.
func TestMergeGateRefusesPendingDBChanges(t *testing.T) {
	f := newVerifyFixture(t)
	db := newFakeDBChanges(t)
	f.svc.SetDBChanges(db)
	exec := f.addExecution("1", verifyHead1)
	f.tick()
	pr := domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, Number: 1, HeadSHA: verifyHead1, TargetBranch: "main"}
	db.set(func(f *fakeDBChanges) { f.pending = []domain.LadoDBChangeFileRef{dbChangesTestFile} })

	// Without an approval both refuse as before, and the files are not
	// asked about.
	for _, err := range []error{f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), f.svc.PreflightMerge(f.ctx, pr, verifyHead1)} {
		requireAPIError(t, err, apierr.KindConflict, "LADO_INTEGRATION_APPROVAL_REQUIRED")
	}
	db.read(func(f *fakeDBChanges) {
		if len(f.pendingAsk) != 0 {
			t.Errorf("pending asked %d times before an approval", len(f.pendingAsk))
		}
	})

	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-1", ExecutionSessionID: exec.SessionID, PRURL: exec.PR.URL, HeadSHA: verifyHead1, TargetBranch: "main"})
	requireDetails(t, f.svc.AuthorizeMerge(f.ctx, pr, strings.ToUpper(verifyHead1)), apierr.KindConflict, "LADO_DB_CHANGE_PENDING",
		map[string]any{"prUrl": exec.PR.URL, "headSha": verifyHead1, "files": []domain.LadoDBChangeFileRef{dbChangesTestFile}})
	if err := f.svc.PreflightMerge(f.ctx, pr, verifyHead1); err != nil {
		t.Errorf("PreflightMerge of the approved head = %v, want nil", err)
	}
	db.read(func(f *fakeDBChanges) {
		if len(f.pendingAsk) != 1 || len(f.pendingAsk[0].Files) != 1 {
			t.Errorf("pending asked with %+v, want the record's details once", f.pendingAsk)
		}
	})

	db.set(func(f *fakeDBChanges) { f.pending, f.pendingErr = nil, errors.New("store down") })
	requireAPIError(t, f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1), apierr.KindInternal, "LADO_MERGE_GATE_FAILED")

	db.set(func(f *fakeDBChanges) { f.pendingErr = nil })
	if err := f.svc.AuthorizeMerge(f.ctx, pr, verifyHead1); err != nil {
		t.Errorf("AuthorizeMerge once applied = %v", err)
	}

	// A head whose record has no db_changes merges without asking.
	plain := f.addExecution("2", verifyHead2)
	db.set(func(f *fakeDBChanges) { f.off, f.pending = true, []domain.LadoDBChangeFileRef{dbChangesTestFile} })
	f.tick()
	f.store.addApproval(domain.LadoIntegrationApproval{ID: "approval-2", ExecutionSessionID: plain.SessionID, PRURL: plain.PR.URL, HeadSHA: verifyHead2, TargetBranch: "main"})
	plainPR := domain.PullRequest{URL: plain.PR.URL, SessionID: plain.SessionID, Number: 2, HeadSHA: verifyHead2, TargetBranch: "main"}
	if err := f.svc.AuthorizeMerge(f.ctx, plainPR, verifyHead2); err != nil {
		t.Errorf("AuthorizeMerge of a head without db_changes = %v", err)
	}
}

// #97: a merged PR goes to the database change service before LADO settles
// it; an error leaves it unsettled for the next pass. A closed PR does not.
func TestSettleTellsTheDBChangesFirst(t *testing.T) {
	f := newVerifyFixture(t)
	db := newFakeDBChanges(t)
	f.svc.SetDBChanges(db)
	recorded := f.addExecution("1", verifyHead1)
	f.tick()
	f.record(recorded.SessionID, verifyHead1)
	f.store.update(recorded.SessionID, func(exec *domain.LadoExecution) { exec.PR.Merged = true })
	unrecorded := f.addExecution("2", verifyHead2)
	f.store.update(unrecorded.SessionID, func(exec *domain.LadoExecution) { exec.PR.Merged = true })
	closed := f.addExecution("3", verifyHead3)
	f.store.update(closed.SessionID, func(exec *domain.LadoExecution) { exec.PR.Closed = true })

	db.set(func(f *fakeDBChanges) { f.settleErr = errors.New("store down") })
	f.clock.advance(time.Minute)
	if err := f.svc.Tick(f.ctx); err == nil || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("Tick with a failing Settled = %v, want its error", err)
	}
	for _, exec := range []domain.LadoExecution{recorded, unrecorded} {
		if _, ok := f.store.settledAt(exec.SessionID); ok {
			t.Errorf("%s settled although Settled failed", exec.SessionID)
		}
	}
	if _, ok := f.store.settledAt(closed.SessionID); !ok {
		t.Error("the closed PR did not settle")
	}
	db.read(func(f *fakeDBChanges) {
		byURL := map[string]fakeSettled{}
		for _, s := range f.settled {
			byURL[s.exec.PR.URL] = s
		}
		if _, told := byURL[closed.PR.URL]; told || len(byURL) != 2 {
			t.Fatalf("Settled told %d PRs (closed told: %v), want the two merged ones", len(byURL), told)
		}
		one := byURL[recorded.PR.URL]
		if one.record == nil || one.record.HeadSHA != verifyHead1 || one.in != nil {
			t.Errorf("recorded PR told record %v and input %v, want its record and no input", one.record, one.in)
		}
		two := byURL[unrecorded.PR.URL]
		if two.record != nil || two.in == nil || two.in.Head != verifyHead2 || two.in.Base != verifyBase || two.in.ProjectID != "p1" {
			t.Errorf("unrecorded PR told record %v and input %+v, want no record and its input", two.record, two.in)
		}
	})

	db.set(func(f *fakeDBChanges) { f.settleErr = nil })
	f.clock.advance(failedRetryAfter)
	f.tick()
	for _, exec := range []domain.LadoExecution{recorded, unrecorded} {
		if _, ok := f.store.settledAt(exec.SessionID); !ok {
			t.Errorf("%s did not settle once Settled worked", exec.SessionID)
		}
	}
}

// #97: no name the check command runner withholds from the daemon's
// environment or sets for git, nor GH_CONFIG_DIR, may name a secret: a
// secret could otherwise hand a command the credentials LADO keeps from it.
func TestDBChangeSecretNamesExcludeTheRunnersVariables(t *testing.T) {
	var names []string
	for _, prefix := range withheldPrefixes {
		names = append(names, prefix+"X")
	}
	for _, suffix := range withheldSuffixes {
		names = append(names, "X"+suffix)
	}
	names = append(names, withheldNames...)
	for _, entry := range gitCredentialBlock {
		names = append(names, envName(entry))
	}
	names = append(names, "GH_CONFIG_DIR")
	for _, name := range names {
		problems := domain.ValidateLadoDBChangeSettings(domain.LadoDBChangeSettings{
			Enabled: true, Paths: []string{"db/*.sql"}, Command: "psql -f {file}", SecretNames: []string{name},
		})
		if len(problems) != 1 || problems[0].Field != "secretNames[0]" || problems[0].Reason != domain.LadoDBChangeProblemReservedSecret {
			t.Errorf("secret name %s: problems %+v, want it reserved", name, problems)
		}
		if lower := strings.ToLower(name); !domain.LadoDBChangeReservedEnv(lower) {
			t.Errorf("%s is not reserved in lower case", lower)
		}
	}
	if problems := domain.ValidateLadoDBChangeSettings(domain.LadoDBChangeSettings{
		Enabled: true, Paths: []string{"db/*.sql"}, Command: "psql -f {file}", SecretNames: []string{"LADO_DB_DSN"},
	}); len(problems) != 0 {
		t.Errorf("LADO_DB_DSN: problems %+v, want none", problems)
	}
}
