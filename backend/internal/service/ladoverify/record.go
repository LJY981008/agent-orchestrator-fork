package ladoverify

// The record of one head (decisions b and c of the step 8 plan): the eight
// items in their fixed order, computed from what LADO and AO know about the
// head, with what the record waits for started on the way. The record is
// evidence about that head, like a check run; a new head gets its own.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladopolicy"
)

const (
	// finishTimeout bounds the writes that record a check command's end,
	// which outlive the context the command ran under.
	finishTimeout = 10 * time.Second
	// headFetchEvery is how often the loop fetches a head the repository
	// lacks; an integration approval and its dry run fetch it every time.
	headFetchEvery = 5 * time.Minute
)

// errHeadUnavailable is why an item git reads cannot be computed while the
// repository lacks the head: it waits as head_unavailable.
var errHeadUnavailable = errors.New("the repository does not have the head, and LADO could not fetch it")

// verifyOptions say how a head's record is computed. forceProtection reads
// the base branch's rules again whatever the cache holds; startWork starts
// the check command, the review round or its cancellation and the fix
// requests the record calls for; fetchBase fetches the base branch from its
// remote before git reads anything, and the PR's head when the repository
// lacks it, as an integration approval does.
// loopFetch fetches the base branch as the loop does, at most every
// baseFetchEvery per repository, remote and branch, and forceFetch past
// that, as after a merge into it (#53).
type verifyOptions struct {
	forceProtection bool
	startWork       bool
	fetchBase       bool
	loopFetch       bool
	forceFetch      bool
}

// headInputs is what a head's record is computed from.
type headInputs struct {
	exec domain.LadoExecution
	task domain.LadoTask
	head string
	// dir is the repository that holds the issue, and prefix its folder in
	// a workspace project, slash-separated (empty for a single repository):
	// the plan's paths are relative to the project root.
	dir, prefix string
	// owner is the session that takes the head's fix and pull requests: the
	// execution's own session while it lives (domain.LadoSessionLive), else
	// its live resolve worker; empty when neither lives, and live says
	// whether there is one (#53).
	owner domain.SessionID
	live  bool
	// worktree is the worktree of that repository where the check commands
	// run: the one at the head of the execution's own session and its
	// resolve worker, else the owner's, else the execution's own; empty when
	// AO recorded none.
	worktree string
	// ignore are the project's globs whose files never count as an overlap
	// with the base.
	ignore []string
	// headMissing says that the repository lacks the head and LADO could
	// not fetch it (ensureHead).
	headMissing bool
	env         map[string]string
	plan        domain.LadoPlan
	// validators are those of the execution's rule snapshot.
	validators validatorSet
	commands   []domain.LadoCheckCommand
	timeout    time.Duration
	// target is the branch the PR merges into. baseBranch and baseRemote are
	// the base the dispatcher resolved, else the target on origin.
	target, baseBranch, baseRemote string
	// baseTip is the remote-tracking target's commit right after an
	// approval fetched it (verifyOptions.fetchBase), empty when git has none.
	baseTip string
}

// validatorSet is what a rule snapshot's validators turn on: the Korean
// commit message check with its config, and the protected paths with their
// globs. A snapshot older than lado-policy/3 has no validators.
type validatorSet struct {
	commitLocale *domain.LadoCommitLocaleConfig
	protectedOn  bool
	protected    []string
}

// branchFacts are the head's merge-base with the target branch and the
// commits after it, oldest first, read once per record.
type branchFacts struct {
	base    string
	commits []Commit
	err     error
}

// evaluation is a head's items and what the fix requests and the evidence
// links take from computing them.
type evaluation struct {
	items []domain.LadoVerifyItem
	// runs are the head's check runs, with the one the pass started.
	// commandsSettled says that none of them runs and no command can start
	// now, so a failure of the commands names all that failed.
	runs            []domain.LadoCheckRun
	commandsSettled bool
	// reviewRuns are the worker's review runs, when the plan requires one.
	reviewRuns []domain.ReviewRun
	// links are the head's evidence links; resolved says whether the
	// evidence item could be computed.
	links    []domain.LadoEvidenceLink
	resolved bool
	// protectionError is the message of a failed read of the base branch's
	// rules, which the record keeps outside its hash.
	protectionError string
}

// verifyHead computes and stores the record of the execution's PR head. The
// record keeps the list of items the worker was told about; with
// opts.startWork the fix requests of items that fail now go out first. The
// head's evidence links are replaced when the evidence item changed.
func (s *Service) verifyHead(ctx context.Context, exec domain.LadoExecution, opts verifyOptions) (domain.LadoVerification, error) {
	record, _, err := s.verifyHeadIn(ctx, exec, opts)
	return record, err
}

