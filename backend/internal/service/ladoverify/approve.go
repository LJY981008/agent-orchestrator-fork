package ladoverify

// LADO fork: the integration approval (decision m of the step 8 plan;
// docs/LJY-PRD/TDD.md §7.1 and §13). In the desktop app the developer
// approves the integration of the heads of an execution's PRs, one per
// repository the task changed, together (#64): each approval
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
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopresence"
)

// ApproveInput is an integration approval or, with DryRun, its preview.
type ApproveInput struct {
	SessionID domain.SessionID
	// PRs are the PRs the developer approves together (#64): the heads of
	// every open PR LADO tracks for the execution, each with the hash of the
	// record the developer saw and the codes of its items that need
	// confirming, which the developer confirmed. A dry run may name any of
	// them, and reads neither hash nor codes.
	PRs []ApprovePR
	// PRURL, HeadSHA, VerificationSHA256 and Acknowledge name the one PR
	// approved when PRs is empty, as an execution with one tracked PR is
	// approved.
	PRURL, HeadSHA     string
	VerificationSHA256 string
	Acknowledge        []string
	DryRun             bool
	// Presence checks the request's human-presence proof for an action. A
	// dry run does not call it.
	Presence func(action, subject string) (domain.LadoPresenceProof, error)
}

// ApprovePR is one PR of an integration approval.
type ApprovePR struct {
	PRURL, HeadSHA     string
	VerificationSHA256 string
	Acknowledge        []string
}

// ApproveResult is what the approval computed for each PR it named, in the
// execution's merge order (PRs), the first of them repeated in the other
// fields: the head's record, its hash, the codes an approval acknowledges
// and, once recorded, the approval with how posting its status went (nil
// for a dry run).
type ApproveResult struct {
	Verification domain.LadoVerification
	SHA256       string
	Confirm      []string
	Approval     *domain.LadoIntegrationApproval
	PRs          []ApprovePRResult
}

// ApprovePRResult is one PR's part of an ApproveResult. Approval is the
// approval of the PR's head: the one recorded now or, for a head approved
// already, that one.
type ApprovePRResult struct {
	PRURL        string
	Verification domain.LadoVerification
	SHA256       string
	Confirm      []string
	Approval     *domain.LadoIntegrationApproval
}

// approveRequest is one PR an approval names, its head in lower case.
type approveRequest struct {
	prURL, head, sum string
	acknowledge      []string
}

// approveRequests returns the PRs in names: its PRs, or the one its single
// fields name.
func approveRequests(in ApproveInput) []approveRequest {
	prs := in.PRs
	if len(prs) == 0 {
		prs = []ApprovePR{{PRURL: in.PRURL, HeadSHA: in.HeadSHA, VerificationSHA256: in.VerificationSHA256, Acknowledge: in.Acknowledge}}
	}
	reqs := make([]approveRequest, 0, len(prs))
	for _, pr := range prs {
		reqs = append(reqs, approveRequest{
			prURL:       pr.PRURL,
			head:        strings.ToLower(strings.TrimSpace(pr.HeadSHA)),
			sum:         pr.VerificationSHA256,
			acknowledge: pr.Acknowledge,
		})
	}
	return reqs
}

