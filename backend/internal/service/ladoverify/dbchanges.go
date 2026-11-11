package ladoverify

// LADO fork: production database changes tied to the integration approval
// (#97, docs/LJY-PRD/adr/0013-db-changes-with-integration-approval.md). The
// verification loop does not apply SQL itself: the daemon installs the
// database change service (ladodbchange) with SetDBChanges, and the loop
// asks it at five points.
//
//   - A head's record gets the optional db_changes item after evidence, in a
//     repository whose settings are on (record.go).
//   - The lado/integration status of an approved head that still has pre-merge
//     SQL to apply is held: not posted, its attempts not counted (status.go).
//   - The merge gate refuses such a head last, after the approval, its base
//     and the merge order (mergegate.go), with LADO_DB_CHANGE_PENDING.
//   - A recorded approval tells the service, which queues the SQL (approve.go).
//   - A merged PR tells the service before LADO settles it, so SQL merged
//     outside LADO is seen; an error leaves the PR to the next pass (settle.go).
//
// Without the service, as in a project LADO wires none for, nothing changes.

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DBChanges is the database change service (ladodbchange) as the loop asks
// it.
type DBChanges interface {
	// Item computes the db_changes item of a head. ok is false when the
	// repository's settings are off or missing: the record then has no such
	// item.
	Item(ctx context.Context, in domain.LadoDBChangeInput) (domain.LadoVerifyItem, bool, error)
	// PendingPreMerge returns the pre-merge files of details not applied
	// yet, none when the settings are off.
	PendingPreMerge(ctx context.Context, project domain.ProjectID, details domain.LadoDBChangesDetails) ([]domain.LadoDBChangeFileRef, error)
	// HoldIntegrationStatus reports whether the approval's status waits for
	// its record's pre-merge SQL, and if so records the hold, which keeps
	// the approval out of the status retries.
	HoldIntegrationStatus(ctx context.Context, approval domain.LadoIntegrationApproval, record domain.LadoVerification) (bool, error)
	// IntegrationApproved is told of the approvals of an execution's heads
	// right after they were recorded. What fails, it logs.
	IntegrationApproved(ctx context.Context, exec domain.LadoExecution, approvals []domain.LadoIntegrationApproval)
	// Settled is told of an execution's merged PR before LADO settles it:
	// the stored record of its head and what LADO knows of the head, each
	// nil when it could not be read. An error leaves the PR unsettled, so
	// the next pass tells it again.
	Settled(ctx context.Context, exec domain.LadoExecution, record *domain.LadoVerification, in *domain.LadoDBChangeInput) error
}

// dbChangesSlot holds the installed DBChanges.
type dbChangesSlot struct {
	p atomic.Pointer[DBChanges]
}

// SetDBChanges installs the database change service. The daemon calls it
// before Start; nil, the default, keeps the loop as it was before #97.
func (s *Service) SetDBChanges(db DBChanges) {
	if db == nil {
		s.dbChanges.p.Store(nil)
		return
	}
	s.dbChanges.p.Store(&db)
}

// dbChangesService returns the installed DBChanges, nil without one.
func (s *Service) dbChangesService() DBChanges {
	if db := s.dbChanges.p.Load(); db != nil {
		return *db
	}
	return nil
}

// dbChangesInput is what the database change service reads of a head: its
// project, its repository's folder in a workspace ("" for a single
// repository), the repository's folder on disk, the merge base and the head,
// and the approved plan.
func dbChangesInput(in headInputs, base string) domain.LadoDBChangeInput {
	return domain.LadoDBChangeInput{
		ProjectID: in.task.ProjectID,
		Repo:      in.prefix,
		Dir:       in.dir,
		Base:      base,
		Head:      in.head,
		Plan:      in.plan,
	}
}

// dbChangesItem computes the head's db_changes item, false for a repository
// whose settings are off, without a service, or when the merge base could
// not be read: the paths item then waits too, so the record cannot be
// approved before the item is computed. An item the service cannot compute
// waits as verifying.
func (s *Service) dbChangesItem(ctx context.Context, in headInputs, branch branchFacts) (domain.LadoVerifyItem, bool) {
	db := s.dbChangesService()
	if db == nil || branch.err != nil || branch.base == "" {
		return domain.LadoVerifyItem{}, false
	}
	item, ok, err := db.Item(ctx, dbChangesInput(in, branch.base))
	switch {
	case err != nil:
		s.deps.Log.Warn("LADO could not compute the db_changes item; it waits", "session", in.exec.SessionID, "head", in.head, "err", redactedErrorLine(err))
		return verifyingItem(domain.LadoVerifyDBChanges), true
	case !ok:
		return domain.LadoVerifyItem{}, false
	case item.Kind != domain.LadoVerifyDBChanges:
		s.deps.Log.Warn("LADO ignored a db_changes item of another kind; it waits", "session", in.exec.SessionID, "head", in.head, "kind", item.Kind)
		return verifyingItem(domain.LadoVerifyDBChanges), true
	}
	return item, true
}

