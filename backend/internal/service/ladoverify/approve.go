package ladoverify

// LADO fork: the integration approval (decision m of the step 8 plan;
// docs/LJY-PRD/TDD.md §7.1 and §13). In the desktop app the developer
// approves the integration of one head of an execution's PR: the approval
// signs the hash of the verification record the developer saw and
// acknowledges the codes of its items that needed confirming. Right before it
// is recorded, LADO reads again what could have moved since: the PR's head on
// its host, the base branch on its remote and the base branch's rules, and
// computes the head's record again. A dry run makes the same reads and
// answers the record, without a proof and without recording an approval.
// Once an approval is recorded, its lado/integration status goes to GitHub,
// where a ruleset that requires the status turns it into the permission to
// merge. LADO never merges.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// ApproveInput is an integration approval or, with DryRun, its preview.
type ApproveInput struct {
	SessionID domain.SessionID
	// PRURL and HeadSHA are the PR and the head of it the developer approves.
	PRURL, HeadSHA string
	// VerificationSHA256 is the hash of the record the developer saw, and
	// Acknowledge the codes of its items that need confirming, which the
	// developer confirmed. A dry run reads neither.
	VerificationSHA256 string
	Acknowledge        []string
	DryRun             bool
	// Presence checks the request's human-presence proof for an action. A
	// dry run does not call it.
	Presence func(action, subject string) (domain.LadoPresenceProof, error)
}

// ApproveResult is the head's record as the approval computed it, its hash,
// the codes an approval acknowledges and, once recorded, the approval with
// how posting its status went (nil for a dry run).
type ApproveResult struct {
	Verification domain.LadoVerification
	SHA256       string
	Confirm      []string
	Approval     *domain.LadoIntegrationApproval
}

// ApproveIntegration approves the integration of the head of an execution's
// PR or, with in.DryRun, previews it. An approval checks its presence proof
// before it reads anything else. Both then read the execution's open PR and
// its head on the host, fetch the base branch, read the base branch's rules
// past the cache and compute and store the head's record again. A dry run
// answers the record whatever its outcome. An approval needs the record's
// hash to be the one signed, no item to wait or fail, the acknowledged codes
// to be the record's confirm codes and no approval of the head yet; it is
// recorded with its proof and audit event in one transaction, and its
// lado/integration status is posted once right away: a post that fails
// leaves the approval, its status failed, for the loop to post again. Every
// refusal is an *apierr.Error.
func (s *Service) ApproveIntegration(ctx context.Context, in ApproveInput) (ApproveResult, error) {
	head := strings.ToLower(strings.TrimSpace(in.HeadSHA))
	var proof domain.LadoPresenceProof
	if !in.DryRun {
		var err error
		if proof, err = s.integrationPresence(ctx, in.SessionID, head, in.Presence); err != nil {
			return ApproveResult{}, err
		}
	}
	exec, err := s.integrationExecution(ctx, in, head)
	if err != nil {
		return ApproveResult{}, err
	}
	exec.PR.HeadSHA = head
	record, err := s.verifyHead(ctx, exec, verifyOptions{forceProtection: true, fetchBase: true})
	if err != nil {
		s.deps.Log.Error("LADO could not verify a head for its integration approval", "session", exec.SessionID, "head", head, "err", err)
		return ApproveResult{}, verifyFailed()
	}
	sum, err := record.SHA256()
	if err != nil {
		s.deps.Log.Error("LADO could not hash a verification record", "session", exec.SessionID, "head", head, "err", err)
		return ApproveResult{}, verifyFailed()
	}
	result := ApproveResult{Verification: record, SHA256: sum, Confirm: record.ConfirmCodes()}
	if in.DryRun {
		return result, nil
	}
	if err := s.approvable(ctx, in, record, sum, result.Confirm); err != nil {
		return ApproveResult{}, err
	}
	approval, err := s.recordApproval(ctx, exec, record, sum, result.Confirm, proof)
	if err != nil {
		return ApproveResult{}, err
	}
	result.Approval = &approval
	s.Kick()
	return result, nil
}

// integrationPresence takes the desktop app's confirmation of the approval,
// with <session>@<head> as its subject, and refuses a proof used before.
func (s *Service) integrationPresence(ctx context.Context, session domain.SessionID, head string, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoPresenceProof, error) {
	if presence == nil {
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Approve the integration in the AO desktop app. It asks you to confirm, then sends the approval itself.")
	}
	proof, err := presence(domain.LadoPresenceIntegrationApprove, string(session)+"@"+head)
	if err != nil {
		return domain.LadoPresenceProof{}, err
	}
	// The store refuses a reused proof when it records the approval; this
	// refuses it before LADO reads or fetches anything.
	used, err := s.deps.Store.LadoPresenceProofUsed(ctx, proof.Nonce)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a presence proof", "err", err)
		return domain.LadoPresenceProof{}, verifyFailed()
	case used:
		return domain.LadoPresenceProof{}, ladopresence.Replayed()
	}
	return proof, nil
}