// ApproveIntegration approves the integration of the heads of an
// execution's open PRs together or, with in.DryRun, previews it. An
// approval checks its presence proof, whose subject names the heads in the
// execution's merge order, before it reads anything but the execution. Both
// then read each PR and its head on the host, fetch its base branch, read
// the base branch's rules past the cache and compute and store the head's
// record again. A dry run answers the records whatever their outcomes, for
// any of the open tracked PRs. An approval names every open PR LADO tracks
// for the execution (LADO_INTEGRATION_PRS_CHANGED otherwise) and needs each
// record's hash to be the one signed, no repository the execution is
// expected to open a PR in to be without one and no item to wait or fail
// (LADO_INTEGRATION_NOT_READY, pr_missing among the reasons for the first),
// and the acknowledged codes to be each record's confirm codes. A head
// approved already is left as it is; the others are recorded with the one
// proof and an audit event in one transaction, an approval LADO withdrew for
// a sibling PR's base renewed in place, and each lado/integration status is
// posted once right away: a post that fails leaves the approval, its status
// failed, for the loop to post again. When every head is approved already
// the approval is refused. Every refusal is an *apierr.Error.
func (s *Service) ApproveIntegration(ctx context.Context, in ApproveInput) (ApproveResult, error) {
	reqs := approveRequests(in)
	var exec domain.LadoExecution
	read := false
	if len(reqs) > 1 {
		// The proof's subject names the heads in merge order.
		var err error
		if exec, err = s.integrationExecutionRead(ctx, in.SessionID); err != nil {
			return ApproveResult{}, err
		}
		read = true
		reqs = inMergeOrder(exec, reqs)
	}
	var proof domain.LadoPresenceProof
	if !in.DryRun {
		heads := make([]string, 0, len(reqs))
		for _, req := range reqs {
			heads = append(heads, req.head)
		}
		var err error
		if proof, err = s.integrationPresence(ctx, in.SessionID, strings.Join(heads, ","), in.Presence); err != nil {
			return ApproveResult{}, err
		}
	}
	if !read {
		var err error
		if exec, err = s.integrationExecutionRead(ctx, in.SessionID); err != nil {
			return ApproveResult{}, err
		}
	}
	execs, err := integrationPRs(exec, reqs, in.DryRun)
	if err != nil {
		return ApproveResult{}, err
	}
	results := make([]ApprovePRResult, len(execs))
	inputs := make([]headInputs, len(execs))
	for i, one := range execs {
		head := reqs[i].head
		if err := s.livePRHolds(ctx, one, head); err != nil {
			return ApproveResult{}, err
		}
		one.PR.HeadSHA = head
		execs[i] = one
		record, headIn, err := s.verifyHeadIn(ctx, one, verifyOptions{forceProtection: true, fetchBase: true})
		if err != nil {
			s.deps.Log.Error("LADO could not verify a head for its integration approval", "session", one.SessionID, "pr", one.PR.URL, "head", head, "err", err)
			return ApproveResult{}, verifyFailed()
		}
		sum, err := record.SHA256()
		if err != nil {
			s.deps.Log.Error("LADO could not hash a verification record", "session", one.SessionID, "head", head, "err", err)
			return ApproveResult{}, verifyFailed()
		}
		results[i] = ApprovePRResult{PRURL: one.PR.URL, Verification: record, SHA256: sum, Confirm: record.ConfirmCodes()}
		inputs[i] = headIn
	}
	result := approveResult(results)
	if in.DryRun {
		return result, nil
	}
	pending, err := s.approvable(ctx, exec, execs, reqs, results)
	if err != nil {
		return ApproveResult{}, err
	}
	if err := s.recordApprovals(ctx, exec, pending, execs, results, proof, inputs); err != nil {
		return ApproveResult{}, err
	}
	s.Kick()
	return approveResult(results), nil
}

// approveResult is the result of the PRs' results, the first repeated in
// its own fields.
func approveResult(results []ApprovePRResult) ApproveResult {
	first := results[0]
	return ApproveResult{Verification: first.Verification, SHA256: first.SHA256, Confirm: first.Confirm, Approval: first.Approval, PRs: results}
}

// inMergeOrder orders reqs as the execution's tracked PRs are ordered; a PR
// it does not track comes after them, in the order given.
func inMergeOrder(exec domain.LadoExecution, reqs []approveRequest) []approveRequest {
	tracked := exec.TrackedPRs()
	rank := func(req approveRequest) int {
		if i := slices.IndexFunc(tracked, func(pr domain.LadoExecutionTrackedPR) bool { return pr.PR.URL == req.prURL }); i >= 0 {
			return i
		}
		return len(tracked)
	}
	reqs = slices.Clone(reqs)
	slices.SortStableFunc(reqs, func(a, b approveRequest) int { return rank(a) - rank(b) })
	return reqs
}