// verifyHeadIn is verifyHead, which also returns the inputs it read.
func (s *Service) verifyHeadIn(ctx context.Context, exec domain.LadoExecution, opts verifyOptions) (domain.LadoVerification, headInputs, error) {
	s.verifyMu.Lock()
	defer s.verifyMu.Unlock()
	in, err := s.headInputs(ctx, exec)
	if err != nil {
		return domain.LadoVerification{}, in, err
	}
	switch {
	case opts.fetchBase:
		s.fetchBase(ctx, in, true)
		if in.baseTip, err = s.remoteTip(ctx, in); err != nil {
			s.deps.Log.Warn("LADO could not read the remote base branch it fetched", "session", exec.SessionID,
				"remote", in.baseRemote, "branch", in.target, "err", redactedErrorLine(err))
		}
	case opts.loopFetch:
		s.fetchBase(ctx, in, opts.forceFetch)
	}
	in.headMissing = !s.ensureHead(ctx, in, opts.fetchBase)
	ev := s.evaluate(ctx, in, opts)
	if ctx.Err() != nil {
		// A stopping daemon leaves the stored record as it was.
		return domain.LadoVerification{}, in, ctx.Err()
	}
	now := s.now()
	record := domain.LadoVerification{
		ExecutionSessionID: exec.SessionID,
		HeadSHA:            in.head,
		PRURL:              exec.PR.URL,
		Items:              ev.items,
		Notified:           []string{},
		ProtectionError:    ev.protectionError,
		ComputedAt:         now,
	}
	previous, found, err := s.deps.Store.GetLadoVerification(ctx, exec.SessionID, in.head)
	if err != nil {
		return domain.LadoVerification{}, in, fmt.Errorf("read the record of head %s: %w", in.head, err)
	}
	if found && previous.Notified != nil {
		record.Notified = previous.Notified
	}
	if ev.resolved && (!found || !sameItem(previous.Items, ev.items, domain.LadoVerifyEvidence)) {
		for i := range ev.links {
			ev.links[i].ID, ev.links[i].CreatedAt = "lado-evidence-"+s.deps.NewID(), now
		}
		if err := s.deps.Store.ReplaceLadoEvidenceLinks(ctx, exec.SessionID, in.head, ev.links); err != nil {
			return domain.LadoVerification{}, in, fmt.Errorf("store the evidence links of head %s: %w", in.head, err)
		}
	}
	if opts.startWork {
		record.Notified = s.requestFixes(ctx, in, ev, record.Notified)
		record.Notified = s.requestPull(ctx, in, record.Notified)
	}
	if _, err := s.deps.Store.UpsertLadoVerification(ctx, record); err != nil {
		return domain.LadoVerification{}, in, fmt.Errorf("store the record of head %s: %w", in.head, err)
	}
	s.markVerified(ctx, record)
	return record, in, nil
}

// headInputs reads the inputs of the execution's PR head. Without its task,
// project, plan, rule snapshot or check settings no item can be computed.
func (s *Service) headInputs(ctx context.Context, exec domain.LadoExecution) (headInputs, error) {
	in := headInputs{exec: exec, head: exec.PR.HeadSHA}
	task, ok, err := s.deps.Tasks.LadoTask(ctx, exec.TaskID)
	switch {
	case err != nil:
		return in, fmt.Errorf("read task %s: %w", exec.TaskID, err)
	case !ok:
		return in, fmt.Errorf("task %s not found", exec.TaskID)
	}
	in.task = task
	if err := s.readPlaces(ctx, &in); err != nil {
		return in, err
	}
	if in.plan, err = s.deps.Plans.ApprovedPlan(ctx, exec.PlanRevisionID); err != nil {
		return in, fmt.Errorf("read the approved plan: %w", err)
	}
	if in.validators, err = s.readValidators(ctx, exec); err != nil {
		return in, err
	}
	commands, minutes, err := s.deps.Store.LadoProjectChecks(ctx, task.ProjectID)
	if err != nil {
		return in, fmt.Errorf("read the project's check commands: %w", err)
	}
	in.commands, in.timeout = commands, time.Duration(minutes)*time.Minute
	settings, err := s.deps.Store.LadoProjectSettings(ctx, task.ProjectID)
	if err != nil {
		return in, fmt.Errorf("read the project's LADO settings: %w", err)
	}
	in.ignore = settings.OverlapIgnoreOrDefault()
	in.target = cmp.Or(exec.PR.TargetBranch, exec.BaseBranch)
	in.baseBranch = cmp.Or(exec.BaseBranch, in.target)
	in.baseRemote = cmp.Or(exec.BaseRemote, "origin")
	if in.target == "" {
		return in, errors.New("the PR has no target branch yet")
	}
	return in, nil
}