// integrationExecution reads the execution and its open PR, and checks that
// head is the PR's head on its host and, for a dry run, the head AO observed,
// which the developer was shown.
func (s *Service) integrationExecution(ctx context.Context, in ApproveInput, head string) (domain.LadoExecution, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, in.SessionID)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read an execution for its integration approval", "session", in.SessionID, "err", err)
		return domain.LadoExecution{}, verifyFailed()
	case !ok:
		return domain.LadoExecution{}, apierr.NotFound("LADO_EXECUTION_NOT_FOUND", "This session is not a LADO execution")
	case exec.PR.URL == "" || exec.PR.Merged || exec.PR.Closed:
		return domain.LadoExecution{}, noOpenPR(in.SessionID)
	case in.PRURL != exec.PR.URL:
		return domain.LadoExecution{}, apierr.Conflict("LADO_INTEGRATION_HEAD_CHANGED",
			"The execution's pull request changed. Review the new one, then approve its integration.",
			map[string]any{"headSha": head, "currentHeadSha": strings.ToLower(exec.PR.HeadSHA), "prUrl": in.PRURL, "currentPrUrl": exec.PR.URL})
	case in.DryRun && !strings.EqualFold(head, exec.PR.HeadSHA):
		return domain.LadoExecution{}, headChanged(head, strings.ToLower(exec.PR.HeadSHA))
	}
	pr, ok, err := s.deps.Store.GetPR(ctx, exec.PR.URL)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a PR for its integration approval", "pr", exec.PR.URL, "err", err)
		return domain.LadoExecution{}, verifyFailed()
	case !ok:
		return domain.LadoExecution{}, noOpenPR(in.SessionID)
	}
	reader := s.liveHeadReader()
	if reader == nil {
		return domain.LadoExecution{}, apierr.Unavailable("LADO_VERIFY_UNAVAILABLE",
			"LADO cannot read pull requests from their host, so it cannot approve an integration now. Check AO's GitHub or GitLab connection.")
	}
	live, err := reader.LiveHead(ctx, pr)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a PR's head from its host", "session", exec.SessionID, "pr", exec.PR.URL, "err", redactedErrorLine(err))
		return domain.LadoExecution{}, apierr.Unavailable("LADO_VERIFY_UNAVAILABLE",
			"LADO could not read the pull request's head from its host. Try again.")
	}
	if live = strings.ToLower(strings.TrimSpace(live)); live != head {
		return domain.LadoExecution{}, headChanged(head, live)
	}
	return exec, nil
}

// approvable refuses to approve a record other than the one signed, a record
// with an item that waits or fails, acknowledgements other than the record's
// confirm codes, and a head approved already.
func (s *Service) approvable(ctx context.Context, in ApproveInput, record domain.LadoVerification, sum string, confirm []string) error {
	if !strings.EqualFold(strings.TrimSpace(in.VerificationSHA256), sum) {
		return verificationChanged()
	}
	if outcome, _ := record.Overall(); outcome == domain.LadoVerifyWait || outcome == domain.LadoVerifyFail {
		return apierr.Conflict("LADO_INTEGRATION_NOT_READY",
			"LADO still verifies this head, or some of its checks fail. Approve its integration once they pass.",
			map[string]any{"reasons": notReadyReasons(record.Items)})
	}
	if given := codeSet(in.Acknowledge); !slices.Equal(given, confirm) {
		return apierr.Invalid("LADO_INTEGRATION_ACK_MISMATCH",
			"Confirm exactly the items of this head that need confirming.",
			map[string]any{"required": confirm, "given": given})
	}
	_, approved, err := s.deps.Store.GetLadoIntegrationApproval(ctx, record.ExecutionSessionID, record.HeadSHA)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read the integration approvals of a head", "session", record.ExecutionSessionID, "head", record.HeadSHA, "err", err)
		return verifyFailed()
	case approved:
		return alreadyApproved()
	}
	return nil
}