// integrationPresence takes the desktop app's confirmation of the approval,
// with <session>@<heads> as its subject (the heads joined by "," in merge
// order), and refuses a proof used before.
func (s *Service) integrationPresence(ctx context.Context, session domain.SessionID, heads string, presence func(action, subject string) (domain.LadoPresenceProof, error)) (domain.LadoPresenceProof, error) {
	if presence == nil {
		return domain.LadoPresenceProof{}, apierr.Forbidden("LADO_PRESENCE_REQUIRED",
			"Approve the integration in the AO desktop app. It asks you to confirm, then sends the approval itself.")
	}
	proof, err := presence(domain.LadoPresenceIntegrationApprove, string(session)+"@"+heads)
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

// integrationExecutionRead reads the execution an approval is for.
func (s *Service) integrationExecutionRead(ctx context.Context, session domain.SessionID) (domain.LadoExecution, error) {
	exec, ok, err := s.deps.Store.LadoExecution(ctx, session)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read an execution for its integration approval", "session", session, "err", err)
		return domain.LadoExecution{}, verifyFailed()
	case !ok:
		return domain.LadoExecution{}, apierr.NotFound("LADO_EXECUTION_NOT_FOUND", "This session is not a LADO execution")
	}
	return exec, nil
}

// openTrackedPRs returns the execution's tracked PRs that are open, in merge
// order: its PR alone for an execution without tracked PRs.
func openTrackedPRs(exec domain.LadoExecution) []domain.LadoExecutionTrackedPR {
	return slices.DeleteFunc(exec.TrackedPRs(), func(pr domain.LadoExecutionTrackedPR) bool {
		return pr.PR.URL == "" || pr.PR.Merged || pr.PR.Closed
	})
}

// integrationPRs returns the execution once for each PR reqs name, with that
// PR as its PR and its verification and integration facts, in reqs' order.
// It checks that the PRs are open PRs the execution tracks and, for a dry
// run, that each head is the one AO observed, which the developer was shown.
// An approval names them all; one of an execution with one open PR, which
// names another, has seen its PR change.
func integrationPRs(exec domain.LadoExecution, reqs []approveRequest, dryRun bool) ([]domain.LadoExecution, error) {
	open := openTrackedPRs(exec)
	if len(open) == 0 {
		return nil, noOpenPR(exec.SessionID)
	}
	execs := make([]domain.LadoExecution, 0, len(reqs))
	if len(reqs) == 1 && len(open) == 1 {
		req, pr := reqs[0], open[0]
		switch {
		case req.prURL != pr.PR.URL:
			return nil, apierr.Conflict("LADO_INTEGRATION_HEAD_CHANGED",
				"The execution's pull request changed. Review the new one, then approve its integration.",
				map[string]any{"headSha": req.head, "currentHeadSha": strings.ToLower(pr.PR.HeadSHA), "prUrl": req.prURL, "currentPrUrl": pr.PR.URL})
		case dryRun && !strings.EqualFold(req.head, pr.PR.HeadSHA):
			return nil, headChanged(req.head, strings.ToLower(pr.PR.HeadSHA))
		}
		return append(execs, withTrackedPR(exec, pr)), nil
	}
	var urls []string
	for _, req := range reqs {
		urls = append(urls, req.prURL)
	}
	current := make([]string, 0, len(open))
	for _, pr := range open {
		current = append(current, pr.PR.URL)
	}
	changed := apierr.Conflict("LADO_INTEGRATION_PRS_CHANGED",
		"The execution's pull requests changed. Review them again, then approve the task's integration.",
		map[string]any{"prUrls": urls, "currentPrUrls": current})
	if !dryRun && len(reqs) != len(open) {
		return nil, changed
	}
	for i, req := range reqs {
		at := slices.IndexFunc(open, func(pr domain.LadoExecutionTrackedPR) bool { return pr.PR.URL == req.prURL })
		if at < 0 || slices.Contains(urls[:i], req.prURL) {
			return nil, changed
		}
		if dryRun && !strings.EqualFold(req.head, open[at].PR.HeadSHA) {
			return nil, headChanged(req.head, strings.ToLower(open[at].PR.HeadSHA))
		}
		execs = append(execs, withTrackedPR(exec, open[at]))
	}
	return execs, nil
}

