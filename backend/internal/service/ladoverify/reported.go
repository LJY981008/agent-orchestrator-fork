package ladoverify

// LADO fork (#71): a worker that opened its PR from a branch other than its
// session's leaves the PR on no session, so the execution never has a PR.
// Before a pass pins PRs (trackPRs), it attaches the PRs such workers
// reported (`ao report --pr-created`) to their sessions as
// `ao session claim-pr` does, never taking over another session's PR. One
// it finds it can never attach is stored as refused, so it is not tried
// again and no longer holds an integration approval; one that fails for now
// is tried again after failedRetryAfter, and refused as unreachable once it
// failed reportedClaimMaxFailures times in a row.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// reportedClaimsPerPass caps the claims one pass makes, and
	// reportedClaimTimeout each claim, so a slow host never holds a pass
	// for more than two minutes.
	reportedClaimsPerPass = 4
	reportedClaimTimeout  = 30 * time.Second
	// reportedClaimMaxFailures is how many failures in a row refuse a
	// reported PR as unreachable.
	reportedClaimMaxFailures = 5
	// ladoAuditReportedPRClaimed is the audit action of a reported PR LADO
	// attached to its execution's session.
	ladoAuditReportedPRClaimed = "execution.reported_pr_claimed"
)

// Claims attaches a PR a worker reported to the worker's session, as
// `ao session claim-pr` does, and never takes over a PR another session
// has. An error that can never pass is a *ReportedPRRefusal; any other
// error may pass later.
type Claims interface {
	ClaimReportedPR(ctx context.Context, session domain.SessionID, prURL string) error
}

// ReportedPRRefusal says a reported PR can never be attached to the
// session, and why: Reason is one of domain.LadoReportedPRRefusalReasons.
type ReportedPRRefusal struct {
	Reason string
	Err    error
}

func (e *ReportedPRRefusal) Error() string {
	if e.Err == nil {
		return "the reported pull request cannot be attached: " + e.Reason
	}
	return fmt.Sprintf("the reported pull request cannot be attached (%s): %v", e.Reason, e.Err)
}

func (e *ReportedPRRefusal) Unwrap() error { return e.Err }

// reportedFailure counts the failures in a row of one reported PR's claim
// and holds when the last one was.
type reportedFailure struct {
	count int
	at    time.Time
}

// reportedKey names a reported PR of an execution.
func reportedKey(session domain.SessionID, prURL string) string {
	return string(session) + "\x00" + prURL
}

// claimReportedPRs attaches the reported PRs LADO still tries to attach
// (domain.LadoExecution.PendingReportedPRs) of up to trackBatch executions,
// oldest first, making at most reportedClaimsPerPass claims. A PR whose
// last claim failed for now rests failedRetryAfter. Without Claims it does
// nothing. It returns what it could not read or store; a failed claim is
// logged.
func (s *Service) claimReportedPRs(ctx context.Context) error {
	if s.deps.Claims == nil {
		return nil
	}
	executions, err := s.deps.Store.ListLadoExecutionsWithReportedPRs(ctx, trackBatch)
	if err != nil {
		return fmt.Errorf("list the executions with reported pull requests: %w", err)
	}
	listed := map[string]bool{}
	for _, exec := range executions {
		for _, pr := range exec.PendingReportedPRs() {
			listed[reportedKey(exec.SessionID, pr.URL)] = true
		}
	}
	now := s.now()
	s.mu.Lock()
	if len(executions) < trackBatch {
		// Every execution LADO still tries is listed: forget the others.
		for key := range s.reportedFailed {
			if !listed[key] {
				delete(s.reportedFailed, key)
			}
		}
	}
	resting := map[string]bool{}
	for key, failure := range s.reportedFailed {
		if now.Sub(failure.at) < failedRetryAfter {
			resting[key] = true
		}
	}
	s.mu.Unlock()

	var problems []error
	claims, claimed := 0, false
	for _, exec := range executions {
		for _, pr := range exec.PendingReportedPRs() {
			if ctx.Err() != nil || claims == reportedClaimsPerPass {
				break
			}
			key := reportedKey(exec.SessionID, pr.URL)
			if resting[key] {
				continue
			}
			claims++
			ok, err := s.claimReportedPR(ctx, exec, pr.URL)
			if err != nil {
				problems = append(problems, err)
			}
			claimed = claimed || ok
		}
	}
	if claimed {
		s.Kick()
	}
	return errors.Join(problems...)
}

