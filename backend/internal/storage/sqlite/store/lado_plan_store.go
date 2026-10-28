package store

// LADO fork: plan requests, plan revisions, plan approvals and the rule
// snapshots approvals fix (docs/LJY-PRD/TDD.md §3.1, §5.1 and §5.2).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateLadoPlanRequest stores a plan request before LADO delivers it.
func (s *Store) CreateLadoPlanRequest(ctx context.Context, req domain.LadoPlanRequest) (domain.LadoPlanRequest, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoPlanRequest{}, err
	}
	defer s.writeMu.Unlock()
	row, err := s.qw.InsertLadoPlanRequest(ctx, gen.InsertLadoPlanRequestParams{
		ID:                  string(req.ID),
		TaskID:              string(req.TaskID),
		SessionID:           string(req.SessionID),
		IssueSnapshotSha256: req.IssueSnapshotSHA256,
		SourceCommentID:     req.SourceCommentID,
		Note:                req.Note,
		BasedOnRevisionID:   string(req.BasedOn),
		RequestedAt:         req.RequestedAt.UTC(),
	})
	if err != nil {
		return domain.LadoPlanRequest{}, fmt.Errorf("create LADO plan request: %w", err)
	}
	return ladoPlanRequestFromGen(row), nil
}

// LadoPlanRequest returns one plan request.
func (s *Store) LadoPlanRequest(ctx context.Context, id domain.LadoPlanRequestID) (domain.LadoPlanRequest, bool, error) {
	row, err := s.qr.GetLadoPlanRequest(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoPlanRequest{}, false, nil
	}
	if err != nil {
		return domain.LadoPlanRequest{}, false, fmt.Errorf("read LADO plan request: %w", err)
	}
	return ladoPlanRequestFromGen(row), true, nil
}

// ListLadoPlanRequests returns a task's plan requests, oldest first.
func (s *Store) ListLadoPlanRequests(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanRequest, error) {
	rows, err := s.qr.ListLadoPlanRequestsByTask(ctx, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("list LADO plan requests: %w", err)
	}
	return ladoPlanRequestsFromGen(rows), nil
}

// ListLadoPlanRequestsForLiveTasks returns the plan requests of every task
// that is not cancelled, grouped by task and oldest first.
func (s *Store) ListLadoPlanRequestsForLiveTasks(ctx context.Context) ([]domain.LadoPlanRequest, error) {
	rows, err := s.qr.ListLadoPlanRequestsForLiveTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list LADO plan requests: %w", err)
	}
	return ladoPlanRequestsFromGen(rows), nil
}

// MarkLadoPlanRequestDelivered records that the orchestrator took the
// request. A request already delivered or given up is left alone.
func (s *Store) MarkLadoPlanRequestDelivered(ctx context.Context, id domain.LadoPlanRequestID, at time.Time) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if _, err := s.qw.MarkLadoPlanRequestDelivered(ctx, gen.MarkLadoPlanRequestDeliveredParams{
		DeliveredAt: ladoNullTime(at),
		ID:          string(id),
	}); err != nil {
		return fmt.Errorf("mark LADO plan request delivered: %w", err)
	}
	return nil
}

// RecordLadoPlanRequestAttempt counts a failed delivery attempt.
func (s *Store) RecordLadoPlanRequestAttempt(ctx context.Context, id domain.LadoPlanRequestID, problem string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if _, err := s.qw.RecordLadoPlanRequestAttempt(ctx, gen.RecordLadoPlanRequestAttemptParams{
		DeliveryError: problem,
		ID:            string(id),
	}); err != nil {
		return fmt.Errorf("record LADO plan request attempt: %w", err)
	}
	return nil
}

// FailLadoPlanRequest records that LADO stopped trying to deliver a request.
func (s *Store) FailLadoPlanRequest(ctx context.Context, id domain.LadoPlanRequestID, at time.Time, problem string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if _, err := s.qw.FailLadoPlanRequest(ctx, gen.FailLadoPlanRequestParams{
		FailedAt:      ladoNullTime(at),
		DeliveryError: problem,
		ID:            string(id),
	}); err != nil {
		return fmt.Errorf("fail LADO plan request: %w", err)
	}
	return nil
}