// withTrackedPR is the execution with pr as its PR.
func withTrackedPR(exec domain.LadoExecution, pr domain.LadoExecutionTrackedPR) domain.LadoExecution {
	exec.PR, exec.Verification, exec.Integration = pr.PR, pr.Verification, pr.Integration
	return exec
}

// livePRHolds checks that head is the head of the execution's PR on its
// host, and that the host has the PR open and merging into the branch the
// record verifies.
func (s *Service) livePRHolds(ctx context.Context, exec domain.LadoExecution, head string) error {
	pr, ok, err := s.deps.Store.GetPR(ctx, exec.PR.URL)
	switch {
	case err != nil:
		s.deps.Log.Error("LADO could not read a PR for its integration approval", "pr", exec.PR.URL, "err", err)
		return verifyFailed()
	case !ok:
		return noOpenPR(exec.SessionID)
	}
	reader := s.liveHeadReader()
	if reader == nil {
		return apierr.Unavailable("LADO_VERIFY_UNAVAILABLE",
			"LADO cannot read pull requests from their host, so it cannot approve an integration now. Check AO's GitHub or GitLab connection.")
	}
	live, err := reader.LivePR(ctx, pr)
	if err != nil {
		s.deps.Log.Warn("LADO could not read a PR from its host", "session", exec.SessionID, "pr", exec.PR.URL, "err", redactedErrorLine(err))
		return apierr.Unavailable("LADO_VERIFY_UNAVAILABLE",
			"LADO could not read the pull request's head from its host. Try again.")
	}
	// The host may have merged, closed or retargeted the PR before AO saw
	// it: the record verified the stored target, and the status LADO posts
	// on the head would count for any branch that requires it.
	liveHead := strings.ToLower(strings.TrimSpace(live.HeadSHA))
	switch target := strings.TrimSpace(live.TargetBranch); {
	case live.Merged || live.Closed:
		return noOpenPR(exec.SessionID)
	case liveHead != head:
		return headChanged(head, liveHead)
	case target != "" && target != approvalTarget(exec):
		return targetChanged(domain.PullRequest{URL: exec.PR.URL, SessionID: exec.SessionID, TargetBranch: target}, head, approvalTarget(exec))
	}
	return nil
}

// approvalTarget is the branch an approval of the execution's PR holds for:
// the PR's target, else the base the dispatcher resolved, as the record
// verified it.
func approvalTarget(exec domain.LadoExecution) string {
	if exec.PR.TargetBranch != "" {
		return exec.PR.TargetBranch
	}
	return exec.BaseBranch
}

// pendingApproval is one PR head an approval records: its index among the
// approval's PRs and, for an approval LADO withdrew for a sibling PR's base,
// the id of the row it renews.
type pendingApproval struct {
	index   int
	renewID string
}