// claimReportedPR claims one reported PR of the execution and reports
// whether it was attached. A refusal is stored and logged once; a failure
// for now is counted, and the count reaching reportedClaimMaxFailures is
// stored as an unreachable refusal. It returns what it could not store, or
// the error of a pass that stopped.
func (s *Service) claimReportedPR(ctx context.Context, exec domain.LadoExecution, prURL string) (bool, error) {
	key := reportedKey(exec.SessionID, prURL)
	claimCtx, cancel := context.WithTimeout(ctx, reportedClaimTimeout)
	err := s.deps.Claims.ClaimReportedPR(claimCtx, exec.SessionID, prURL)
	cancel()
	if err == nil {
		s.mu.Lock()
		delete(s.reportedFailed, key)
		s.mu.Unlock()
		s.deps.Log.Info("LADO attached a pull request the worker reported to its session",
			"session", exec.SessionID, "task", exec.TaskID, "pr", prURL)
		s.auditReportedClaim(ctx, exec, prURL)
		return true, nil
	}
	if ctx.Err() != nil {
		// The pass stopped; the claim did not fail, so it is not counted.
		return false, ctx.Err()
	}
	now := s.now()
	var reason string
	var refusal *ReportedPRRefusal
	if errors.As(err, &refusal) && slices.Contains(domain.LadoReportedPRRefusalReasons, refusal.Reason) {
		reason = refusal.Reason
	} else {
		s.mu.Lock()
		failure := s.reportedFailed[key]
		failure.count++
		failure.at = now
		s.reportedFailed[key] = failure
		s.mu.Unlock()
		if failure.count < reportedClaimMaxFailures {
			s.deps.Log.Info("LADO could not attach a pull request the worker reported to its session yet; it tries again",
				"session", exec.SessionID, "pr", prURL, "failures", failure.count, "err", redactedErrorLine(err))
			return false, nil
		}
		reason = domain.LadoReportedPRUnreachable
	}
	if err := s.deps.Store.RefuseLadoReportedPR(ctx, exec.SessionID, prURL, reason, now); err != nil {
		return false, fmt.Errorf("record that execution %s cannot take reported pull request %s: %w", exec.SessionID, prURL, err)
	}
	s.mu.Lock()
	delete(s.reportedFailed, key)
	s.mu.Unlock()
	s.deps.Log.Warn("LADO cannot attach a pull request the worker reported to its session; attach it with `ao session claim-pr` if it is the task's",
		"session", exec.SessionID, "task", exec.TaskID, "pr", prURL, "reason", reason, "err", redactedErrorLine(err))
	return false, nil
}

// auditReportedClaim records that LADO attached a reported PR to the
// execution's session. What fails is logged: the PR is attached.
func (s *Service) auditReportedClaim(ctx context.Context, exec domain.LadoExecution, prURL string) {
	payload, err := json.Marshal(map[string]any{"task": exec.TaskID, "session": exec.SessionID, "pr": prURL})
	if err == nil {
		_, err = s.deps.Store.AppendLadoAuditEvent(context.WithoutCancel(ctx), domain.LadoAuditEvent{
			At: s.now(), Actor: "lado", Action: ladoAuditReportedPRClaimed, SubjectKind: "execution", SubjectID: string(exec.SessionID), Payload: payload,
		})
	}
	if err != nil {
		s.deps.Log.Error("LADO could not audit a reported pull request it attached", "session", exec.SessionID, "pr", prURL, "err", err)
	}
}