// readPlaces finds the repository that holds the issue as the dispatcher
// does (the project's folder, or in a workspace project the child repository
// with the issue's origin), the execution's worktree of it, whether the
// worker session lives, and the project's environment.
func (s *Service) readPlaces(ctx context.Context, in *headInputs) error {
	project, ok, err := s.deps.Projects.GetProject(ctx, string(in.task.ProjectID))
	switch {
	case err != nil:
		return fmt.Errorf("read project %s: %w", in.task.ProjectID, err)
	case !ok:
		return fmt.Errorf("project %s not found", in.task.ProjectID)
	}
	in.dir, in.env = project.Path, project.Config.Env
	relative := ""
	if project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
		if err != nil {
			return fmt.Errorf("read the workspace repositories of project %s: %w", project.ID, err)
		}
		found := false
		for _, child := range children {
			repo, err := domain.LadoTrackerRepoFromOrigin(child.RepoOriginURL)
			if err == nil && domain.LadoSameRepo(repo, in.task.Repo) {
				relative, found = child.RelativePath, true
				break
			}
		}
		if !found {
			return fmt.Errorf("no workspace repository has the origin %s", domain.LadoRepoRef(in.task.Repo))
		}
		in.dir, in.prefix = filepath.Join(project.Path, filepath.FromSlash(relative)), filepath.ToSlash(relative)
	}
	return s.readOwner(ctx, in, relative)
}

// readOwner finds the session that takes the head's requests and the
// worktree where its check commands run (headInputs.owner and worktree).
func (s *Service) readOwner(ctx context.Context, in *headInputs, relative string) error {
	worktreeOf := func(session domain.SessionRecord) string {
		if session.Metadata.WorkspacePath == "" {
			return ""
		}
		return filepath.Join(session.Metadata.WorkspacePath, filepath.FromSlash(relative))
	}
	session, ok, err := s.deps.Sessions.GetSession(ctx, in.exec.SessionID)
	if err != nil {
		return fmt.Errorf("read the worker session: %w", err)
	}
	own := ""
	if ok {
		own = worktreeOf(session)
		if domain.LadoSessionLive(session) && !in.exec.SessionTerminated {
			in.owner = in.exec.SessionID
		}
	}
	trees := []string{own}
	if id := in.exec.Resolve.SessionID; id != "" && id != in.exec.SessionID {
		worker, ok, err := s.deps.Sessions.GetSession(ctx, id)
		if err != nil {
			return fmt.Errorf("read the resolve worker session: %w", err)
		}
		if ok {
			tree := worktreeOf(worker)
			if in.owner == "" && domain.LadoSessionLive(worker) {
				in.owner = id
				// The owner's worktree comes first.
				trees = []string{tree, own}
			} else {
				trees = append(trees, tree)
			}
		}
	}
	in.live = in.owner != ""
	in.worktree = cmp.Or(trees...)
	for _, tree := range trees {
		if tree == "" || in.head == "" {
			continue
		}
		if current, err := s.deps.Git.RevParse(ctx, tree, "HEAD"); err == nil && strings.EqualFold(current, in.head) {
			in.worktree = tree
			break
		}
	}
	return nil
}

// readValidators reads the validators of the execution's rule snapshot.
func (s *Service) readValidators(ctx context.Context, exec domain.LadoExecution) (validatorSet, error) {
	var set validatorSet
	if exec.PlanApprovalID == "" {
		return set, nil
	}
	snapshot, ok, err := s.deps.Snapshots.LadoPolicySnapshot(ctx, exec.PlanApprovalID)
	if err != nil {
		return set, fmt.Errorf("read the rule snapshot: %w", err)
	}
	if !ok {
		// The plan was approved before LADO had rules.
		return set, nil
	}
	var content struct {
		Validators []ladopolicy.SnapshotValidator `json:"validators"`
	}
	if err := json.Unmarshal(snapshot.Content, &content); err != nil {
		return set, fmt.Errorf("read the rule snapshot: %w", err)
	}
	for _, validator := range content.Validators {
		switch validator.Kind {
		case ladopolicy.ValidatorCommitMessageLocale:
			var cfg domain.LadoCommitLocaleConfig
			if err := json.Unmarshal(validator.Config, &cfg); err != nil {
				return set, fmt.Errorf("read the rule snapshot's commit message validator: %w", err)
			}
			set.commitLocale = &cfg
		case ladopolicy.ValidatorProtectedPaths:
			var cfg domain.LadoProtectedPathsConfig
			if err := json.Unmarshal(validator.Config, &cfg); err != nil {
				return set, fmt.Errorf("read the rule snapshot's protected paths validator: %w", err)
			}
			set.protectedOn, set.protected = true, cfg.Globs
		}
	}
	return set, nil
}