// approvable checks the PRs of an approval, execs and results in reqs'
// order, and returns those to record. It refuses a PR head whose approval
// into its target LADO revoked for its own base (#53), a record other than
// the one signed, an execution still without a PR in a repository it is
// expected to open one in (pr_missing) or with an item of a record that
// waits or fails, and acknowledgements other than a record's confirm codes.
// A head approved already into its target is left out, its approval in its
// result; when every head is, the approval is refused.
func (s *Service) approvable(ctx context.Context, exec domain.LadoExecution, execs []domain.LadoExecution, reqs []approveRequest, results []ApprovePRResult) ([]pendingApproval, error) {
	held := make([][]domain.LadoIntegrationApproval, len(execs))
	for i, one := range execs {
		record := results[i].Verification
		approvals, err := s.deps.Store.ListLadoIntegrationApprovals(ctx, record.ExecutionSessionID, record.PRURL, record.HeadSHA)
		if err != nil {
			s.deps.Log.Error("LADO could not read the integration approvals of a head", "session", record.ExecutionSessionID, "head", record.HeadSHA, "err", err)
			return nil, verifyFailed()
		}
		target := approvalTarget(one)
		held[i] = slices.DeleteFunc(approvals, func(a domain.LadoIntegrationApproval) bool { return !strings.EqualFold(a.TargetBranch, target) })
		if slices.ContainsFunc(held[i], func(a domain.LadoIntegrationApproval) bool { return a.Revoked() && !a.Renewable() }) {
			return nil, integrationRevoked()
		}
		if !strings.EqualFold(strings.TrimSpace(reqs[i].sum), results[i].SHA256) {
			return nil, verificationChanged()
		}
	}
	reasons := []string{}
	missing := exec.MissingPRRepos()
	if len(missing) > 0 {
		reasons = append(reasons, domain.LadoValidatingPRMissing)
	}
	for _, result := range results {
		if outcome, _ := result.Verification.Overall(); outcome == domain.LadoVerifyWait || outcome == domain.LadoVerifyFail {
			for _, reason := range notReadyReasons(result.Verification.Items) {
				if !slices.Contains(reasons, reason) {
					reasons = append(reasons, reason)
				}
			}
		}
	}
	if len(reasons) > 0 {
		details := map[string]any{"reasons": reasons}
		if len(missing) > 0 {
			details["missingRepos"] = missing
		}
		message := "LADO still verifies this head, or some of its checks fail. Approve its integration once they pass."
		if len(results) > 1 || len(missing) > 0 {
			message = "LADO still verifies the task's pull requests, some of their checks fail, or a pull request the task needs is not open yet. Approve its integration once they pass."
		}
		return nil, apierr.Conflict("LADO_INTEGRATION_NOT_READY", message, details)
	}
	for i, result := range results {
		if given := codeSet(reqs[i].acknowledge); !slices.Equal(given, result.Confirm) {
			details := map[string]any{"required": result.Confirm, "given": given}
			if len(results) > 1 {
				details["prUrl"] = result.PRURL
			}
			return nil, apierr.Invalid("LADO_INTEGRATION_ACK_MISMATCH",
				"Confirm exactly the items of this head that need confirming.", details)
		}
	}
	var pending []pendingApproval
	for i := range results {
		if at := slices.IndexFunc(held[i], func(a domain.LadoIntegrationApproval) bool { return !a.Revoked() }); at >= 0 {
			approval := held[i][at]
			results[i].Approval = &approval
			continue
		}
		next := pendingApproval{index: i}
		if at := slices.IndexFunc(held[i], domain.LadoIntegrationApproval.Renewable); at >= 0 {
			next.renewID = held[i][at].ID
		}
		pending = append(pending, next)
	}
	if len(pending) == 0 {
		return nil, alreadyApproved()
	}
	return pending, nil
}

// derivedPresenceNonce is the proof nonce the store gives the approval at
// index i of those one proof signs (store.LadoDerivedPresenceNonce): the
// proof's own for the first, "<nonce>#<i>" for the others.
func derivedPresenceNonce(nonce string, i int) string {
	if i == 0 {
		return nonce
	}
	return nonce + "#" + strconv.Itoa(i)
}