// dbChangesHeld reports whether the status of a live approval waits for the
// pre-merge SQL of its stored record (DBChanges.HoldIntegrationStatus). A
// revoked approval, which posts pending, a record without a db_changes item
// and a loop without the service never wait.
func (s *Service) dbChangesHeld(ctx context.Context, approval domain.LadoIntegrationApproval) (bool, error) {
	db := s.dbChangesService()
	if db == nil || approval.Revoked() {
		return false, nil
	}
	record, found, err := s.deps.Store.GetLadoVerification(ctx, approval.ExecutionSessionID, approval.HeadSHA)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if _, has, err := record.DBChanges(); err != nil || !has {
		return false, err
	}
	return db.HoldIntegrationStatus(ctx, approval, record)
}

// dbChangesPending returns the pre-merge files of the approved head's
// stored record that are not applied yet, none without the service or a
// db_changes item.
func (s *Service) dbChangesPending(ctx context.Context, approval domain.LadoIntegrationApproval, head string) ([]domain.LadoDBChangeFileRef, error) {
	db := s.dbChangesService()
	if db == nil {
		return nil, nil
	}
	record, found, err := s.deps.Store.GetLadoVerification(ctx, approval.ExecutionSessionID, head)
	if err != nil || !found {
		return nil, err
	}
	details, has, err := record.DBChanges()
	if err != nil || !has || len(details.PreMerge()) == 0 {
		return nil, err
	}
	project, err := s.executionProject(ctx, approval.ExecutionSessionID)
	if err != nil {
		return nil, err
	}
	return db.PendingPreMerge(ctx, project, details)
}

// executionProject is the project of the execution's task.
func (s *Service) executionProject(ctx context.Context, session domain.SessionID) (domain.ProjectID, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, session)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "", fmt.Errorf("execution %s not found", session)
	}
	task, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "", fmt.Errorf("task %s not found", exec.TaskID)
	}
	return task.ProjectID, nil
}

// dbChangesSettled tells the service of the execution's merged PR before
// LADO settles it (DBChanges.Settled): the stored record of the merged head,
// and, when that record has no db_changes item, what LADO knows of the head
// (settledInput). A closed PR, or a loop without the service, tells nothing.
func (s *Service) dbChangesSettled(ctx context.Context, exec domain.LadoExecution) error {
	db := s.dbChangesService()
	if db == nil || !exec.PR.Merged {
		return nil
	}
	var record *domain.LadoVerification
	if exec.PR.HeadSHA != "" {
		stored, found, err := s.deps.Store.GetLadoVerification(ctx, exec.SessionID, exec.PR.HeadSHA)
		if err != nil {
			return err
		}
		if found {
			record = &stored
		}
	}
	var in *domain.LadoDBChangeInput
	if _, has, _ := recordDBChanges(record); !has {
		in = s.settledInput(ctx, exec)
	}
	return db.Settled(ctx, exec, record, in)
}

// recordDBChanges reads the db_changes details of a record that may be nil.
func recordDBChanges(record *domain.LadoVerification) (domain.LadoDBChangesDetails, bool, error) {
	if record == nil {
		return domain.LadoDBChangesDetails{}, false, nil
	}
	return record.DBChanges()
}

// settledInput is what LADO knows of a merged head, nil when it cannot tell:
// its inputs, and where its branch left the target. The merge moved the
// target onto the head, so the merge base is the one with the base tip AO
// last observed for the PR, else with the remote-tracking target while it
// is not the head itself.
func (s *Service) settledInput(ctx context.Context, exec domain.LadoExecution) *domain.LadoDBChangeInput {
	if exec.PR.HeadSHA == "" {
		return nil
	}
	in, err := s.headInputs(ctx, exec, false)
	if err != nil {
		s.deps.Log.Info("LADO could not read a merged head's inputs for its database changes", "session", exec.SessionID, "pr", exec.PR.URL, "err", redactedErrorLine(err))
		return nil
	}
	base, err := s.observedMergeBase(ctx, in)
	if err != nil || base == "" || strings.EqualFold(base, in.head) {
		base, err = s.deps.Git.MergeBase(ctx, in.dir, "refs/remotes/"+in.baseRemote+"/"+in.target, in.head)
	}
	if err != nil || base == "" || strings.EqualFold(base, in.head) {
		return nil
	}
	input := dbChangesInput(in, base)
	return &input
}