// LadoArtifactVersionPinned reports whether an AO artifact version is already
// a plan revision.
func (s *Store) LadoArtifactVersionPinned(ctx context.Context, sessionID domain.SessionID, path string, version int, sha256 string) (bool, error) {
	n, err := s.qr.CountPinnedLadoArtifactVersion(ctx, gen.CountPinnedLadoArtifactVersionParams{
		SessionID:       string(sessionID),
		Path:            path,
		ArtifactVersion: int64(version),
		Sha256:          sha256,
	})
	if err != nil {
		return false, fmt.Errorf("read LADO plan revisions: %w", err)
	}
	return n > 0, nil
}

// PinLadoPlanRevision stores a new revision as the task's next number. It
// reports false, with the stored revision, when that artifact version was
// pinned already.
func (s *Store) PinLadoPlanRevision(ctx context.Context, rev domain.LadoPlanRevision) (domain.LadoPlanRevision, bool, error) {
	problems, err := json.Marshal(nonNilLadoPlanProblems(rev.Problems))
	if err != nil {
		return domain.LadoPlanRevision{}, false, fmt.Errorf("pin LADO plan revision: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoPlanRevision{}, false, err
	}
	defer s.writeMu.Unlock()
	var pinned domain.LadoPlanRevision
	created := false
	err = s.inTx(ctx, "pin LADO plan revision", func(q *gen.Queries) error {
		n, err := q.CountPinnedLadoArtifactVersion(ctx, gen.CountPinnedLadoArtifactVersionParams{
			SessionID:       string(rev.SessionID),
			Path:            rev.Path,
			ArtifactVersion: int64(rev.ArtifactVersion),
			Sha256:          rev.SHA256,
		})
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		count, err := q.CountLadoPlanRevisionsForTask(ctx, string(rev.TaskID))
		if err != nil {
			return err
		}
		row, err := q.InsertLadoPlanRevision(ctx, gen.InsertLadoPlanRevisionParams{
			ID:                  string(rev.ID),
			TaskID:              string(rev.TaskID),
			RequestID:           string(rev.RequestID),
			Number:              count + 1,
			SessionID:           string(rev.SessionID),
			Path:                rev.Path,
			ArtifactVersion:     int64(rev.ArtifactVersion),
			Source:              rev.Source,
			Sha256:              rev.SHA256,
			BaseRef:             rev.BaseRef,
			BaseSha:             rev.BaseSHA,
			IssueSnapshotSha256: rev.IssueSnapshotSHA256,
			ProblemsJson:        string(problems),
			CreatedAt:           rev.CreatedAt.UTC(),
		})
		if err != nil {
			return err
		}
		pinned, created = ladoPlanRevisionFromGen(row), true
		return nil
	})
	if err != nil {
		return domain.LadoPlanRevision{}, false, err
	}
	return pinned, created, nil
}

// LadoPlanRevision returns one revision.
func (s *Store) LadoPlanRevision(ctx context.Context, id domain.LadoPlanRevisionID) (domain.LadoPlanRevision, bool, error) {
	row, err := s.qr.GetLadoPlanRevision(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoPlanRevision{}, false, nil
	}
	if err != nil {
		return domain.LadoPlanRevision{}, false, fmt.Errorf("read LADO plan revision: %w", err)
	}
	return ladoPlanRevisionFromGen(row), true, nil
}

// ListLadoPlanRevisions returns a task's revisions in order.
func (s *Store) ListLadoPlanRevisions(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanRevision, error) {
	rows, err := s.qr.ListLadoPlanRevisionsByTask(ctx, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("list LADO plan revisions: %w", err)
	}
	revisions := make([]domain.LadoPlanRevision, 0, len(rows))
	for _, row := range rows {
		revisions = append(revisions, ladoPlanRevisionFromGen(row))
	}
	return revisions, nil
}

// ListLadoPlanApprovals returns a task's approvals, oldest first.
func (s *Store) ListLadoPlanApprovals(ctx context.Context, taskID domain.LadoTaskID) ([]domain.LadoPlanApproval, error) {
	rows, err := s.qr.ListLadoPlanApprovalsByTask(ctx, string(taskID))
	if err != nil {
		return nil, fmt.Errorf("list LADO plan approvals: %w", err)
	}
	approvals := make([]domain.LadoPlanApproval, 0, len(rows))
	for _, row := range rows {
		approvals = append(approvals, ladoPlanApprovalFromGen(row))
	}
	return approvals, nil
}

// ApproveLadoPlan records an approval, the presence proof it used, the rule
// snapshot it fixed, its queue entry and its audit event in one transaction
// (docs/LJY-PRD/TDD.md §5.2 and §5.3). Inside the transaction it checks again
// that the revision is still the task's latest with the approved sha256, so a
// revision pinned meanwhile is not approved by mistake
// (domain.ErrLadoPlanSuperseded). A proof whose nonce was used before is
// refused with domain.ErrLadoPresenceReplayed. A nil snapshot stores the
// approval without one; otherwise the snapshot takes the approval's id and
// time and is stored with the rule versions it used, which must all still
// exist, and pins the setup item versions it used, whose items must still be
// live: the rules and the setup are resolved before the transaction, so a rule
// deleted or a setup item ended in between is refused with
// domain.ErrLadoRulesChanged. The approval supersedes
// the task's open queue entries and, with a snapshot, queues entry under the
// approval's task, id and time, the task's project and the snapshot's id; a
// nil snapshot or entry queues nothing. When any part fails, nothing is kept.
func (s *Store) ApproveLadoPlan(ctx context.Context, approval domain.LadoPlanApproval, proof domain.LadoPresenceProof, event domain.LadoAuditEvent, snapshot *domain.LadoPolicySnapshot, entry *domain.LadoQueueEntry) (domain.LadoPlanApproval, domain.LadoAuditEvent, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoPlanApproval{}, domain.LadoAuditEvent{}, err
	}
	defer s.writeMu.Unlock()
	var (
		stored   domain.LadoPlanApproval
		appended domain.LadoAuditEvent
	)
	err := s.inTx(ctx, "approve LADO plan", func(q *gen.Queries) error {
		latest, err := q.GetLatestLadoPlanRevision(ctx, string(approval.TaskID))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrLadoPlanSuperseded
		}
		if err != nil {
			return err
		}
		if latest.ID != string(approval.RevisionID) || latest.Sha256 != approval.SHA256 {
			return domain.ErrLadoPlanSuperseded
		}
		if snapshot != nil {
			if err := checkLadoSnapshotRulesTx(ctx, q, *snapshot); err != nil {
				return err
			}
		}
		if err := insertLadoPresenceProof(ctx, q, proof); err != nil {
			return err
		}
		row, err := q.InsertLadoPlanApproval(ctx, gen.InsertLadoPlanApprovalParams{
			ID:         string(approval.ID),
			RevisionID: string(approval.RevisionID),
			TaskID:     string(approval.TaskID),
			Sha256:     approval.SHA256,
			ProofNonce: proof.Nonce,
			ApprovedAt: approval.ApprovedAt.UTC(),
		})
		if err != nil {
			return err
		}
		stored = ladoPlanApprovalFromGen(row)
		if snapshot != nil {
			if err := insertLadoPolicySnapshotTx(ctx, q, stored, *snapshot); err != nil {
				return err
			}
		}
		if err := queueLadoApprovalTx(ctx, q, stored, snapshot, entry); err != nil {
			return err
		}
		appended, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	if err != nil {
		return domain.LadoPlanApproval{}, domain.LadoAuditEvent{}, unwrapLadoSentinel(err)
	}
	return stored, appended, nil
}

// LadoPolicySnapshot returns the rule snapshot of one approval with the rule
// versions it used, ordered by rule id, and the setup item versions it
// pinned. It reports false for an approval without a snapshot.
func (s *Store) LadoPolicySnapshot(ctx context.Context, approvalID domain.LadoPlanApprovalID) (domain.LadoPolicySnapshot, bool, error) {
	row, err := s.qr.GetLadoPolicySnapshotByApproval(ctx, string(approvalID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoPolicySnapshot{}, false, nil
	}
	if err != nil {
		return domain.LadoPolicySnapshot{}, false, fmt.Errorf("read LADO rule snapshot: %w", err)
	}
	// The rule rows commit with the snapshot row and never change.
	rules, err := s.qr.ListLadoPolicySnapshotRules(ctx, row.ID)
	if err != nil {
		return domain.LadoPolicySnapshot{}, false, fmt.Errorf("read LADO rule snapshot rules: %w", err)
	}
	setup, err := s.qr.ListLadoPolicySnapshotSetupVersions(ctx, row.ID)
	if err != nil {
		return domain.LadoPolicySnapshot{}, false, fmt.Errorf("read LADO rule snapshot setup versions: %w", err)
	}
	snapshot := ladoPolicySnapshotFromGen(row)
	snapshot.Rules = make([]domain.LadoPolicySnapshotRule, 0, len(rules))
	for _, rule := range rules {
		snapshot.Rules = append(snapshot.Rules, domain.LadoPolicySnapshotRule{
			RuleID:    domain.LadoRuleID(rule.RuleID),
			VersionID: domain.LadoRuleVersionID(rule.RuleVersionID),
		})
	}
	// Ordered by tool, kind and name; nil without pins.
	for _, pin := range setup {
		snapshot.SetupVersionIDs = append(snapshot.SetupVersionIDs, domain.LadoSetupItemVersionID(pin.LadoSetupItemVersion.ID))
	}
	return snapshot, true, nil
}

// checkLadoSnapshotRulesTx refuses a snapshot that uses a rule version that
// no longer exists with domain.ErrLadoRulesChanged.
func checkLadoSnapshotRulesTx(ctx context.Context, q *gen.Queries, snapshot domain.LadoPolicySnapshot) error {
	for _, rule := range snapshot.Rules {
		n, err := q.CountLadoRuleVersion(ctx, gen.CountLadoRuleVersionParams{ID: string(rule.VersionID), RuleID: string(rule.RuleID)})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrLadoRulesChanged
		}
	}
	return nil
}

// insertLadoPolicySnapshotTx stores the rule snapshot of a stored approval,
// with the approval's id and time, the rule versions it used and the pins of
// its setup item versions. A version that is gone, or whose item ended, is
// refused with domain.ErrLadoRulesChanged.
func insertLadoPolicySnapshotTx(ctx context.Context, q *gen.Queries, approval domain.LadoPlanApproval, snapshot domain.LadoPolicySnapshot) error {
	if _, err := q.InsertLadoPolicySnapshot(ctx, gen.InsertLadoPolicySnapshotParams{
		ID:           string(snapshot.ID),
		ApprovalID:   string(approval.ID),
		Sha256:       snapshot.SHA256,
		SnapshotJson: string(snapshot.Content),
		Blocking:     boolInt(snapshot.Blocking),
		CreatedAt:    approval.ApprovedAt.UTC(),
	}); err != nil {
		return err
	}
	for _, rule := range snapshot.Rules {
		if err := q.InsertLadoPolicySnapshotRule(ctx, gen.InsertLadoPolicySnapshotRuleParams{
			SnapshotID:    string(snapshot.ID),
			RuleID:        string(rule.RuleID),
			RuleVersionID: string(rule.VersionID),
		}); err != nil {
			return err
		}
	}
	for _, id := range snapshot.SetupVersionIDs {
		n, err := q.CountLiveLadoSetupItemVersion(ctx, string(id))
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrLadoRulesChanged
		}
		if err := q.InsertLadoPolicySnapshotSetup(ctx, gen.InsertLadoPolicySnapshotSetupParams{
			SnapshotID:    string(snapshot.ID),
			ItemVersionID: string(id),
		}); err != nil {
			return err
		}
	}
	return nil
}