// recordApprovals records the pending approvals with the proof and one audit
// event in one transaction, then posts each one's status once on GitHub and
// puts each in its result. GitLab gets no status: its approvals are
// not_applicable. inputs are what each record was computed from: the
// repository verified, whose host takes the status (#59), and the remote
// target's commit the record was computed against, which the merge gate
// compares with (#53).
func (s *Service) recordApprovals(ctx context.Context, exec domain.LadoExecution, pending []pendingApproval, execs []domain.LadoExecution, results []ApprovePRResult, proof domain.LadoPresenceProof, inputs []headInputs) error {
	now := s.now()
	actor := "human"
	if proof.Dev {
		actor = "human:dev-presence"
	}
	approvals := make([]domain.LadoIntegrationApproval, 0, len(pending))
	prs := make([]map[string]any, 0, len(pending))
	for n, p := range pending {
		result := results[p.index]
		state := domain.LadoIntegrationStatusPending
		if inputs[p.index].repo.Provider == domain.TrackerProviderGitLab {
			state = domain.LadoIntegrationStatusNotApplicable
		}
		id := p.renewID
		if id == "" {
			id = "lado-integration-" + s.deps.NewID()
		}
		approval := domain.LadoIntegrationApproval{
			ID:                 id,
			ExecutionSessionID: exec.SessionID,
			PRURL:              result.Verification.PRURL,
			HeadSHA:            result.Verification.HeadSHA,
			TargetBranch:       approvalTarget(execs[p.index]),
			VerificationSHA256: result.SHA256,
			Acknowledged:       result.Confirm,
			ProofNonce:         derivedPresenceNonce(proof.Nonce, n),
			Actor:              actor,
			ApprovedAt:         now,
			StatusState:        state,
			BaseTipSHA:         inputs[p.index].baseTip,
		}
		approvals = append(approvals, approval)
		prs = append(prs, map[string]any{
			"pr":           approval.PRURL,
			"head":         approval.HeadSHA,
			"target":       approval.TargetBranch,
			"verification": approval.VerificationSHA256,
			"acknowledged": approval.Acknowledged,
		})
	}
	fields := map[string]any{"task": exec.TaskID, "session": exec.SessionID, "proof": proof.Nonce, "dev": proof.Dev}
	if len(prs) == 1 {
		maps.Copy(fields, prs[0])
	} else {
		fields["prs"] = prs
	}
	payload, _ := json.Marshal(fields)
	err := s.deps.Store.ApproveLadoIntegrations(ctx, approvals, proof, domain.LadoAuditEvent{
		At:          now,
		Actor:       actor,
		Action:      "integration.approved",
		SubjectKind: "execution",
		SubjectID:   string(exec.SessionID),
		Payload:     payload,
	})
	switch {
	case errors.Is(err, domain.ErrLadoPresenceReplayed):
		return ladopresence.Replayed()
	case errors.Is(err, domain.ErrLadoIntegrationRevoked):
		return integrationRevoked()
	case errors.Is(err, domain.ErrLadoIntegrationAlreadyApproved):
		return alreadyApproved()
	case errors.Is(err, domain.ErrLadoVerificationChanged):
		// A pass stored another record of a head in the meantime.
		return verificationChanged()
	case err != nil:
		s.deps.Log.Error("LADO could not record an integration approval", "session", exec.SessionID, "err", err)
		return apierr.Internal("LADO_VERIFY_FAILED", "Failed to save the integration approval")
	}
	for n, p := range pending {
		approval := approvals[n]
		if approval.StatusState == domain.LadoIntegrationStatusPending {
			posted, err := s.postIntegrationStatus(ctx, approval)
			if err != nil {
				s.deps.Log.Warn("LADO recorded an integration approval but could not post its status; the loop posts it again",
					"approval", approval.ID, "err", err)
			} else {
				approval = posted
			}
		}
		results[p.index].Approval = &approval
	}
	return nil
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
	return apierr.Conflict("LADO_INTEGRATION_ALREADY_APPROVED", "The integration of this head into its target branch is approved already.", nil)
}

func verifyFailed() error {
	return apierr.Internal("LADO_VERIFY_FAILED", "LADO could not verify this pull request's head")
}
