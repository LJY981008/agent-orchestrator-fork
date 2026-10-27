package store

// LADO fork: verification of an execution's PR head, its evidence and the
// integration approval (docs/LJY-PRD/TDD.md §7 and §8). The verification loop
// runs the project's check commands on the head, records the review rounds it
// starts and the evidence it resolves, and stores one record per execution
// and head; the developer approves the integration of one head with a
// presence proof. Task states stay derived: execution reads carry the record
// and the approval of the PR's current head (fillLadoVerification).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// StartLadoCheckRun stores run as running: its id, execution, head, name,
// command and start time. Its outcome is FinishLadoCheckRun's, so a run given
// in another state is refused.
func (s *Store) StartLadoCheckRun(ctx context.Context, run domain.LadoCheckRun) error {
	if run.ID == "" || run.ExecutionSessionID == "" || run.StartedAt.IsZero() {
		return errors.New("start LADO check run: an id, a session and a start time are required")
	}
	if run.Status != "" && run.Status != domain.LadoCheckRunRunning {
		return fmt.Errorf("start LADO check run: a run starts running, not %s", run.Status)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	if err := s.qw.InsertLadoCheckRun(ctx, gen.InsertLadoCheckRunParams{
		ID:                 run.ID,
		ExecutionSessionID: string(run.ExecutionSessionID),
		HeadSha:            run.HeadSHA,
		Name:               run.Name,
		Command:            run.Command,
		StartedAt:          run.StartedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("start LADO check run: %w", err)
	}
	return nil
}

// FinishLadoCheckRun records how a running check run ended at at: its final
// status, its exit code (nil when the command did not exit), the sha256 of
// its log blob and its error, without secrets. A run that finished before
// keeps its outcome, and the error is domain.ErrLadoCheckRunFinished.
func (s *Store) FinishLadoCheckRun(ctx context.Context, id string, status domain.LadoCheckRunStatus, exitCode *int, logSHA256, errorMessage string, at time.Time) error {
	if status == "" || status == domain.LadoCheckRunRunning || at.IsZero() {
		return errors.New("finish LADO check run: a final status and a time are required")
	}
	var exit sql.NullInt64
	if exitCode != nil {
		exit = sql.NullInt64{Int64: int64(*exitCode), Valid: true}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.FinishLadoCheckRun(ctx, gen.FinishLadoCheckRunParams{
		Status:       string(status),
		ExitCode:     exit,
		LogSha256:    logSHA256,
		ErrorMessage: errorMessage,
		FinishedAt:   ladoNullTime(at),
		ID:           id,
	})
	if err != nil {
		return fmt.Errorf("finish LADO check run: %w", err)
	}
	if n > 0 {
		return nil
	}
	// Nothing changed: the run finished before, or there is none.
	if _, err := s.qw.GetLadoCheckRun(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("finish LADO check run: no check run %s", id)
	} else if err != nil {
		return fmt.Errorf("finish LADO check run: %w", err)
	}
	return domain.ErrLadoCheckRunFinished
}

// CancelRunningLadoCheckRuns marks every running check run cancelled at at
// and returns how many it marked: at daemon start, the runs a stopped daemon
// left behind, which the loop then runs again.
func (s *Store) CancelRunningLadoCheckRuns(ctx context.Context, at time.Time) (int, error) {
	if at.IsZero() {
		return 0, errors.New("cancel running LADO check runs: a time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return 0, err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.CancelRunningLadoCheckRuns(ctx, ladoNullTime(at))
	if err != nil {
		return 0, fmt.Errorf("cancel running LADO check runs: %w", err)
	}
	return int(n), nil
}

// ListLadoCheckRuns returns the check runs of an execution's head by start
// time, oldest first.
func (s *Store) ListLadoCheckRuns(ctx context.Context, session domain.SessionID, headSHA string) ([]domain.LadoCheckRun, error) {
	rows, err := s.qr.ListLadoCheckRunsForHead(ctx, gen.ListLadoCheckRunsForHeadParams{ExecutionSessionID: string(session), HeadSha: headSHA})
	if err != nil {
		return nil, fmt.Errorf("list LADO check runs: %w", err)
	}
	runs := make([]domain.LadoCheckRun, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, ladoCheckRunFromGen(row))
	}
	return runs, nil
}

// GetLadoCheckRun returns one check run. It reports false for an unknown id.
func (s *Store) GetLadoCheckRun(ctx context.Context, id string) (domain.LadoCheckRun, bool, error) {
	row, err := s.qr.GetLadoCheckRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoCheckRun{}, false, nil
	}
	if err != nil {
		return domain.LadoCheckRun{}, false, fmt.Errorf("read LADO check run: %w", err)
	}
	return ladoCheckRunFromGen(row), true, nil
}

// UpsertLadoVerification stores the record of v's execution and head: its
// canonical content and sha256 (domain.LadoVerification.Content), its overall
// outcome and reason, and when it was computed. It reports whether the record
// is new or its outcome, reason or sha256 changed: only then is a change
// event published. A nil v.Notified keeps the stored list (empty for a new
// record); any other list replaces it.
func (s *Store) UpsertLadoVerification(ctx context.Context, v domain.LadoVerification) (bool, error) {
	if v.ExecutionSessionID == "" || v.HeadSHA == "" || v.ComputedAt.IsZero() {
		return false, errors.New("upsert LADO verification: a session, a head and a time are required")
	}
	content, err := v.Content()
	if err != nil {
		return false, fmt.Errorf("upsert LADO verification: %w", err)
	}
	sum, err := v.SHA256()
	if err != nil {
		return false, fmt.Errorf("upsert LADO verification: %w", err)
	}
	outcome, reason := v.Overall()
	var notified []byte
	if v.Notified != nil {
		if notified, err = json.Marshal(v.Notified); err != nil {
			return false, fmt.Errorf("upsert LADO verification: %w", err)
		}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return false, err
	}
	defer s.writeMu.Unlock()
	changed := false
	err = s.inTx(ctx, "upsert LADO verification", func(q *gen.Queries) error {
		stored, err := q.GetLadoVerification(ctx, gen.GetLadoVerificationParams{ExecutionSessionID: string(v.ExecutionSessionID), HeadSha: v.HeadSHA})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			changed = true
			if notified == nil {
				notified = []byte("[]")
			}
		case err != nil:
			return err
		default:
			changed = stored.Outcome != string(outcome) || stored.Reason != reason || stored.Sha256 != sum
			if notified == nil {
				notified = []byte(stored.NotifiedJson)
			}
		}
		return q.UpsertLadoVerification(ctx, gen.UpsertLadoVerificationParams{
			ExecutionSessionID: string(v.ExecutionSessionID),
			HeadSha:            v.HeadSHA,
			PRURL:              v.PRURL,
			Outcome:            string(outcome),
			Reason:             reason,
			ContentJson:        string(content),
			Sha256:             sum,
			NotifiedJson:       string(notified),
			ComputedAt:         v.ComputedAt.UTC(),
		})
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// GetLadoVerification returns the record of an execution's head with its
// items, its notified list and when it was computed. It reports false while
// the head has none.
func (s *Store) GetLadoVerification(ctx context.Context, session domain.SessionID, headSHA string) (domain.LadoVerification, bool, error) {
	row, err := s.qr.GetLadoVerification(ctx, gen.GetLadoVerificationParams{ExecutionSessionID: string(session), HeadSha: headSHA})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoVerification{}, false, nil
	}
	if err != nil {
		return domain.LadoVerification{}, false, fmt.Errorf("read LADO verification: %w", err)
	}
	// The content is the record's canonical JSON; its items decode as they
	// were hashed.
	var content struct {
		Items []domain.LadoVerifyItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(row.ContentJson), &content); err != nil {
		return domain.LadoVerification{}, false, fmt.Errorf("decode LADO verification: %w", err)
	}
	notified := []string{}
	if err := json.Unmarshal([]byte(row.NotifiedJson), &notified); err != nil {
		return domain.LadoVerification{}, false, fmt.Errorf("decode LADO verification notified list: %w", err)
	}
	return domain.LadoVerification{
		ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
		HeadSHA:            row.HeadSha,
		PRURL:              row.PRURL,
		Items:              content.Items,
		Notified:           notified,
		ComputedAt:         row.ComputedAt.UTC(),
	}, true, nil
}

// AddLadoExecutionReview records a review run LADO started for an
// execution's head as the execution's next round, from 1 to
// domain.LadoReviewRoundCap. A round that is not the next one is refused.
func (s *Store) AddLadoExecutionReview(ctx context.Context, r domain.LadoExecutionReview) error {
	if r.ReviewRunID == "" || r.ExecutionSessionID == "" || r.CreatedAt.IsZero() {
		return errors.New("add LADO execution review: a run, a session and a time are required")
	}
	if r.Round < 1 || r.Round > domain.LadoReviewRoundCap {
		return fmt.Errorf("add LADO execution review: round %d is not 1 to %d", r.Round, domain.LadoReviewRoundCap)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "add LADO execution review", func(q *gen.Queries) error {
		rounds, err := q.CountLadoExecutionReviews(ctx, string(r.ExecutionSessionID))
		if err != nil {
			return err
		}
		if int64(r.Round) != rounds+1 {
			return fmt.Errorf("round %d is not the next one: the execution has %d", r.Round, rounds)
		}
		return q.InsertLadoExecutionReview(ctx, gen.InsertLadoExecutionReviewParams{
			ReviewRunID:        r.ReviewRunID,
			ExecutionSessionID: string(r.ExecutionSessionID),
			HeadSha:            r.HeadSHA,
			Round:              int64(r.Round),
			CreatedAt:          r.CreatedAt.UTC(),
		})
	})
}

// ListLadoExecutionReviews returns an execution's review rounds in order.
func (s *Store) ListLadoExecutionReviews(ctx context.Context, session domain.SessionID) ([]domain.LadoExecutionReview, error) {
	rows, err := s.qr.ListLadoExecutionReviews(ctx, string(session))
	if err != nil {
		return nil, fmt.Errorf("list LADO execution reviews: %w", err)
	}
	reviews := make([]domain.LadoExecutionReview, 0, len(rows))
	for _, row := range rows {
		reviews = append(reviews, ladoExecutionReviewFromGen(row))
	}
	return reviews, nil
}

// MarkLadoExecutionReviewCancelled records that LADO cancelled a review run
// that stalled, at at; a run marked before keeps its first time. The run
// still counts as a round.
func (s *Store) MarkLadoExecutionReviewCancelled(ctx context.Context, runID string, at time.Time) error {
	if at.IsZero() {
		return errors.New("mark LADO execution review cancelled: a time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.MarkLadoExecutionReviewCancelled(ctx, gen.MarkLadoExecutionReviewCancelledParams{CancelledAt: ladoNullTime(at), ReviewRunID: runID})
	if err != nil {
		return fmt.Errorf("mark LADO execution review cancelled: %w", err)
	}
	if n > 0 {
		return nil
	}
	if _, err := s.qw.GetLadoExecutionReview(ctx, runID); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("mark LADO execution review cancelled: no review run %s", runID)
	} else if err != nil {
		return fmt.Errorf("mark LADO execution review cancelled: %w", err)
	}
	return nil
}

// ReplaceLadoEvidenceLinks replaces the evidence links of an execution's head
// with links in one transaction; the links of other heads stay. A link
// without a session or head takes the call's; one that names others is
// refused.
func (s *Store) ReplaceLadoEvidenceLinks(ctx context.Context, session domain.SessionID, headSHA string, links []domain.LadoEvidenceLink) error {
	if session == "" || headSHA == "" {
		return errors.New("replace LADO evidence links: a session and a head are required")
	}
	for _, link := range links {
		if (link.ExecutionSessionID != "" && link.ExecutionSessionID != session) || (link.HeadSHA != "" && link.HeadSHA != headSHA) {
			return fmt.Errorf("replace LADO evidence links: link %s belongs to %s at %s", link.ID, link.ExecutionSessionID, link.HeadSHA)
		}
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "replace LADO evidence links", func(q *gen.Queries) error {
		if err := q.DeleteLadoEvidenceLinksForHead(ctx, gen.DeleteLadoEvidenceLinksForHeadParams{ExecutionSessionID: string(session), HeadSha: headSHA}); err != nil {
			return err
		}
		for _, link := range links {
			if err := q.InsertLadoEvidenceLink(ctx, gen.InsertLadoEvidenceLinkParams{
				ID:                 link.ID,
				ExecutionSessionID: string(session),
				HeadSha:            headSHA,
				Criterion:          int64(link.Criterion),
				State:              string(link.State),
				RefKind:            string(link.RefKind),
				RefKey:             link.RefKey,
				Sha:                link.SHA,
				CreatedAt:          link.CreatedAt.UTC(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListLadoEvidenceLinks returns the evidence links of an execution's head by
// criterion.
func (s *Store) ListLadoEvidenceLinks(ctx context.Context, session domain.SessionID, headSHA string) ([]domain.LadoEvidenceLink, error) {
	rows, err := s.qr.ListLadoEvidenceLinksForHead(ctx, gen.ListLadoEvidenceLinksForHeadParams{ExecutionSessionID: string(session), HeadSha: headSHA})
	if err != nil {
		return nil, fmt.Errorf("list LADO evidence links: %w", err)
	}
	links := make([]domain.LadoEvidenceLink, 0, len(rows))
	for _, row := range rows {
		links = append(links, domain.LadoEvidenceLink{
			ID:                 row.ID,
			ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
			HeadSHA:            row.HeadSha,
			Criterion:          int(row.Criterion),
			State:              domain.LadoEvidenceState(row.State),
			RefKind:            domain.LadoEvidenceRefKind(row.RefKind),
			RefKey:             row.RefKey,
			SHA:                row.Sha,
			CreatedAt:          row.CreatedAt.UTC(),
		})
	}
	return links, nil
}

// ListLadoCommitChecks returns the CI checks AO recorded for one commit of a
// PR, by name.
func (s *Store) ListLadoCommitChecks(ctx context.Context, prURL, commit string) ([]domain.PullRequestCheck, error) {
	rows, err := s.qr.ListLadoCommitChecks(ctx, gen.ListLadoCommitChecksParams{PRURL: prURL, CommitHash: commit})
	if err != nil {
		return nil, fmt.Errorf("list LADO commit checks: %w", err)
	}
	checks := make([]domain.PullRequestCheck, 0, len(rows))
	for _, row := range rows {
		checks = append(checks, checkRowFromGen(row))
	}
	return checks, nil
}

// ListLadoExecutionsToVerify returns up to limit executions for the
// verification loop, oldest first: those whose session's newest PR is open,
// and those whose newest PR merged or closed and that are not settled yet.
// Each carries that PR, whether its session ended and the verification facts
// of the PR's head, as task reads give them.
func (s *Store) ListLadoExecutionsToVerify(ctx context.Context, limit int) ([]domain.LadoExecution, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.qr.ListLadoExecutionsToVerify(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list LADO executions to verify: %w", err)
	}
	executions := make([]domain.LadoExecution, 0, len(rows))
	for _, row := range rows {
		execution := ladoExecutionFromGen(row.LadoExecution)
		execution.SessionTerminated = row.SessionTerminated
		execution.PR = ladoExecutionPR(row.PRURL, row.PRNumber, row.PRState, row.PRHeadSha, row.PRTargetBranch, row.PRTitle)
		executions = append(executions, execution)
	}
	if err := s.fillLadoVerification(ctx, executions); err != nil {
		return nil, err
	}
	return executions, nil
}

// SettleLadoExecution records that the loop settled the execution's merged or
// closed PR at at, with audit in the same transaction when it is given. An
// execution settled before, and a session that is not an execution, write
// nothing, so the audit event is appended once.
func (s *Store) SettleLadoExecution(ctx context.Context, session domain.SessionID, at time.Time, audit *domain.LadoAuditEvent) error {
	if at.IsZero() {
		return errors.New("settle LADO execution: a time is required")
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "settle LADO execution", func(q *gen.Queries) error {
		n, err := q.SettleLadoExecution(ctx, gen.SettleLadoExecutionParams{SettledAt: ladoNullTime(at), SessionID: string(session)})
		if err != nil || n == 0 || audit == nil {
			return err
		}
		_, err = appendLadoAuditEventTx(ctx, q, *audit)
		return err
	})
}

// ApproveLadoIntegration records the integration approval a, the presence
// proof it used and its audit event in one transaction (docs/LJY-PRD/TDD.md
// §7.1); the approval takes the proof's nonce. A proof used before is refused
// with domain.ErrLadoPresenceReplayed, a head that has an approval already
// with domain.ErrLadoIntegrationAlreadyApproved, and an approval whose
// verification sha256 is not the one of the head's stored record with
// domain.ErrLadoVerificationChanged: the caller stores the record it
// recomputed first. When any part fails, nothing is kept.
func (s *Store) ApproveLadoIntegration(ctx context.Context, a domain.LadoIntegrationApproval, proof domain.LadoPresenceProof, audit domain.LadoAuditEvent) error {
	if a.ID == "" || a.ExecutionSessionID == "" || a.HeadSHA == "" || a.ApprovedAt.IsZero() {
		return errors.New("approve LADO integration: an id, a session, a head and a time are required")
	}
	acknowledged, err := json.Marshal(append([]string{}, a.Acknowledged...))
	if err != nil {
		return fmt.Errorf("approve LADO integration: %w", err)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	err = s.inTx(ctx, "approve LADO integration", func(q *gen.Queries) error {
		if err := insertLadoPresenceProof(ctx, q, proof); err != nil {
			return err
		}
		head := gen.GetLadoIntegrationApprovalForHeadParams{ExecutionSessionID: string(a.ExecutionSessionID), HeadSha: a.HeadSHA}
		_, err := q.GetLadoIntegrationApprovalForHead(ctx, head)
		switch {
		case err == nil:
			return domain.ErrLadoIntegrationAlreadyApproved
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		record, err := q.GetLadoVerification(ctx, gen.GetLadoVerificationParams{ExecutionSessionID: string(a.ExecutionSessionID), HeadSha: a.HeadSHA})
		if errors.Is(err, sql.ErrNoRows) || (err == nil && record.Sha256 != a.VerificationSHA256) {
			return domain.ErrLadoVerificationChanged
		}
		if err != nil {
			return err
		}
		err = q.InsertLadoIntegrationApproval(ctx, gen.InsertLadoIntegrationApprovalParams{
			ID:                 a.ID,
			ExecutionSessionID: string(a.ExecutionSessionID),
			PRURL:              a.PRURL,
			HeadSha:            a.HeadSHA,
			VerificationSha256: a.VerificationSHA256,
			AcknowledgedJson:   string(acknowledged),
			ProofNonce:         proof.Nonce,
			Actor:              a.Actor,
			ApprovedAt:         a.ApprovedAt.UTC(),
			StatusState:        string(a.StatusState),
			StatusAttempts:     int64(a.StatusAttempts),
			StatusPostedAt:     ladoNullTime(a.StatusPostedAt),
			StatusError:        a.StatusError,
		})
		if isSQLiteUnique(err) {
			// The proof's nonce is new (inserted above), so the head's pair is
			// what repeats.
			return domain.ErrLadoIntegrationAlreadyApproved
		}
		if err != nil {
			return err
		}
		_, err = appendLadoAuditEventTx(ctx, q, audit)
		return err
	})
	return unwrapLadoSentinel(err)
}

// SetLadoIntegrationStatus records how posting an approval's commit status
// went: its state, the attempts so far, when it was posted (zero while it was
// not) and the last error, without secrets.
func (s *Store) SetLadoIntegrationStatus(ctx context.Context, id string, state domain.LadoIntegrationStatusState, attempts int, postedAt time.Time, errorMessage string) error {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	n, err := s.qw.SetLadoIntegrationStatus(ctx, gen.SetLadoIntegrationStatusParams{
		StatusState:    string(state),
		StatusAttempts: int64(attempts),
		StatusPostedAt: ladoNullTime(postedAt),
		StatusError:    errorMessage,
		ID:             id,
	})
	if err != nil {
		return fmt.Errorf("set LADO integration status: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("set LADO integration status: no integration approval %s", id)
	}
	return nil
}

// GetLadoIntegrationApproval returns the integration approval of an
// execution's head. It reports false while the head has none.
func (s *Store) GetLadoIntegrationApproval(ctx context.Context, session domain.SessionID, headSHA string) (domain.LadoIntegrationApproval, bool, error) {
	row, err := s.qr.GetLadoIntegrationApprovalForHead(ctx, gen.GetLadoIntegrationApprovalForHeadParams{ExecutionSessionID: string(session), HeadSha: headSHA})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LadoIntegrationApproval{}, false, nil
	}
	if err != nil {
		return domain.LadoIntegrationApproval{}, false, fmt.Errorf("read LADO integration approval: %w", err)
	}
	approval, err := ladoIntegrationApprovalFromGen(row)
	if err != nil {
		return domain.LadoIntegrationApproval{}, false, err
	}
	return approval, true, nil
}

// ListLadoIntegrationStatusRetries returns up to limit approvals whose commit
// status is still pending or failed, with fewer than
// domain.LadoIntegrationStatusMaxAttempts attempts, oldest first.
func (s *Store) ListLadoIntegrationStatusRetries(ctx context.Context, limit int) ([]domain.LadoIntegrationApproval, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.qr.ListLadoIntegrationStatusRetries(ctx, gen.ListLadoIntegrationStatusRetriesParams{
		MaxAttempts:  domain.LadoIntegrationStatusMaxAttempts,
		MaxApprovals: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list LADO integration status retries: %w", err)
	}
	approvals := make([]domain.LadoIntegrationApproval, 0, len(rows))
	for _, row := range rows {
		approval, err := ladoIntegrationApprovalFromGen(row)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	return approvals, nil
}

func ladoCheckRunFromGen(row gen.LadoCheckRun) domain.LadoCheckRun {
	var exit *int
	if row.ExitCode.Valid {
		n := int(row.ExitCode.Int64)
		exit = &n
	}
	return domain.LadoCheckRun{
		ID:                 row.ID,
		ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
		HeadSHA:            row.HeadSha,
		Name:               row.Name,
		Command:            row.Command,
		Status:             domain.LadoCheckRunStatus(row.Status),
		ExitCode:           exit,
		LogSHA256:          row.LogSha256,
		ErrorMessage:       row.ErrorMessage,
		StartedAt:          row.StartedAt.UTC(),
		FinishedAt:         ladoTimeFromNull(row.FinishedAt),
	}
}

func ladoExecutionReviewFromGen(row gen.LadoExecutionReview) domain.LadoExecutionReview {
	return domain.LadoExecutionReview{
		ReviewRunID:        row.ReviewRunID,
		ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
		HeadSHA:            row.HeadSha,
		Round:              int(row.Round),
		CreatedAt:          row.CreatedAt.UTC(),
		CancelledAt:        ladoTimeFromNull(row.CancelledAt),
	}
}

func ladoIntegrationApprovalFromGen(row gen.LadoIntegrationApproval) (domain.LadoIntegrationApproval, error) {
	acknowledged := []string{}
	if err := json.Unmarshal([]byte(row.AcknowledgedJson), &acknowledged); err != nil {
		return domain.LadoIntegrationApproval{}, fmt.Errorf("decode LADO integration approval %s: %w", row.ID, err)
	}
	return domain.LadoIntegrationApproval{
		ID:                 row.ID,
		ExecutionSessionID: domain.SessionID(row.ExecutionSessionID),
		PRURL:              row.PRURL,
		HeadSHA:            row.HeadSha,
		VerificationSHA256: row.VerificationSha256,
		Acknowledged:       acknowledged,
		ProofNonce:         row.ProofNonce,
		Actor:              row.Actor,
		ApprovedAt:         row.ApprovedAt.UTC(),
		StatusState:        domain.LadoIntegrationStatusState(row.StatusState),
		StatusAttempts:     int(row.StatusAttempts),
		StatusPostedAt:     ladoTimeFromNull(row.StatusPostedAt),
		StatusError:        row.StatusError,
	}, nil
}