// evaluate computes the head's items in their fixed order. An item whose
// inputs cannot be read this time waits as verifying, so the record always
// holds every item.
func (s *Service) evaluate(ctx context.Context, in headInputs, opts verifyOptions) evaluation {
	var ev evaluation
	read := s.protection.Read(ctx, in.task.Repo, in.target, opts.forceProtection)
	ev.protectionError = read.ErrorMessage()
	required, named := RequiredContexts(read.Rules)
	checks, checksErr := s.deps.Store.ListLadoCommitChecks(ctx, in.exec.PR.URL, in.head)
	if checksErr != nil {
		checksErr = fmt.Errorf("read the head's CI checks: %w", checksErr)
	}
	runs, runsErr := s.deps.Store.ListLadoCheckRuns(ctx, in.exec.SessionID, in.head)
	if runsErr != nil {
		runsErr = fmt.Errorf("read the head's check runs: %w", runsErr)
	}
	branch := s.branch(ctx, in)

	steps := []struct {
		kind     domain.LadoVerifyKind
		evaluate func() (domain.LadoVerifyItem, error)
	}{
		{domain.LadoVerifyRequiredChecks, func() (domain.LadoVerifyItem, error) {
			if checksErr != nil {
				return domain.LadoVerifyItem{}, checksErr
			}
			return EvaluateRequiredChecks(required, named, read.Err == nil, checks)
		}},
		{domain.LadoVerifyCommands, func() (domain.LadoVerifyItem, error) {
			if runsErr != nil {
				return domain.LadoVerifyItem{}, runsErr
			}
			return s.commandsItem(ctx, in, runs, named, opts, &ev)
		}},
		{domain.LadoVerifyCommitMessages, func() (domain.LadoVerifyItem, error) {
			if in.validators.commitLocale == nil {
				return EvaluateCommits(nil, nil, "")
			}
			if branch.err != nil {
				return domain.LadoVerifyItem{}, branch.err
			}
			return EvaluateCommits(in.validators.commitLocale, branch.commits, in.exec.PR.Title)
		}},
		{domain.LadoVerifyPaths, func() (domain.LadoVerifyItem, error) {
			if branch.err != nil {
				return domain.LadoVerifyItem{}, branch.err
			}
			changed, err := s.deps.Git.ChangedPaths(ctx, in.dir, branch.base, in.head)
			if err != nil {
				return domain.LadoVerifyItem{}, err
			}
			return EvaluatePaths(PathsInput{
				Changed:     PrefixPaths(in.prefix, changed),
				Affected:    in.plan.AffectedPaths,
				Protected:   in.validators.protected,
				ProtectedOn: in.validators.protectedOn,
				Repository:  in.prefix,
			})
		}},
		{domain.LadoVerifyBaseBranch, func() (domain.LadoVerifyItem, error) {
			return EvaluateBase(ctx, s.deps.Git, BaseInput{
				Dir: in.dir, Branch: in.baseBranch, Remote: in.baseRemote, HeadSHA: in.head, StartedAt: in.exec.StartedAt,
				DispatchLocalSHA: in.exec.DispatchLocalBaseSHA, Target: in.target, Prefix: in.prefix,
				Affected: in.plan.AffectedPaths, Ignore: in.ignore,
			})
		}},
		{domain.LadoVerifyProtection, func() (domain.LadoVerifyItem, error) {
			return EvaluateProtection(read)
		}},
		{domain.LadoVerifyReview, func() (domain.LadoVerifyItem, error) {
			return s.reviewItem(ctx, in, opts, &ev)
		}},
		{domain.LadoVerifyEvidence, func() (domain.LadoVerifyItem, error) {
			if err := errors.Join(checksErr, runsErr, branch.err); err != nil {
				return domain.LadoVerifyItem{}, err
			}
			return s.evidenceItem(ctx, in, checks, branch.commits, &ev)
		}},
	}
	for _, step := range steps {
		item, err := step.evaluate()
		switch {
		case errors.Is(err, errHeadUnavailable):
			item = domain.LadoVerifyItem{Kind: step.kind, Outcome: domain.LadoVerifyWait, Code: domain.LadoCodeHeadUnavailable, Details: json.RawMessage("{}")}
		case err != nil:
			s.deps.Log.Warn("LADO could not compute a verification item; it waits", "session", in.exec.SessionID, "head", in.head, "item", step.kind, "err", err)
			item = verifyingItem(step.kind)
		}
		ev.items = append(ev.items, item)
	}
	return ev
}