// SetLadoModeWithProof stores the project's LADO mode together with the
// presence proof that allowed the change and its audit event, in one
// transaction. Turning LADO mode off needs presence from step 4b
// (docs/LJY-PRD/TDD.md §13).
func (s *Store) SetLadoModeWithProof(ctx context.Context, projectID domain.ProjectID, on bool, at time.Time, proof domain.LadoPresenceProof, event domain.LadoAuditEvent) (domain.LadoProjectSettings, error) {
	mode := int64(0)
	if on {
		mode = 1
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return domain.LadoProjectSettings{}, err
	}
	defer s.writeMu.Unlock()
	var settings domain.LadoProjectSettings
	err := s.inTx(ctx, "set LADO mode", func(q *gen.Queries) error {
		if err := insertLadoPresenceProof(ctx, q, proof); err != nil {
			return err
		}
		row, err := q.UpsertLadoProjectSettings(ctx, gen.UpsertLadoProjectSettingsParams{
			ProjectID: string(projectID),
			LadoMode:  mode,
			UpdatedAt: at.UTC(),
		})
		if err != nil {
			return err
		}
		if settings, err = ladoProjectSettingsFromGen(row); err != nil {
			return err
		}
		_, err = appendLadoAuditEventTx(ctx, q, event)
		return err
	})
	if err != nil {
		return domain.LadoProjectSettings{}, unwrapLadoSentinel(err)
	}
	return settings, nil
}