// recordApproval records the approval with its proof and audit event, then
// posts its status once on GitHub. GitLab gets no status: its approvals are
// not_applicable.
func (s *Service) recordApproval(ctx context.Context, exec domain.LadoExecution, record domain.LadoVerification, sum string, acknowledged []string, proof domain.LadoPresenceProof) (domain.LadoIntegrationApproval, error) {
	task, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
	if err != nil || !ok {
		s.deps.Log.Error("LADO could not read the task of an integration approval", "task", exec.TaskID, "found", ok, "err", err)
		return domain.LadoIntegrationApproval{}, verifyFailed()
	}
	now := s.now()
	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	state := domain.LadoIntegrationStatusPending
	if task.Repo.Provider == domain.TrackerProviderGitLab {
		state = domain.LadoIntegrationStatusNotApplicable
	}
	approval := domain.LadoIntegrationApproval{
		ID:                 "lado-integration-" + s.deps.NewID(),
		ExecutionSessionID: exec.SessionID,
		PRURL:              record.PRURL,
		HeadSHA:            record.HeadSHA,
		VerificationSHA256: sum,
		Acknowledged:       acknowledged,
		ProofNonce:         proof.Nonce,
		Actor:              actor,
		ApprovedAt:         now,
		StatusState:        state,
	}
	payload, _ := json.Marshal(map[string]any{
		"task":         exec.TaskID,
		"session":      exec.SessionID,
		"pr":           approval.PRURL,
		"head":         approval.HeadSHA,
		"verification": sum,
		"acknowledged": acknowledged,
		"proof":        proof.Nonce,
		"dev":          proof.Dev,
	})
	err = s.deps.Store.ApproveLadoIntegration(ctx, approval, proof, domain.LadoAuditEvent{
		At:          now,
		Actor:       actor,
		Action:      "integration.approved",
		SubjectKind: "execution",
		SubjectID:   string(exec.SessionID),
		Payload:     payload,
	})
	switch {
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return domain.LadoIntegrationApproval{}, ladopresence.Replayed()
	case errors.Is(err, domain.ErrLadoIntegrationAlreadyApproved):
		return domain.LadoIntegrationApproval{}, alreadyApproved()
	case errors.Is(err, domain.ErrLadoVerificationChanged):
		// A pass stored another record of the head in the meantime.
		return domain.LadoIntegrationApproval{}, verificationChanged()
	case err != nil:
		s.deps.Log.Error("LADO could not record an integration approval", "session", exec.SessionID, "head", approval.HeadSHA, "err", err)
		return domain.LadoIntegrationApproval{}, apierr.Internal("LADO_VERIFY_FAILED", "Failed to save the integration approval")
	}
	if state == domain.LadoIntegrationStatusPending {
		posted, err := s.postIntegrationStatus(ctx, approval)
		if err != nil {
			s.deps.Log.Warn("LADO recorded an integration approval but could not post its status; the loop posts it again",
				"approval", approval.ID, "err", err)
		} else {
			approval = posted
		}
	}
	return approval, nil
}

// notReadyReasons returns the codes of the items that wait or fail, in the
// items' order, each once.
func notReadyReasons(items []domain.LadoVerifyItem) []string {
	reasons := []string{}
	for _, item := range items {
		if (item.Outcome == domain.LadoVerifyWait || item.Outcome == domain.LadoVerifyFail) && !slices.Contains(reasons, item.Code) {
			reasons = append(reasons, item.Code)
		}
	}
	return reasons
}

// codeSet returns codes sorted and without duplicates, as ConfirmCodes
// lists them.
func codeSet(codes []string) []string {
	set := append([]string{}, codes...)
	slices.Sort(set)
	return slices.Compact(set)
}

func noOpenPR(session domain.SessionID) error {
	return apierr.Conflict("LADO_EXECUTION_NO_PR", "This execution has no open pull request to integrate.",
		map[string]any{"sessionId": string(session)})
}

func headChanged(head, current string) error {
	return apierr.Conflict("LADO_INTEGRATION_HEAD_CHANGED",
		"The pull request has a new head. Approve its integration once LADO verified it.",
		map[string]any{"headSha": head, "currentHeadSha": current})
}

func verificationChanged() error {
	return apierr.Conflict("LADO_VERIFICATION_CHANGED",
		"What LADO verified about this head changed since you looked. Review it again, then approve.", nil)
}

func alreadyApproved() error {
	return apierr.Conflict("LADO_INTEGRATION_ALREADY_APPROVED", "The integration of this head is approved already.", nil)
}

func verifyFailed() error {
	return apierr.Internal("LADO_VERIFY_FAILED", "LADO could not verify this pull request's head")
}