// ensureHead reports whether the repository that holds the issue has the
// head. When it lacks it, as after GitHub's Update branch, an applied
// suggestion or a bot's push, LADO fetches the head ref the host keeps for
// the PR from the base remote (prompts off, 30 seconds): the loop at most
// once per head every headFetchEvery, an integration approval and its dry
// run (force) every time. A head git cannot tell about counts as there, so
// the reads that need it report their own error.
func (s *Service) ensureHead(ctx context.Context, in headInputs, force bool) bool {
	has, err := s.deps.Git.HasCommit(ctx, in.dir, in.head)
	if err != nil || has {
		return true
	}
	ref := PullHeadRef(in.task.Repo.Provider, in.exec.PR.Number)
	if ref == "" {
		return false
	}
	key, now := headKey(in.exec.SessionID, in.head), s.now()
	s.mu.Lock()
	last, tried := s.headFetched[key]
	if !force && tried && now.Sub(last) < headFetchEvery {
		s.mu.Unlock()
		return false
	}
	for other, at := range s.headFetched {
		if now.Sub(at) >= headFetchEvery {
			delete(s.headFetched, other)
		}
	}
	s.headFetched[key] = now
	s.mu.Unlock()
	if err := s.deps.Git.FetchPullHead(ctx, in.dir, in.baseRemote, ref); err != nil {
		s.deps.Log.Warn("LADO could not fetch a pull request's head; the items git reads wait", "session", in.exec.SessionID,
			"head", in.head, "ref", ref, "err", redactedErrorLine(err))
		return false
	}
	has, err = s.deps.Git.HasCommit(ctx, in.dir, in.head)
	return err != nil || has
}

// headKey names one head of an execution.
func headKey(session domain.SessionID, head string) string {
	return string(session) + "@" + head
}

// fetchBase brings the remote-tracking base branch, and the PR's target
// when it is another branch, up to date in the repository that holds the
// issue: each at most every baseFetchEvery unless force, within
// baseFetchTimeout. A fetch that fails is logged: the items then compare
// with what the last fetch brought.
func (s *Service) fetchBase(ctx context.Context, in headInputs, force bool) {
	for _, branch := range slices.Compact([]string{in.baseBranch, in.target}) {
		if !s.fetchDue(fetchKey(in.dir, in.baseRemote, branch), force) {
			continue
		}
		if err := s.fetchBranch(ctx, in.dir, in.baseRemote, branch); err != nil {
			s.deps.Log.Warn("LADO could not fetch the base branch; it compares with the last fetch", "session", in.exec.SessionID,
				"remote", in.baseRemote, "branch", branch, "err", redactedErrorLine(err))
		}
	}
}

// fetchBranch fetches remote's branch into dir within baseFetchTimeout.
func (s *Service) fetchBranch(ctx context.Context, dir, remote, branch string) error {
	ctx, cancel := context.WithTimeout(ctx, baseFetchTimeout)
	defer cancel()
	return s.deps.Git.Fetch(ctx, dir, remote, branch)
}

// fetchKey names one branch of one remote of a repository.
func fetchKey(dir, remote, branch string) string {
	return dir + "\x00" + remote + "\x00" + branch
}

// fetchDue reports whether the branch at key is to be fetched now, and if
// so notes the fetch: always with force, else when the last fetch is
// baseFetchEvery old or older.
func (s *Service) fetchDue(key string, force bool) bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.baseFetched[key]; ok && !force && now.Sub(last) < baseFetchEvery {
		return false
	}
	for other, at := range s.baseFetched {
		if now.Sub(at) >= baseFetchEvery {
			delete(s.baseFetched, other)
		}
	}
	s.baseFetched[key] = now
	return true
}

// remoteTip returns the commit of the remote-tracking target branch, empty
// when git has no such ref.
func (s *Service) remoteTip(ctx context.Context, in headInputs) (string, error) {
	tip, err := s.deps.Git.RevParse(ctx, in.dir, "refs/remotes/"+in.baseRemote+"/"+in.target)
	if errors.Is(err, ErrRefNotFound) {
		return "", nil
	}
	return tip, err
}

// verifyingItem is an item LADO could not compute this time: it waits.
func verifyingItem(kind domain.LadoVerifyKind) domain.LadoVerifyItem {
	return domain.LadoVerifyItem{Kind: kind, Outcome: domain.LadoVerifyWait, Code: domain.LadoValidatingVerifying, Details: json.RawMessage("{}")}
}

