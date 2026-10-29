// Package ladonotify tells each LADO project's orchestrator what happened to
// its tasks, one line per event (docs/LJY-PRD/TDD.md §5.1): a plan request
// taken, a revision pinned, a plan request that failed, a run that started, a
// pull request that opened, work that completed and a run that failed. The
// orchestrator relays them to the developer; plan text never goes with them.
//
// The service reads LADO's stored facts on the plan loop's timer and keeps one
// notice per
// event in an outbox, keyed so a fact read again adds nothing. Events from
// before the outbox existed are never told. Notices wait while the
// orchestrator works, and go together in one message once it is idle; LADO
// never starts an orchestrator for them, and a notice nobody takes within a
// day is dropped.
package ladonotify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// quietFor is how long no new notice must have come before LADO sends,
	// so a burst of events goes in one message; oldestWaits sends anyway.
	quietFor    = 10 * time.Second
	oldestWaits = time.Minute
)

// Store keeps the outbox and reads the facts notices tell of.
type Store interface {
	LadoNoticeSince(ctx context.Context) (time.Time, error)
	AddLadoOrchestratorNotice(ctx context.Context, n domain.LadoOrchestratorNotice) (bool, error)
	ListWaitingLadoOrchestratorNotices(ctx context.Context) ([]domain.LadoOrchestratorNotice, error)
	MarkLadoOrchestratorNoticesSent(ctx context.Context, ids []int64, to domain.SessionID, at time.Time) error
	DropLadoOrchestratorNotices(ctx context.Context, ids []int64, at time.Time) error
	ListLadoPlanRequestNoticeFacts(ctx context.Context, since time.Time) ([]domain.LadoPlanRequestNoticeFact, error)
	ListLadoPlanRevisionNoticeFacts(ctx context.Context, since time.Time) ([]domain.LadoPlanRevisionNoticeFact, error)
	ListLadoProjectsWithRunsSince(ctx context.Context, since time.Time) ([]domain.ProjectID, error)
	ListLadoTasks(ctx context.Context, projectID domain.ProjectID, includeCancelled bool) ([]domain.LadoTask, error)
}

// Orchestrators finds a project's live orchestrator, without starting one, and
// messages it.
type Orchestrators interface {
	// Orchestrator returns the project's newest live orchestrator and its
	// activity; ok is false when the project has none.
	Orchestrator(ctx context.Context, projectID domain.ProjectID) (id domain.SessionID, activity domain.ActivityState, ok bool, err error)
	// Send delivers a message from the daemon.
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// Deps wires a Service.
type Deps struct {
	Store         Store
	Orchestrators Orchestrators
	// Clock stamps notices; nil uses time.Now.
	Clock func() time.Time
	Log   *slog.Logger
}

// Service collects and sends notices. LADO's plan loop runs Tick on its timer
// (service/ladotask).
type Service struct {
	deps Deps
	// queued holds the keys this process queued or found queued, so a fact
	// read again on the next tick costs no write.
	mu     sync.Mutex
	queued map[string]bool
}

// New returns a Service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps, queued: map[string]bool{}}
}

// Tick adds a notice for each event since notices started, then sends each
// project's waiting notices to its orchestrator when it is idle.
func (s *Service) Tick(ctx context.Context) error {
	since, err := s.deps.Store.LadoNoticeSince(ctx)
	if err != nil {
		return err
	}
	now := s.deps.Clock().UTC()
	return errors.Join(s.collect(ctx, since, now), s.send(ctx, now))
}

// collect adds a notice for each event the stored facts show since a time.
func (s *Service) collect(ctx context.Context, since, now time.Time) error {
	var problems []error
	requests, err := s.deps.Store.ListLadoPlanRequestNoticeFacts(ctx, since)
	if err != nil {
		problems = append(problems, err)
	}
	for _, req := range requests {
		if req.Kind != domain.LadoPlanRequestWorker {
			// A request made before plan workers went to the orchestrator
			// itself.
			continue
		}
		n := issueNumber(req.IssueRef)
		if !req.RequestedAt.Before(since) {
			s.add(ctx, req.ProjectID, "request:"+string(req.ID), domain.LadoNoticeRequested(n), req.RequestedAt, now)
		}
		if !req.FailedAt.IsZero() && !req.FailedAt.Before(since) && req.FailureCode != domain.LadoPlanRequestReplaced {
			s.add(ctx, req.ProjectID, "plan_failed:"+string(req.ID), domain.LadoNoticePlanFailed(n, req.FailureCode), req.FailedAt, now)
		}
	}
	revisions, err := s.deps.Store.ListLadoPlanRevisionNoticeFacts(ctx, since)
	if err != nil {
		problems = append(problems, err)
	}
	for _, rev := range revisions {
		key := fmt.Sprintf("revision:%s:%d", rev.TaskID, rev.Number)
		s.add(ctx, rev.ProjectID, key, domain.LadoNoticeRevision(issueNumber(rev.IssueRef), rev.Number), rev.CreatedAt, now)
	}
	projects, err := s.deps.Store.ListLadoProjectsWithRunsSince(ctx, since)
	if err != nil {
		problems = append(problems, err)
	}
	for _, project := range projects {
		tasks, err := s.deps.Store.ListLadoTasks(ctx, project, true)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		for _, task := range tasks {
			s.collectRun(ctx, task, since, now)
		}
	}
	return errors.Join(problems...)
}

