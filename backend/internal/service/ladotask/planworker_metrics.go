package ladotask

// What making a revision took (docs/LJY-PRD/TDD.md §5.1, decision 5a of the
// plan worker plan): the time of the design, the critique and the revision
// turn, each plan worker session's tokens from AO's usage records, and the
// critique's findings with the plan's answers. LADO records them when it pins
// a plan worker's revision and refreshes the tokens when it stops the
// sessions, since AO reads usage from transcripts a little later.

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladoverify"
)

// reviewProblems adds the review section's problems to a plan worker's plan
// and counts its answers to the critique's P1 and major findings. Only a
// request that had a review step is checked.
func (s *Service) reviewProblems(ctx context.Context, req domain.LadoPlanRequest, plan domain.LadoPlan) ([]domain.LadoPlanProblem, int, int) {
	if !req.IsWorker() || req.FeedbackSentAt.IsZero() {
		return nil, 0, 0
	}
	critique, _ := s.lastCritique(ctx, req.ID)
	var findings []domain.LadoPlanFinding
	if critique.State == domain.LadoCritiqueDone {
		findings = critique.Findings
	}
	return domain.CheckLadoPlanReview(plan.Body, findings)
}

// lastCritique returns a request's newest review.
func (s *Service) lastCritique(ctx context.Context, id domain.LadoPlanRequestID) (domain.LadoPlanCritique, []domain.LadoPlanCritique) {
	critiques, err := s.deps.Store.ListLadoPlanCritiques(ctx, id)
	if err != nil || len(critiques) == 0 {
		return domain.LadoPlanCritique{}, critiques
	}
	return critiques[len(critiques)-1], critiques
}

// recordMetrics stores what making a plan worker's revision took.
func (s *Service) recordMetrics(ctx context.Context, req domain.LadoPlanRequest, rev domain.LadoPlanRevision, plan domain.LadoPlan, addressed, notAddressed int) {
	if !req.IsWorker() {
		return
	}
	latest, critiques := s.lastCritique(ctx, req.ID)
	now := s.deps.Clock().UTC()
	m := domain.LadoPlanRevisionMetrics{
		RevisionID: rev.ID, RequestID: req.ID,
		Design:    span(req.WorkerStartedAt, req.DraftSubmittedAt),
		Revise:    span(req.FeedbackSentAt, req.SubmittedAt),
		Review:    reviewOf(latest),
		Addressed: addressed, NotAddressed: notAddressed,
		UpdatedAt: now,
	}
	if latest.State == domain.LadoCritiqueDone {
		m.P1, m.Major, m.Minor = latest.Counts()
	}
	for _, c := range critiques {
		if c.Attempt > 0 && !c.FinishedAt.IsZero() {
			m.Critique += span(c.StartedAt, c.FinishedAt)
		}
	}
	settings, err := s.deps.Store.LadoDispatchSettings(ctx)
	if err != nil {
		settings = domain.DefaultLadoDispatchSettings()
	}
	m.ChangeKinds = domain.DecideLadoPlanReview(plan, settings.PlanReview, ladoverify.MatchPath).Kinds
	m.Sessions = s.planUsage(ctx, req, critiques)
	if err := s.deps.Store.SetLadoPlanRevisionMetrics(ctx, m); err != nil {
		s.deps.Log.Warn("LADO could not record a revision's metrics", "revision", rev.ID, "err", err)
	}
}

// refreshMetrics reads the sessions' usage again for the request's newest
// revision, once its sessions stopped.
func (s *Service) refreshMetrics(ctx context.Context, req domain.LadoPlanRequest) {
	revisions, err := s.deps.Store.ListLadoPlanRevisions(ctx, req.TaskID)
	if err != nil {
		return
	}
	for i := len(revisions) - 1; i >= 0; i-- {
		if revisions[i].RequestID != req.ID {
			continue
		}
		m, ok, err := s.deps.Store.LadoPlanRevisionMetrics(ctx, revisions[i].ID)
		if err != nil || !ok {
			return
		}
		_, critiques := s.lastCritique(ctx, req.ID)
		m.Sessions = s.planUsage(ctx, req, critiques)
		m.UpdatedAt = s.deps.Clock().UTC()
		if err := s.deps.Store.SetLadoPlanRevisionMetrics(ctx, m); err != nil {
			s.deps.Log.Warn("LADO could not refresh a revision's metrics", "revision", m.RevisionID, "err", err)
		}
		return
	}
}

// planUsage reads each plan worker session's usage.
func (s *Service) planUsage(ctx context.Context, req domain.LadoPlanRequest, critiques []domain.LadoPlanCritique) []domain.LadoPlanSessionUsage {
	var out []domain.LadoPlanSessionUsage
	add := func(id domain.SessionID, role domain.LadoPlanRole) {
		if id == "" {
			return
		}
		usage, err := s.deps.Store.LadoSessionUsage(ctx, id)
		if err != nil {
			s.deps.Log.Debug("LADO could not read a plan worker's usage", "session", id, "err", err)
			usage = domain.LadoPlanSessionUsage{SessionID: id}
		}
		usage.Role = role
		out = append(out, usage)
	}
	add(req.WorkerSession(), domain.LadoPlanRoleDesigner)
	for _, c := range critiques {
		add(c.SessionID, domain.LadoPlanRoleCritic)
	}
	return out
}

// reviewOf says how a review went.
func reviewOf(c domain.LadoPlanCritique) domain.LadoPlanReview {
	switch c.State {
	case domain.LadoCritiqueDone:
		return domain.LadoPlanReviewDone
	case domain.LadoCritiqueSkipped:
		return domain.LadoPlanReviewSkipped
	case domain.LadoCritiqueFailed:
		return domain.LadoPlanReviewFailed
	default:
		return domain.LadoPlanReviewNone
	}
}

// span is to minus from, zero when either is unknown or out of order.
func span(from, to time.Time) time.Duration {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return 0
	}
	return to.Sub(from)
}