// branch reads where the head's branch left the target and the commits
// since.
func (s *Service) branch(ctx context.Context, in headInputs) branchFacts {
	if in.headMissing {
		return branchFacts{err: errHeadUnavailable}
	}
	base, err := s.mergeBase(ctx, in)
	if err != nil {
		return branchFacts{err: err}
	}
	commits, err := s.deps.Git.Commits(ctx, in.dir, base, in.head)
	if err != nil {
		return branchFacts{err: err}
	}
	return branchFacts{base: base, commits: commits}
}

// mergeBase returns where the head's branch left its target: the merge-base
// of the head and the remote-tracking target branch, or of the head and the
// base tip AO last observed on the host, whichever is newer. The loop never
// fetches, so the remote-tracking branch can be older than the host's, as
// after the host's Update branch merged the target into the head; its
// merge-base would count the target's newer commits as the worker's (review
// of #31).
func (s *Service) mergeBase(ctx context.Context, in headInputs) (string, error) {
	tracking, trackingErr := s.deps.Git.MergeBase(ctx, in.dir, "refs/remotes/"+in.baseRemote+"/"+in.target, in.head)
	observed, observedErr := s.observedMergeBase(ctx, in)
	// Either alone counts when the other cannot be computed.
	haveTracking, haveObserved := trackingErr == nil, observedErr == nil && observed != ""
	switch {
	case haveTracking && haveObserved && observed != tracking:
		return s.newerBase(ctx, in.dir, in.head, tracking, observed)
	case haveTracking:
		return tracking, nil
	case haveObserved:
		return observed, nil
	}
	return "", errors.Join(trackingErr, observedErr)
}

// observedMergeBase returns the merge-base of the head and the base tip AO
// last observed for the PR; nothing when AO has none.
func (s *Service) observedMergeBase(ctx context.Context, in headInputs) (string, error) {
	pr, ok, err := s.deps.Store.GetPR(ctx, in.exec.PR.URL)
	if err != nil || !ok || pr.BaseSHA == "" {
		return "", err
	}
	return s.deps.Git.MergeBase(ctx, in.dir, pr.BaseSHA, in.head)
}

// newerBase returns the one of two merge-bases of head that descends from
// the other or, when neither does, as after a force push of the target, the
// one fewer commits from head.
func (s *Service) newerBase(ctx context.Context, dir, head, a, b string) (string, error) {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		older, err := s.deps.Git.IsAncestor(ctx, dir, pair[0], pair[1])
		if err != nil {
			return "", err
		}
		if older {
			return pair[1], nil
		}
	}
	fromA, err := s.deps.Git.Commits(ctx, dir, a, head)
	if err != nil {
		return "", err
	}
	fromB, err := s.deps.Git.Commits(ctx, dir, b, head)
	if err != nil {
		return "", err
	}
	if len(fromB) < len(fromA) {
		return b, nil
	}
	return a, nil
}

// commandsItem computes the commands item from the head's runs, and first
// starts the head's next check command when the worktree is ready and the
// daemon's slot is free.
func (s *Service) commandsItem(ctx context.Context, in headInputs, runs []domain.LadoCheckRun, named bool, opts verifyOptions, ev *evaluation) (domain.LadoVerifyItem, error) {
	due := CommandsToRun(in.commands, runs)
	code := ""
	if len(due) > 0 {
		// After a run, the worktree may hold what the run left, such as build
		// output, and nothing else.
		code = WorktreeReady(ctx, s.deps.Git, in.worktree, in.head, runs)
		if code == "" && opts.startWork {
			if run, ok := s.startCommand(ctx, in, due[0]); ok {
				runs, due = append(slices.Clip(runs), run), due[1:]
			}
		}
	}
	ev.runs = runs
	ev.commandsSettled = (len(due) == 0 || code != "") &&
		!slices.ContainsFunc(runs, func(run domain.LadoCheckRun) bool { return run.Status == domain.LadoCheckRunRunning })
	return EvaluateCommands(CommandsInput{Commands: in.commands, Runs: runs, RequiredNamed: named, WorktreeCode: code})
}