// collectRun adds the notices of a task's run: its start, its PR, and its
// end when it completed or failed. A queue entry that ran out of attempts
// failed without a run.
func (s *Service) collectRun(ctx context.Context, task domain.LadoTask, since, now time.Time) {
	n := issueNumber(task.IssueRef)
	if q := task.Queue; q.ID != "" && !q.FailedAt.IsZero() && !q.FailedAt.Before(since) {
		s.add(ctx, task.ProjectID, "entry_failed:"+string(q.ID), domain.LadoNoticeRunFailed(n, domain.LadoFailedAttemptsExhausted), q.FailedAt, now)
	}
	e := task.Execution
	if e.SessionID == "" || e.StartedAt.Before(since) {
		return
	}
	run := string(e.SessionID)
	s.add(ctx, task.ProjectID, "started:"+run, domain.LadoNoticeStarted(n), e.StartedAt, now)
	if e.PR.URL != "" {
		s.add(ctx, task.ProjectID, "pr:"+run+":"+e.PR.URL, domain.LadoNoticePROpened(n, e.PR.URL), now, now)
	}
	switch state, reason := e.State(); state {
	case domain.LadoTaskCompleted:
		s.add(ctx, task.ProjectID, "completed:"+run, domain.LadoNoticeCompleted(n), now, now)
	case domain.LadoTaskFailed:
		s.add(ctx, task.ProjectID, "failed:"+run, domain.LadoNoticeRunFailed(n, reason), now, now)
	}
}

// add queues a notice once per key. A key this process already queued, or
// found queued, is not written again.
func (s *Service) add(ctx context.Context, project domain.ProjectID, key, text string, occurred, now time.Time) {
	s.mu.Lock()
	known := s.queued[key]
	s.mu.Unlock()
	if known {
		return
	}
	if _, err := s.deps.Store.AddLadoOrchestratorNotice(ctx, domain.LadoOrchestratorNotice{
		ProjectID: project, Key: key, Text: text, OccurredAt: occurred, CreatedAt: now,
	}); err != nil {
		s.deps.Log.Warn("LADO could not queue a notice", "project", project, "key", key, "err", err)
		return
	}
	s.mu.Lock()
	s.queued[key] = true
	s.mu.Unlock()
}

// send drops the notices nobody can take any more, then sends each project's
// waiting notices together to its idle orchestrator.
func (s *Service) send(ctx context.Context, now time.Time) error {
	waiting, err := s.deps.Store.ListWaitingLadoOrchestratorNotices(ctx)
	if err != nil {
		return err
	}
	byProject := map[domain.ProjectID][]domain.LadoOrchestratorNotice{}
	var projects []domain.ProjectID
	for _, n := range waiting {
		if _, ok := byProject[n.ProjectID]; !ok {
			projects = append(projects, n.ProjectID)
		}
		byProject[n.ProjectID] = append(byProject[n.ProjectID], n)
	}
	var problems []error
	for _, project := range projects {
		notices, dropped := keep(byProject[project], now)
		if len(dropped) > 0 {
			if err := s.deps.Store.DropLadoOrchestratorNotices(ctx, dropped, now); err != nil {
				problems = append(problems, err)
			}
		}
		if len(notices) == 0 || !due(notices, now) {
			continue
		}
		id, activity, ok, err := s.deps.Orchestrators.Orchestrator(ctx, project)
		if err != nil {
			problems = append(problems, fmt.Errorf("find the orchestrator of %s: %w", project, err))
			continue
		}
		if !ok || (activity != domain.ActivityIdle && activity != domain.ActivityWaitingInput) {
			continue
		}
		lines := make([]string, 0, len(notices))
		ids := make([]int64, 0, len(notices))
		for _, n := range notices {
			lines = append(lines, n.Text)
			ids = append(ids, n.ID)
		}
		if err := s.deps.Orchestrators.Send(ctx, id, strings.Join(lines, "\n")); err != nil {
			s.deps.Log.Debug("LADO could not send notices; it tries again", "project", project, "orchestrator", id, "err", err)
			continue
		}
		if err := s.deps.Store.MarkLadoOrchestratorNoticesSent(ctx, ids, id, now); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// keep splits a project's waiting notices, oldest first, into those still to
// send and those to drop: older than a day, or beyond the newest
// LadoNoticeMaxPending.
func keep(notices []domain.LadoOrchestratorNotice, now time.Time) ([]domain.LadoOrchestratorNotice, []int64) {
	sort.SliceStable(notices, func(i, j int) bool { return notices[i].OccurredAt.Before(notices[j].OccurredAt) })
	var kept []domain.LadoOrchestratorNotice
	var dropped []int64
	for _, n := range notices {
		if now.Sub(n.CreatedAt) > domain.LadoNoticeKeep {
			dropped = append(dropped, n.ID)
			continue
		}
		kept = append(kept, n)
	}
	if extra := len(kept) - domain.LadoNoticeMaxPending; extra > 0 {
		for _, n := range kept[:extra] {
			dropped = append(dropped, n.ID)
		}
		kept = kept[extra:]
	}
	return kept, dropped
}

// due reports whether notices should go now: no new one came for quietFor,
// or the oldest has waited oldestWaits.
func due(notices []domain.LadoOrchestratorNotice, now time.Time) bool {
	newest, oldest := notices[0].CreatedAt, notices[0].CreatedAt
	for _, n := range notices {
		if n.CreatedAt.After(newest) {
			newest = n.CreatedAt
		}
		if n.CreatedAt.Before(oldest) {
			oldest = n.CreatedAt
		}
	}
	return now.Sub(newest) >= quietFor || now.Sub(oldest) >= oldestWaits
}

// issueNumber returns the issue number of a canonical issue id, 0 when it
// has none.
func issueNumber(ref domain.IssueID) int {
	id, err := domain.ParseLadoIssueRef(ref)
	if err != nil {
		return 0
	}
	return domain.LadoIssueNumber(id)
}