func insertLadoPresenceProof(ctx context.Context, q *gen.Queries, proof domain.LadoPresenceProof) error {
	dev := int64(0)
	if proof.Dev {
		dev = 1
	}
	err := q.InsertLadoPresenceProof(ctx, gen.InsertLadoPresenceProofParams{
		Nonce:     proof.Nonce,
		Action:    proof.Action,
		SubjectID: proof.SubjectID,
		Dev:       dev,
		ExpiresAt: proof.ExpiresAt.UTC(),
		UsedAt:    proof.UsedAt.UTC(),
	})
	if isSQLiteUnique(err) || isSQLitePrimaryKey(err) {
		return domain.ErrLadoPresenceReplayed
	}
	return err
}

// isSQLitePrimaryKey reports a PRIMARY KEY violation, which SQLite reports
// apart from other UNIQUE violations.
func isSQLitePrimaryKey(err error) bool {
	var sqliteErr *moderncsqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

// unwrapLadoSentinel returns a LADO domain sentinel as is, so callers can
// match it without the transaction's wrapping text.
func unwrapLadoSentinel(err error) error {
	for _, sentinel := range []error{
		domain.ErrLadoLeaseLost,
		domain.ErrLadoPresenceReplayed,
		domain.ErrLadoPlanSuperseded,
		domain.ErrLadoRuleChanged,
		domain.ErrLadoRuleReferenced,
		domain.ErrLadoRulesChanged,
		domain.ErrLadoSetupItemExists,
		domain.ErrLadoSetupItemChanged,
		domain.ErrLadoSetupItemNotLive,
		domain.ErrLadoSetupAlreadyRestored,
		domain.ErrLadoConflictAcceptanceNotLive,
		domain.ErrLadoIntegrationAlreadyApproved,
		domain.ErrLadoVerificationChanged,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return err
}

// ladoTaskPlan reads the plan facts of one task.
func (s *Store) ladoTaskPlan(ctx context.Context, id domain.LadoTaskID) (domain.LadoTaskPlan, error) {
	var plan domain.LadoTaskPlan
	requests, err := s.qr.ListLadoPlanRequestsByTask(ctx, string(id))
	if err != nil {
		return plan, fmt.Errorf("read LADO plan requests: %w", err)
	}
	if len(requests) > 0 {
		plan.Request = ladoPlanRequestFromGen(requests[len(requests)-1])
	}
	latest, err := s.qr.GetLatestLadoPlanRevision(ctx, string(id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return plan, fmt.Errorf("read LADO plan revision: %w", err)
	default:
		plan.Latest = ladoPlanRevisionFromGen(latest)
	}
	approval, err := s.qr.GetLatestLadoPlanApproval(ctx, string(id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return plan, nil
	case err != nil:
		return plan, fmt.Errorf("read LADO plan approval: %w", err)
	}
	plan.Approval = ladoPlanApprovalFromGen(approval)
	snapshot, err := s.qr.GetLadoPolicySnapshotByApproval(ctx, approval.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return plan, fmt.Errorf("read LADO rule snapshot: %w", err)
	default:
		plan.Snapshot = ladoPolicySnapshotRefFromGen(snapshot)
	}
	return plan, nil
}

// ladoProjectPlans reads the plan facts of every task in a project.
func (s *Store) ladoProjectPlans(ctx context.Context, projectID domain.ProjectID) (map[domain.LadoTaskID]domain.LadoTaskPlan, error) {
	plans := map[domain.LadoTaskID]domain.LadoTaskPlan{}
	requests, err := s.qr.ListLadoPlanRequestsByProject(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("read LADO plan requests: %w", err)
	}
	for _, row := range requests {
		id := domain.LadoTaskID(row.TaskID)
		plan := plans[id]
		plan.Request = ladoPlanRequestFromGen(row)
		plans[id] = plan
	}
	revisions, err := s.qr.ListLatestLadoPlanRevisionsByProject(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("read LADO plan revisions: %w", err)
	}
	for _, row := range revisions {
		id := domain.LadoTaskID(row.TaskID)
		plan := plans[id]
		plan.Latest = ladoPlanRevisionFromGen(row)
		plans[id] = plan
	}
	approvals, err := s.qr.ListLadoPlanApprovalsByProject(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("read LADO plan approvals: %w", err)
	}
	for _, row := range approvals {
		id := domain.LadoTaskID(row.TaskID)
		plan := plans[id]
		plan.Approval = ladoPlanApprovalFromGen(row)
		plans[id] = plan
	}
	snapshots, err := s.qr.ListLadoPolicySnapshotsByProject(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("read LADO rule snapshots: %w", err)
	}
	// A task carries the snapshot of its newest approval, which may have none.
	byApproval := make(map[domain.LadoPlanApprovalID]gen.LadoPolicySnapshot, len(snapshots))
	for _, row := range snapshots {
		byApproval[domain.LadoPlanApprovalID(row.ApprovalID)] = row
	}
	for id, plan := range plans {
		if row, ok := byApproval[plan.Approval.ID]; ok {
			plan.Snapshot = ladoPolicySnapshotRefFromGen(row)
			plans[id] = plan
		}
	}
	return plans, nil
}

func ladoPlanRequestsFromGen(rows []gen.LadoPlanRequest) []domain.LadoPlanRequest {
	requests := make([]domain.LadoPlanRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, ladoPlanRequestFromGen(row))
	}
	return requests
}

func ladoPlanRequestFromGen(row gen.LadoPlanRequest) domain.LadoPlanRequest {
	return domain.LadoPlanRequest{
		ID:                  domain.LadoPlanRequestID(row.ID),
		TaskID:              domain.LadoTaskID(row.TaskID),
		SessionID:           domain.SessionID(row.SessionID),
		IssueSnapshotSHA256: row.IssueSnapshotSha256,
		SourceCommentID:     row.SourceCommentID,
		Note:                row.Note,
		BasedOn:             domain.LadoPlanRevisionID(row.BasedOnRevisionID),
		RequestedAt:         row.RequestedAt.UTC(),
		DeliveredAt:         ladoTimeFromNull(row.DeliveredAt),
		DeliveryAttempts:    int(row.DeliveryAttempts),
		DeliveryError:       row.DeliveryError,
		FailedAt:            ladoTimeFromNull(row.FailedAt),
	}
}

func ladoPlanRevisionFromGen(row gen.LadoPlanRevision) domain.LadoPlanRevision {
	var problems []domain.LadoPlanProblem
	// The column only ever holds what PinLadoPlanRevision wrote.
	_ = json.Unmarshal([]byte(row.ProblemsJson), &problems)
	return domain.LadoPlanRevision{
		ID:                  domain.LadoPlanRevisionID(row.ID),
		TaskID:              domain.LadoTaskID(row.TaskID),
		RequestID:           domain.LadoPlanRequestID(row.RequestID),
		Number:              int(row.Number),
		SessionID:           domain.SessionID(row.SessionID),
		Path:                row.Path,
		ArtifactVersion:     int(row.ArtifactVersion),
		Source:              row.Source,
		SHA256:              row.Sha256,
		BaseRef:             row.BaseRef,
		BaseSHA:             row.BaseSha,
		IssueSnapshotSHA256: row.IssueSnapshotSha256,
		Problems:            problems,
		CreatedAt:           row.CreatedAt.UTC(),
	}
}

func ladoPlanApprovalFromGen(row gen.LadoPlanApproval) domain.LadoPlanApproval {
	return domain.LadoPlanApproval{
		ID:         domain.LadoPlanApprovalID(row.ID),
		RevisionID: domain.LadoPlanRevisionID(row.RevisionID),
		TaskID:     domain.LadoTaskID(row.TaskID),
		SHA256:     row.Sha256,
		ProofNonce: row.ProofNonce,
		ApprovedAt: row.ApprovedAt.UTC(),
	}
}

func ladoPolicySnapshotFromGen(row gen.LadoPolicySnapshot) domain.LadoPolicySnapshot {
	return domain.LadoPolicySnapshot{
		ID:         domain.LadoPolicySnapshotID(row.ID),
		ApprovalID: domain.LadoPlanApprovalID(row.ApprovalID),
		SHA256:     row.Sha256,
		Content:    []byte(row.SnapshotJson),
		Blocking:   row.Blocking == 1,
		CreatedAt:  row.CreatedAt.UTC(),
	}
}

func ladoPolicySnapshotRefFromGen(row gen.LadoPolicySnapshot) domain.LadoPolicySnapshotRef {
	return domain.LadoPolicySnapshotRef{
		ID:       domain.LadoPolicySnapshotID(row.ID),
		SHA256:   row.Sha256,
		Blocking: row.Blocking == 1,
	}
}

func nonNilLadoPlanProblems(problems []domain.LadoPlanProblem) []domain.LadoPlanProblem {
	if problems == nil {
		return []domain.LadoPlanProblem{}
	}
	return problems
}