// startCommand starts command on the head in the execution's worktree when
// the daemon's one check command slot is free, and returns its run. The
// command runs on under ctx; its end and its output, pinned as the run's
// log, are recorded, the slot freed and a pass kicked.
func (s *Service) startCommand(ctx context.Context, in headInputs, command domain.LadoCheckCommand) (domain.LadoCheckRun, bool) {
	s.mu.Lock()
	if s.commandRunning || !s.leftoversCancelled {
		s.mu.Unlock()
		return domain.LadoCheckRun{}, false
	}
	s.commandRunning = true
	s.mu.Unlock()
	run := domain.LadoCheckRun{
		ID:                 "lado-check-" + s.deps.NewID(),
		ExecutionSessionID: in.exec.SessionID,
		HeadSHA:            in.head,
		Name:               command.Name,
		Command:            command.Command,
		Status:             domain.LadoCheckRunRunning,
		StartedAt:          s.now(),
	}
	if err := s.deps.Store.StartLadoCheckRun(ctx, run); err != nil {
		s.freeSlot()
		s.deps.Log.Warn("LADO could not record a check command run; it tries again", "session", in.exec.SessionID, "command", command.Name, "err", err)
		return domain.LadoCheckRun{}, false
	}
	spec := CommandSpec{Dir: in.worktree, Command: command.Command, Env: CommandEnv(s.deps.Env(), in.env), Timeout: in.timeout}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.Kick()
		defer s.freeSlot()
		s.finishCommand(ctx, run, spec.Dir, s.deps.Run(ctx, spec))
	}()
	return run, true
}

func (s *Service) freeSlot() {
	s.mu.Lock()
	s.commandRunning = false
	s.mu.Unlock()
}

// finishCommand records how a check command run ended, with its output as
// the run's log and the status of the worktree at dir after it, which the
// next command on the head compares with (WorktreeReady). It records even
// when ctx is done, as when the daemon stops: a run left running would read
// as one going on until the next start.
func (s *Service) finishCommand(ctx context.Context, run domain.LadoCheckRun, dir string, result CommandResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	worktree := ""
	if status, err := s.deps.Git.Status(ctx, dir); err == nil {
		worktree = domain.LadoWorktreeStatusSHA256(status)
	} else {
		s.deps.Log.Warn("LADO could not read the worktree after a check command", "session", run.ExecutionSessionID, "command", run.Name, "err", err)
	}
	logSHA256 := ""
	if s.deps.Blobs != nil {
		sum, err := s.deps.Blobs.Put(result.Output)
		if err != nil {
			s.deps.Log.Warn("LADO could not keep a check command's output", "session", run.ExecutionSessionID, "command", run.Name, "err", err)
		}
		logSHA256 = sum
	}
	message := ""
	if result.Err != nil {
		message = redactedErrorLine(result.Err)
	}
	if err := s.deps.Store.FinishLadoCheckRun(ctx, run.ID, result.Status, result.ExitCode, logSHA256, message, worktree, s.now()); err != nil {
		s.deps.Log.Warn("LADO could not record how a check command ended", "session", run.ExecutionSessionID, "command", run.Name, "run", run.ID, "err", err)
	}
}

// reviewItem computes the review item and, with opts.startWork, starts a
// review round or cancels a stalled one as the item asks.
func (s *Service) reviewItem(ctx context.Context, in headInputs, opts verifyOptions, ev *evaluation) (domain.LadoVerifyItem, error) {
	review := ReviewInput{
		Required: in.plan.ReviewRequired,
		Reviewer: in.plan.Reviewer,
		PRURL:    in.exec.PR.URL,
		HeadSHA:  in.head,
		Ready:    ReviewReady(ev.items),
		Now:      s.now(),
	}
	if review.Required {
		if s.deps.Reviews == nil {
			return domain.LadoVerifyItem{}, errors.New("no review service is wired")
		}
		var err error
		if review.Rounds, err = s.deps.Store.ListLadoExecutionReviews(ctx, in.exec.SessionID); err != nil {
			return domain.LadoVerifyItem{}, fmt.Errorf("read the review rounds: %w", err)
		}
		if review.Runs, err = s.deps.Reviews.Runs(ctx, in.exec.SessionID); err != nil {
			return domain.LadoVerifyItem{}, fmt.Errorf("read the review runs: %w", err)
		}
		ev.reviewRuns = review.Runs
	}
	item, action, err := EvaluateReview(review)
	if err != nil {
		return domain.LadoVerifyItem{}, err
	}
	if opts.startWork {
		s.reviewAction(ctx, in, review.Rounds, action)
	}
	return item, nil
}

