package ladotask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// defaultPlanInterval is how often LADO looks for new plan versions and
	// retries deliveries. AO records an artifact version 30 to 60 s after
	// the file settles, so a shorter interval would find nothing more.
	defaultPlanInterval = 10 * time.Second
	// defaultDeliveryTimeout is how long LADO keeps delivering a request to
	// an orchestrator that cannot take it yet, such as one still starting.
	defaultDeliveryTimeout = 10 * time.Minute
)

// WatchPlans delivers the plan requests still waiting for their orchestrator
// and pins every new version of a requested plan file as the task's next
// revision (docs/LJY-PRD/TDD.md §5.1). It works only from stored facts, so a
// restart picks up where it stopped.
func (s *Service) WatchPlans(ctx context.Context) {
	requests, err := s.deps.Store.ListLadoPlanRequestsForLiveTasks(ctx)
	if err != nil {
		s.deps.Log.Warn("LADO could not list plan requests", "err", err)
		return
	}
	byTask := map[domain.LadoTaskID][]domain.LadoPlanRequest{}
	var order []domain.LadoTaskID
	for _, req := range requests {
		if _, ok := byTask[req.TaskID]; !ok {
			order = append(order, req.TaskID)
		}
		byTask[req.TaskID] = append(byTask[req.TaskID], req)
	}
	for _, id := range order {
		if ctx.Err() != nil {
			return
		}
		task, ok, err := s.deps.Store.LadoTask(ctx, id)
		if err != nil || !ok {
			continue
		}
		s.deliverWaiting(ctx, task, byTask[id])
		s.pinPlans(ctx, task, byTask[id])
	}
}

// deliverWaiting retries the task's newest request to an orchestrator while
// it waits, and gives up older ones a newer request replaced. Requests made
// before plan workers are the only ones an orchestrator gets.
func (s *Service) deliverWaiting(ctx context.Context, task domain.LadoTask, requests []domain.LadoPlanRequest) {
	now := s.deps.Clock().UTC()
	for i, req := range requests {
		if req.IsWorker() || !req.DeliveredAt.IsZero() || !req.FailedAt.IsZero() {
			continue
		}
		if i < len(requests)-1 {
			s.failRequest(ctx, req, now, "a newer plan request replaced it")
			continue
		}
		if now.Sub(req.RequestedAt) > s.deps.DeliveryTimeout {
			s.failRequest(ctx, req, now, fmt.Sprintf("the orchestrator did not take the request within %s", s.deps.DeliveryTimeout))
			continue
		}
		orchestrator, ok, err := s.deps.Orchestrators.Session(ctx, req.SessionID)
		if err != nil {
			continue
		}
		if !ok || orchestrator.Terminated {
			s.failRequest(ctx, req, now, ErrOrchestratorGone.Error())
			continue
		}
		snapshot, err := s.snapshotOf(req)
		if err != nil {
			s.deps.Log.Warn("LADO could not read a pinned issue snapshot", "request", req.ID, "err", err)
			continue
		}
		var basedOn domain.LadoPlanRevision
		if req.BasedOn != "" {
			basedOn, _, _ = s.deps.Store.LadoPlanRevision(ctx, req.BasedOn)
		}
		s.deliver(ctx, task, req, snapshot, orchestrator, basedOn)
	}
}

func (s *Service) failRequest(ctx context.Context, req domain.LadoPlanRequest, at time.Time, reason string) {
	if err := s.deps.Store.FailLadoPlanRequest(ctx, req.ID, at, reason); err != nil {
		s.deps.Log.Warn("LADO could not record a failed plan request", "request", req.ID, "err", err)
	}
}

