package store

// LADO fork: PRs a worker reported it created that its session does not have
// (issue #71). Execution reads carry them as ReportedPRs; LADO's verification
// loop lists the executions it still tries to attach one to and records the
// ones it cannot (lado_reported_pr_refusals, LADO migration 0016).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ladoReportedRepo names a repository of a PR, case aside.
type ladoReportedRepo struct {
	host, repository string
}

func ladoReportedRepoOf(ref domain.ChangeRequestReference) ladoReportedRepo {
	return ladoReportedRepo{strings.ToLower(ref.Host), strings.ToLower(ref.Repository)}
}

// fillLadoReportedPRs sets each single or integration worker's execution's
// ReportedPRs from its session's pr_created reports, given the PRs of the
// sessions (sessionPRs, as ListLadoExecutionPRs reads them). A reported PR
// counts once, in the order it was first reported, and not when the
// session has it (the same address or the same PR by its reference key) or
// has an open PR in its repository (host and repository, case aside): LADO
// tracks one PR per repository. One whose PR row, found by its address or
// an alias as claiming a PR finds it, belongs to another session, ended or
// not, is Refused as owned elsewhere: LADO never moves another session's PR.
// One LADO refused carries the stored reason.
func (s *Store) fillLadoReportedPRs(ctx context.Context, executions []domain.LadoExecution, sessionPRs []gen.ListLadoExecutionPRsRow) error {
	var ids []string
	for i := range executions {
		executions[i].ReportedPRs = nil
		if role := executions[i].Role; role == domain.LadoRoleSingle || role == domain.LadoRoleIntegration {
			ids = append(ids, string(executions[i].SessionID))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	reports, err := s.qr.ListLadoSessionReportedPRs(ctx, ids)
	if err != nil {
		return fmt.Errorf("read LADO reported PRs: %w", err)
	}
	if len(reports) == 0 {
		return nil
	}
	reported := map[domain.SessionID][]string{}
	var reporting []string
	for _, row := range reports {
		session := domain.SessionID(row.SessionID)
		if _, ok := reported[session]; !ok {
			reporting = append(reporting, row.SessionID)
		}
		reported[session] = append(reported[session], row.Reference)
	}
	refusalRows, err := s.qr.ListLadoReportedPRRefusals(ctx, reporting)
	if err != nil {
		return fmt.Errorf("read LADO reported PR refusals: %w", err)
	}
	refused := make(map[ladoPRKey]string, len(refusalRows))
	for _, row := range refusalRows {
		refused[ladoPRKey{domain.SessionID(row.ExecutionSessionID), row.PRURL}] = row.Reason
	}
	type sessionFacts struct {
		urls      map[string]bool
		keys      map[string]bool
		openRepos map[ladoReportedRepo]bool
	}
	facts := map[domain.SessionID]*sessionFacts{}
	for _, row := range sessionPRs {
		if _, ok := reported[row.SessionID]; !ok {
			continue
		}
		f := facts[row.SessionID]
		if f == nil {
			f = &sessionFacts{urls: map[string]bool{}, keys: map[string]bool{}, openRepos: map[ladoReportedRepo]bool{}}
			facts[row.SessionID] = f
		}
		f.urls[row.URL] = true
		ref, err := domain.ParseChangeRequestURL(row.URL)
		if err != nil {
			continue
		}
		f.keys[ref.Key()] = true
		if row.PRState != domain.PRStateMerged && row.PRState != domain.PRStateClosed {
			f.openRepos[ladoReportedRepoOf(ref)] = true
		}
	}
	for i := range executions {
		execution := &executions[i]
		references := reported[execution.SessionID]
		if len(references) == 0 {
			continue
		}
		f := facts[execution.SessionID]
		if f == nil {
			f = &sessionFacts{}
		}
		seen := map[string]bool{}
		for _, reference := range references {
			ref, refErr := domain.ParseChangeRequestURL(reference)
			parsed := refErr == nil
			id := reference
			if parsed {
				id = ref.Key()
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			if f.urls[reference] || (parsed && (f.keys[ref.Key()] || f.openRepos[ladoReportedRepoOf(ref)])) {
				continue
			}
			owner, owned, err := s.ladoReportedPROwner(ctx, reference, ref, parsed)
			if err != nil {
				return err
			}
			switch {
			case owned && owner == execution.SessionID:
				// The session has it under another address.
				continue
			case owned:
				execution.ReportedPRs = append(execution.ReportedPRs, domain.LadoReportedPR{
					URL: reference, Refused: true, Reason: domain.LadoReportedPROwnedElsewhere,
				})
				continue
			}
			pr := domain.LadoReportedPR{URL: reference}
			if reason, ok := refused[ladoPRKey{execution.SessionID, reference}]; ok {
				pr.Refused, pr.Reason = true, reason
			}
			execution.ReportedPRs = append(execution.ReportedPRs, pr)
		}
	}
	return nil
}

// ladoReportedPROwner returns the session whose PR row the reported
// reference names, by its address or an alias of it and then by its
// normalized address, as claiming a PR resolves a weak alias. It reports
// false when no PR row has it.
func (s *Store) ladoReportedPROwner(ctx context.Context, reference string, ref domain.ChangeRequestReference, parsed bool) (domain.SessionID, bool, error) {
	addresses := []string{reference}
	if parsed && ref.URL != reference {
		addresses = append(addresses, ref.URL)
	}
	for _, address := range addresses {
		row, err := s.qr.GetPRByURLOrAlias(ctx, address)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("read the PR of reported %s: %w", reference, err)
		}
		return row.SessionID, true, nil
	}
	return "", false, nil
}

// ListLadoExecutionsWithReportedPRs returns up to limit executions, oldest
// first, that LADO still tries to attach a reported PR to (#71): a single
// or integration worker's, not settled, whose session runs and has a
// PendingReportedPRs entry. They carry every execution fact, as
// LadoExecution reads them.
func (s *Store) ListLadoExecutionsWithReportedPRs(ctx context.Context, limit int) ([]domain.LadoExecution, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.qr.ListLadoExecutionsWithReportedPRs(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list LADO executions with reported PRs: %w", err)
	}
	executions := make([]domain.LadoExecution, 0, len(rows))
	for _, row := range rows {
		// The query lists only executions whose session runs.
		executions = append(executions, ladoExecutionFromGen(row.LadoExecution))
	}
	if err := s.fillLadoExecutionFacts(ctx, executions); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(executions, func(e domain.LadoExecution) bool { return len(e.PendingReportedPRs()) == 0 }), nil
}

// RefuseLadoReportedPR records at at that LADO does not attach the PR the
// execution of session reported at prURL to the session, for reason (one of
// domain.LadoReportedPRRefusalReasons). A second refusal of the PR keeps the
// newer reason and time. A session that is not an execution is an error.
func (s *Store) RefuseLadoReportedPR(ctx context.Context, session domain.SessionID, prURL, reason string, at time.Time) error {
	if session == "" || prURL == "" || at.IsZero() {
		return errors.New("refuse LADO reported PR: a session, a PR and a time are required")
	}
	if !slices.Contains(domain.LadoReportedPRRefusalReasons, reason) {
		return fmt.Errorf("refuse LADO reported PR: unknown reason %q", reason)
	}
	if err := s.writeMu.LockContext(ctx); err != nil {
		return err
	}
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "refuse LADO reported PR", func(q *gen.Queries) error {
		if _, err := q.GetLadoExecution(ctx, string(session)); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session %s is not a LADO execution", session)
		} else if err != nil {
			return err
		}
		return q.UpsertLadoReportedPRRefusal(ctx, gen.UpsertLadoReportedPRRefusalParams{
			ExecutionSessionID: string(session),
			PRURL:              prURL,
			Reason:             reason,
			RefusedAt:          at.UTC(),
		})
	})
}