// reviewAction starts a review round, recording each run it created as the
// execution's next round, or cancels a stalled LADO round. What fails is
// logged: the next pass reads the review service's runs again.
func (s *Service) reviewAction(ctx context.Context, in headInputs, rounds []domain.LadoExecutionReview, action ReviewAction) {
	session := in.exec.SessionID
	switch {
	case action.Trigger:
		ids, err := s.deps.Reviews.Trigger(ctx, session, in.exec.PR.URL, action.Harness)
		if err != nil {
			s.deps.Log.Warn("LADO could not start a review", "session", session, "head", in.head, "err", err)
			return
		}
		for i, id := range ids {
			round := domain.LadoExecutionReview{ReviewRunID: id, ExecutionSessionID: session, HeadSHA: in.head, Round: len(rounds) + 1 + i, CreatedAt: s.now()}
			if err := s.deps.Store.AddLadoExecutionReview(ctx, round); err != nil {
				s.deps.Log.Warn("LADO could not record a review round", "session", session, "run", id, "err", err)
			}
		}
	case action.CancelRunID != "":
		if err := s.deps.Reviews.Cancel(ctx, session); err != nil {
			s.deps.Log.Warn("LADO could not cancel a stalled review", "session", session, "run", action.CancelRunID, "err", err)
			return
		}
		if err := s.deps.Store.MarkLadoExecutionReviewCancelled(ctx, action.CancelRunID, s.now()); err != nil {
			s.deps.Log.Warn("LADO could not record a cancelled review", "session", session, "run", action.CancelRunID, "err", err)
		}
	}
}

// evidenceItem pins the session's newest evidence file and checks it against
// the head's records. A file whose bytes do not match AO's record of it, or
// that cannot be parsed, counts as none.
func (s *Service) evidenceItem(ctx context.Context, in headInputs, checks []domain.PullRequestCheck, commits []Commit, ev *evaluation) (domain.LadoVerifyItem, error) {
	var ref *EvidenceFileRef
	var file *EvidenceFile
	if s.deps.Artifacts != nil && s.deps.Blobs != nil {
		pinned, data, err := PinEvidence(ctx, s.deps.Artifacts, s.deps.Blobs, in.exec.SessionID)
		switch {
		case errors.Is(err, ErrEvidenceHashMismatch):
			s.deps.Log.Warn("LADO ignored an evidence file that does not match AO's record of it", "session", in.exec.SessionID, "err", err)
		case err != nil:
			return domain.LadoVerifyItem{}, err
		default:
			ref = pinned
		}
		if data != nil {
			if parsed, err := ParseEvidence(data); err == nil {
				file = &parsed
			}
		}
	}
	shas := make([]string, len(commits))
	for i, commit := range commits {
		shas[i] = commit.SHA
	}
	links, item, err := ResolveEvidence(EvidenceInput{
		File:          file,
		FileRef:       ref,
		CriteriaCount: len(in.plan.AcceptanceCriteria),
		HeadSHA:       in.head,
		Checks:        checks,
		Commands:      in.commands,
		Runs:          ev.runs,
		Commits:       shas,
	})
	if err != nil {
		return domain.LadoVerifyItem{}, err
	}
	ev.links, ev.resolved = links, true
	return item, nil
}

// requestPull asks the worker once per head to pull its branch when its
// worktree is behind a head made on the host: the head holds the worktree's
// commit and is another one. The check commands run only in a worktree at
// the head. The notified list records the message (outside the hash).
func (s *Service) requestPull(ctx context.Context, in headInputs, notified []string) []string {
	if s.deps.Sender == nil || !in.live || in.worktree == "" || in.headMissing || slices.Contains(notified, domain.LadoNotifiedWorktreeBehind) {
		return notified
	}
	current, err := s.deps.Git.RevParse(ctx, in.worktree, "HEAD")
	if err != nil || strings.EqualFold(current, in.head) {
		return notified
	}
	if behind, err := s.deps.Git.IsAncestor(ctx, in.worktree, current, in.head); err != nil || !behind {
		return notified
	}
	message := fmt.Sprintf("LADO checks head %s of your pull request, which was made on the host, for example by Update branch or an applied suggestion. "+
		"Your worktree is at %s, which the head builds on. Pull your branch so that your worktree is at the head: "+
		"LADO runs the project's check commands there. If you change anything after that, push again.", shortCommit(in.head), shortCommit(current))
	if err := s.deps.Sender.Send(ctx, in.owner, message); err != nil {
		s.deps.Log.Warn("LADO could not ask a worker to pull its branch; it tries again", "session", in.owner, "head", in.head, "err", err)
		return notified
	}
	return append(slices.Clone(notified), domain.LadoNotifiedWorktreeBehind)
}

// sameItem reports whether both lists hold the same item of kind.
func sameItem(a, b []domain.LadoVerifyItem, kind domain.LadoVerifyKind) bool {
	isKind := func(item domain.LadoVerifyItem) bool { return item.Kind == kind }
	i, j := slices.IndexFunc(a, isKind), slices.IndexFunc(b, isKind)
	return i >= 0 && j >= 0 && a[i].Outcome == b[j].Outcome && a[i].Code == b[j].Code && bytes.Equal(a[i].Details, b[j].Details)
}