// pinPlans pins the versions of the task's plan file that are not revisions
// yet, oldest first, in every session the task's requests went to: the
// orchestrators of requests made before plan workers, and each designer that
// submitted a final plan. Of a designer's versions, only the plan it
// submitted last is pinned.
func (s *Service) pinPlans(ctx context.Context, task domain.LadoTask, requests []domain.LadoPlanRequest) {
	path := domain.LadoPlanPath(task.ID)
	seen := map[domain.SessionID]bool{}
	for _, req := range requests {
		session := req.SessionID
		if req.IsWorker() {
			session = req.WorkerSession()
			if session == "" || req.SubmittedAt.IsZero() {
				continue
			}
		}
		if seen[session] {
			continue
		}
		seen[session] = true
		versions, err := s.deps.Artifacts.Versions(ctx, session, path)
		if err != nil {
			s.deps.Log.Debug("LADO could not list plan versions", "task", task.ID, "session", session, "err", err)
			continue
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
		for _, version := range versions {
			pinned, err := s.deps.Store.LadoArtifactVersionPinned(ctx, version.SessionID, version.Path, int(version.Version), version.SHA256)
			if err != nil {
				break
			}
			if pinned {
				continue
			}
			owner, ok := requestFor(requests, version)
			if !ok || (owner.IsWorker() && version.SHA256 != owner.FinalSHA256) {
				continue
			}
			if err := s.pinVersion(ctx, task, owner, version); err != nil {
				s.deps.Log.Warn("LADO could not pin a plan version; it tries again", "task", task.ID, "version", version.Version, "err", err)
				break
			}
		}
	}
}

// requestFor returns the newest request to the version's session made
// before the version was written. The file's modification time comes first,
// because AO records a version up to a minute after the write.
func requestFor(requests []domain.LadoPlanRequest, version domain.SessionArtifactVersion) (domain.LadoPlanRequest, bool) {
	written := version.ModTime
	if written.IsZero() {
		written = version.CreatedAt
	}
	var owner domain.LadoPlanRequest
	found := false
	for _, req := range requests {
		if req.SessionID == version.SessionID && !req.RequestedAt.After(written) {
			owner, found = req, true
		}
	}
	return owner, found
}

// pinVersion copies one artifact version into LADO's blob store and records
// it as a revision with its problems.
func (s *Service) pinVersion(ctx context.Context, task domain.LadoTask, req domain.LadoPlanRequest, version domain.SessionArtifactVersion) error {
	content, err := s.deps.Artifacts.Read(ctx, version)
	if err != nil {
		return fmt.Errorf("read the artifact version: %w", err)
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != version.SHA256 {
		// The agent can write to AO's version store; a copy that no longer
		// matches AO's record is not the version AO captured.
		s.deps.Log.Warn("LADO skipped a plan version whose bytes do not match AO's record", "task", task.ID, "version", version.Version)
		return nil
	}
	if len(content) <= domain.LadoPlanMaxBytes {
		if _, err := s.deps.Blobs.Put(content); err != nil {
			return fmt.Errorf("pin the plan: %w", err)
		}
	}
	snapshot, err := s.snapshotOf(req)
	if err != nil {
		return err
	}
	comments := map[string]bool{}
	for _, comment := range snapshot.Comments {
		comments[comment.ID] = true
	}
	plan, problems := domain.ParseLadoPlan(content, domain.LadoPlanExpectations{
		Task: task.ID, Issue: task.IssueRef, Comments: comments, SourceComment: req.SourceCommentID,
	})
	baseSHA := ""
	if plan.BaseRef != "" && !hasProblem(problems, "base_ref") {
		dir, err := s.repoDir(ctx, task)
		if err == nil {
			baseSHA, err = s.deps.Git.RevParse(ctx, dir, plan.BaseRef)
		}
		if err != nil {
			problems = append(problems, domain.LadoPlanProblem{Code: domain.LadoPlanUnknownBaseRef, Field: "base_ref", Detail: plan.BaseRef})
		}
	}
	reviewed, addressed, notAddressed := s.reviewProblems(ctx, req, plan)
	problems = append(problems, reviewed...)
	source := string(version.Source)
	if source == "" {
		source = string(domain.ArtifactVersionSourceAgent)
	}
	rev, created, err := s.deps.Store.PinLadoPlanRevision(ctx, domain.LadoPlanRevision{
		ID:                  domain.LadoPlanRevisionID("lado-plan-" + s.deps.NewID()),
		TaskID:              task.ID,
		RequestID:           req.ID,
		SessionID:           version.SessionID,
		Path:                version.Path,
		ArtifactVersion:     int(version.Version),
		Source:              source,
		SHA256:              version.SHA256,
		BaseRef:             plan.BaseRef,
		BaseSHA:             baseSHA,
		IssueSnapshotSHA256: req.IssueSnapshotSHA256,
		Problems:            problems,
		CreatedAt:           s.deps.Clock().UTC(),
	})
	if err != nil {
		return err
	}
	if created {
		s.deps.Log.Info("LADO pinned a plan revision", "task", task.ID, "revision", rev.Number, "problems", len(rev.Problems))
		s.recordMetrics(ctx, req, rev, plan, addressed, notAddressed)
		if req.IsWorker() {
			s.kickPlans()
		}
	}
	return nil
}

func hasProblem(problems []domain.LadoPlanProblem, field string) bool {
	for _, problem := range problems {
		if problem.Field == field {
			return true
		}
	}
	return false
}

// repoDir returns the checkout of the repository that holds the task's
// issue: the project's folder, or the workspace child with that origin.
func (s *Service) repoDir(ctx context.Context, task domain.LadoTask) (string, error) {
	project, err := s.project(ctx, task.ProjectID)
	if err != nil {
		return "", err
	}
	if project.Kind != domain.ProjectKindWorkspace {
		return project.Path, nil
	}
	children, err := s.deps.Projects.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return "", err
	}
	for _, child := range children {
		repo, err := domain.LadoTrackerRepoFromOrigin(child.RepoOriginURL)
		if err == nil && domain.LadoSameRepo(repo, task.Repo) {
			return filepath.Join(project.Path, filepath.FromSlash(child.RelativePath)), nil
		}
	}
	return "", fmt.Errorf("no workspace repository has the origin %s", domain.LadoRepoRef(task.Repo))
}
