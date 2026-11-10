package ladologwatch

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

const (
	// incidentWindow is how long a fingerprint that needs an issue gathers
	// the occurrences of every instance before LADO writes the issue, so the
	// replicas of one service end up in one issue ("n개 중 m개").
	incidentWindow = 60 * time.Second
	// incidentSaveInterval is how often the counts of a fingerprint are
	// written, so a flood of lines does not flood the change log.
	incidentSaveInterval = 10 * time.Second
	// incidentRetryDelay is how long LADO waits before it tries again to
	// create an issue the tracker or the store failed.
	incidentRetryDelay = 5 * time.Minute
	// incidentIdle is how long a fingerprint with nothing to write stays in
	// memory.
	incidentIdle = time.Hour
	// incidentBurstEvery is how often at most LADO tells of the hourly cap.
	incidentBurstEvery = time.Hour
	// incidentIssueSpan and incidentPlanSpan are the spans the hourly issue
	// cap and the daily plan cap count over.
	incidentIssueSpan = time.Hour
	incidentPlanSpan  = 24 * time.Hour
)

// IncidentStore is what the incident handler keeps in LADO's store.
type IncidentStore interface {
	LadoLogFingerprint(ctx context.Context, project domain.ProjectID, fingerprint string) (domain.LadoLogFingerprint, bool, error)
	AddLadoLogOccurrences(ctx context.Context, d domain.LadoLogOccurrenceDelta) (domain.LadoLogFingerprint, error)
	LinkLadoLogFingerprintTask(ctx context.Context, id domain.LadoLogFingerprintID, task domain.LadoTaskID, issueRef, previousIssueRef domain.IssueID, at time.Time) error
	ClearLadoLogFingerprintTask(ctx context.Context, id domain.LadoLogFingerprintID) error
	SetLadoLogAutoPlan(ctx context.Context, id domain.LadoLogFingerprintID, requestedAt time.Time, held domain.LadoLogAutoPlanHold) error
	CountLadoLogIssuesSince(ctx context.Context, project domain.ProjectID, since time.Time) (int, error)
	CountLadoLogAutoPlansSince(ctx context.Context, project domain.ProjectID, since time.Time) (int, error)
	SetLadoLogBurstNotified(ctx context.Context, project domain.ProjectID, at time.Time) error
	// LadoTask reads the task a fingerprint leads to.
	LadoTask(ctx context.Context, id domain.LadoTaskID) (domain.LadoTask, bool, error)
}

// IncidentTasks creates the issue and the task of an incident: the
// ladotask service.
type IncidentTasks interface {
	// CreateFromLog returns the pending task along with its error when LADO
	// could not confirm the issue's creation.
	CreateFromLog(ctx context.Context, in ladotask.LogCreateInput) (domain.LadoTask, error)
}

// IncidentPlans requests the plan of an incident's task: the ladotask
// service.
type IncidentPlans interface {
	RequestPlan(ctx context.Context, in ladotask.PlanRequestInput) (domain.LadoPlanRequest, error)
}

// IncidentNotices queues a notice for a project's orchestrator: the store's
// outbox, which service/ladonotify delivers.
type IncidentNotices interface {
	AddLadoOrchestratorNotice(ctx context.Context, n domain.LadoOrchestratorNotice) (bool, error)
}

// HandlerDeps wires an IncidentHandler.
type HandlerDeps struct {
	Store   IncidentStore
	Tasks   IncidentTasks
	Plans   IncidentPlans
	Notices IncidentNotices
	// Clock stamps what Handle records; nil uses time.Now. Flush takes its
	// own time.
	Clock func() time.Time
	Log   *slog.Logger
}

// IncidentHandler turns occurrences into LADO's records, issues, tasks,
// plan requests and notices (docs/LJY-PRD/TDD.md, log watch):
//
//   - A project's occurrences are handled one at a time.
//   - An ignored fingerprint is only counted.
//   - A fingerprint without a live issue gathers occurrences for 60 seconds,
//     then gets an issue and a task, within the hourly issue cap.
//   - A fingerprint with an open issue only raises LADO's counts, written at
//     most every 10 seconds; nothing goes to the tracker.
//   - A fingerprint whose issue was closed or whose task was cancelled gets a
//     new issue that links the old one.
//   - A creation LADO could not confirm is looked up by its task id later;
//     when that task is gone, the fingerprint starts over.
//   - After the task, a plan is requested within the daily cap, and the
//     orchestrator is told.
//
// The windows and unwritten counts live in memory only: a daemon that stops
// in a window creates the issue at the fingerprint's next occurrence.
type IncidentHandler struct {
	deps HandlerDeps

	mu       sync.Mutex
	projects map[domain.ProjectID]*incidentProject
}

// incidentProject is one project's fingerprints in memory. Its lock
// serializes the project's occurrences.
type incidentProject struct {
	mu     sync.Mutex
	states map[string]*incidentState
	// burstNotifiedAt is when LADO last told of the hourly cap, as far as
	// this handler knows; the settings carry the stored value.
	burstNotifiedAt time.Time
}

// incidentState is what the handler holds of one fingerprint.
type incidentState struct {
	settings domain.LadoLogWatchSettings
	// fingerprint, service, kind and location are what the first
	// occurrence said of the fingerprint; kind and location are cleaned.
	fingerprint, service, kind, location string
	// fp is the fingerprint as last read or written; ID 0 before then.
	fp domain.LadoLogFingerprint
	// pending are the occurrences not written yet, by instance.
	pending map[string]*incidentCount
	// recent are the occurrences since the issue decision was last made, by
	// instance; a window shows them in its issue.
	recent map[string]*incidentCount
	// sample is the occurrence the issue shows; serviceInstances the most
	// instances of the service seen attached meanwhile.
	sample           Occurrence
	serviceInstances int
	savedAt          time.Time
	// windowUntil is when the window for a new issue ends, zero without one;
	// previous is the closed issue the new one follows.
	windowUntil time.Time
	previous    domain.IssueID
	retryAfter  time.Time
	touchedAt   time.Time
	// unlinked is a task created for the fingerprint that LADO could not
	// record on its row. It is kept until the link is written, so the
	// fingerprint gets no second issue meanwhile.
	unlinked *incidentUnlinked
}

// incidentUnlinked is a created task not linked to its fingerprint yet.
type incidentUnlinked struct {
	task     domain.LadoTask
	previous domain.IssueID
}

// incidentCount is what one instance showed of a fingerprint.
type incidentCount struct {
	image       string
	count       int
	first, last time.Time
}

// NewIncidentHandler returns an IncidentHandler.
func NewIncidentHandler(deps HandlerDeps) *IncidentHandler {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &IncidentHandler{deps: deps, projects: map[domain.ProjectID]*incidentProject{}}
}

// Handle records one occurrence of a project's incident. The settings are
// the project's current log watch settings. A fingerprint seen for the first
// time since the daemon started is written and decided on at once; later
// occurrences are written by Flush.
func (h *IncidentHandler) Handle(ctx context.Context, project domain.ProjectID, settings domain.LadoLogWatchSettings, o Occurrence) {
	fingerprint := o.Incident.Fingerprint
	if project == "" || fingerprint == "" {
		return
	}
	now := h.deps.Clock().UTC()
	p := h.incidentProject(project)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.burstNotifiedAt.Before(settings.BurstNotifiedAt) {
		p.burstNotifiedAt = settings.BurstNotifiedAt
	}
	st, known := p.states[fingerprint]
	if !known {
		st = &incidentState{
			fingerprint: fingerprint,
			service:     o.Service,
			kind:        CleanKind(o.Incident.ExceptionKind),
			location:    CleanKind(o.Incident.Location),
			pending:     map[string]*incidentCount{},
			recent:      map[string]*incidentCount{},
		}
		p.states[fingerprint] = st
	}
	settings.ProjectID = project
	st.settings = settings
	st.add(o, now)
	if !known {
		h.incidentSync(ctx, p, st, now)
	}
}

// Flush writes the counts gathered for 10 seconds and creates the issues
// whose window ended by now. The runner calls it every few seconds.
func (h *IncidentHandler) Flush(ctx context.Context, now time.Time) {
	now = now.UTC()
	h.mu.Lock()
	ids := make([]domain.ProjectID, 0, len(h.projects))
	for id := range h.projects {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		p := h.incidentProject(id)
		p.mu.Lock()
		keys := make([]string, 0, len(p.states))
		for key := range p.states {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			st := p.states[key]
			switch {
			case !st.windowUntil.IsZero() && !now.Before(st.windowUntil):
				h.incidentSync(ctx, p, st, now)
			case len(st.pending) > 0 && now.Sub(st.savedAt) >= incidentSaveInterval:
				h.incidentSync(ctx, p, st, now)
			case len(st.pending) == 0 && st.windowUntil.IsZero() && st.unlinked == nil && now.Sub(st.touchedAt) >= incidentIdle:
				delete(p.states, key)
			}
		}
		p.mu.Unlock()
	}
}

func (h *IncidentHandler) incidentProject(id domain.ProjectID) *incidentProject {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.projects[id]
	if p == nil {
		p = &incidentProject{states: map[string]*incidentState{}}
		h.projects[id] = p
	}
	return p
}

// add counts one occurrence in memory.
func (st *incidentState) add(o Occurrence, now time.Time) {
	at := o.Incident.At.UTC()
	if o.Incident.At.IsZero() {
		at = now
	}
	for _, counts := range []map[string]*incidentCount{st.pending, st.recent} {
		c := counts[o.Instance]
		if c == nil {
			c = &incidentCount{first: at, last: at}
			counts[o.Instance] = c
		}
		c.count++
		if o.Image != "" {
			c.image = o.Image
		}
		if at.Before(c.first) {
			c.first = at
		}
		if at.After(c.last) {
			c.last = at
		}
	}
	if st.sample.Incident.Fingerprint == "" {
		st.sample = o
	}
	st.serviceInstances = max(st.serviceInstances, o.ServiceInstances)
	st.touchedAt = now
}

// incidentSync writes a fingerprint's unwritten counts and decides what its
// issue needs.
func (h *IncidentHandler) incidentSync(ctx context.Context, p *incidentProject, st *incidentState, now time.Time) {
	if !h.incidentWrite(ctx, st, now) {
		return
	}
	if st.unlinked != nil {
		// A created task waits for its link: write it before anything is
		// decided, and create nothing while it waits.
		u := *st.unlinked
		if h.incidentLink(ctx, st, u.task, u.previous, now) && u.task.IssueRef != "" {
			h.incidentCreated(ctx, st, u.task, u.previous, now)
		}
		st.resetRecent()
		return
	}
	if st.fp.Ignored {
		st.closeWindow()
		return
	}
	if !st.windowUntil.IsZero() {
		if now.Before(st.windowUntil) {
			return
		}
		h.incidentCreate(ctx, p, st, now)
		return
	}
	if now.Before(st.retryAfter) {
		st.resetRecent()
		return
	}
	h.incidentDecide(ctx, st, now)
}

// incidentWrite writes the unwritten counts, or reads the fingerprint when
// there are none. It reports whether the fingerprint is known now.
func (h *IncidentHandler) incidentWrite(ctx context.Context, st *incidentState, now time.Time) bool {
	instances := make([]string, 0, len(st.pending))
	for instance := range st.pending {
		instances = append(instances, instance)
	}
	sort.Strings(instances)
	for _, instance := range instances {
		c := st.pending[instance]
		fp, err := h.deps.Store.AddLadoLogOccurrences(ctx, domain.LadoLogOccurrenceDelta{
			Project:     st.settings.ProjectID,
			Fingerprint: st.fingerprint,
			Service:     st.service,
			Kind:        st.kind,
			Location:    st.location,
			Instance:    instance,
			Image:       c.image,
			Delta:       c.count,
			FirstSeen:   c.first,
			LastSeen:    c.last,
		})
		if err != nil {
			h.deps.Log.Warn("LADO could not record a log incident", "project", st.settings.ProjectID, "err", err)
			return false
		}
		st.fp = fp
		delete(st.pending, instance)
	}
	st.savedAt = now
	if len(instances) > 0 {
		return true
	}
	// Nothing to write: read the fingerprint again, which the developer may
	// have put on the ignore list meanwhile.
	fp, ok, err := h.deps.Store.LadoLogFingerprint(ctx, st.settings.ProjectID, st.fingerprint)
	if err != nil || !ok {
		if err != nil {
			h.deps.Log.Warn("LADO could not read a log fingerprint", "project", st.settings.ProjectID, "err", err)
		}
		return false
	}
	st.fp = fp
	return true
}

// incidentDecide looks at the fingerprint's task and issue: an open issue
// needs nothing but the counts, a missing or closed one opens a window for a
// new issue.
func (h *IncidentHandler) incidentDecide(ctx context.Context, st *incidentState, now time.Time) {
	fp := st.fp
	switch {
	case fp.TaskID == "" && fp.IssueRef == "":
		// Never created, held by the hourly cap, or its task was deleted
		// unconfirmed (task_id NULL).
		st.openWindow(now, "")
		return
	case fp.TaskID == "":
		// The task is gone, the issue stays: treat it as closed.
		st.openWindow(now, fp.IssueRef)
		return
	}
	task, ok, err := h.deps.Store.LadoTask(ctx, fp.TaskID)
	if err != nil {
		h.deps.Log.Warn("LADO could not read a log incident's task", "project", st.settings.ProjectID, "task", fp.TaskID, "err", err)
		st.resetRecent()
		return
	}
	if !ok {
		if fp.IssueRef != "" {
			st.openWindow(now, fp.IssueRef)
			return
		}
		// An unconfirmed creation whose task the tracking loop deleted.
		if err := h.deps.Store.ClearLadoLogFingerprintTask(ctx, fp.ID); err != nil {
			h.deps.Log.Warn("LADO could not clear a log fingerprint's task", "project", st.settings.ProjectID, "err", err)
			st.resetRecent()
			return
		}
		st.fp.TaskID, st.fp.IssueRef = "", ""
		st.openWindow(now, "")
		return
	}
	if fp.IssueRef == "" {
		if task.IssueRef == "" {
			// Still unconfirmed: the tracking loop resolves it.
			st.resetRecent()
			return
		}
		// The tracking loop found the issue: link it, then do what a
		// confirmed creation does.
		if err := h.deps.Store.LinkLadoLogFingerprintTask(ctx, fp.ID, task.ID, task.IssueRef, "", now); err != nil {
			h.deps.Log.Warn("LADO could not link a log incident's issue", "project", st.settings.ProjectID, "err", err)
			st.resetRecent()
			return
		}
		st.fp.IssueRef = task.IssueRef
		if incidentClosed(task) {
			st.openWindow(now, task.IssueRef)
			return
		}
		h.incidentCreated(ctx, st, task, fp.PreviousIssueRef, now)
		st.resetRecent()
		return
	}
	if incidentClosed(task) {
		st.openWindow(now, fp.IssueRef)
		return
	}
	h.incidentLatePlan(ctx, st, task, now)
	st.resetRecent()
}

// incidentClosed reports whether a fingerprint's task no longer stands for
// it: cancelled, or its issue was closed.
func incidentClosed(task domain.LadoTask) bool {
	return !task.CancelledAt.IsZero() || task.Issue.State == domain.IssueDone || task.Issue.State == domain.IssueCancelled
}

func (st *incidentState) openWindow(now time.Time, previous domain.IssueID) {
	st.windowUntil = now.Add(incidentWindow)
	st.previous = previous
}

func (st *incidentState) closeWindow() {
	st.windowUntil = time.Time{}
	st.previous = ""
	st.resetRecent()
}

func (st *incidentState) resetRecent() {
	st.recent = map[string]*incidentCount{}
	st.sample = Occurrence{}
	st.serviceInstances = 0
}

// incidentCreate ends a window: it creates the issue and the task within the
// hourly cap, or records the fingerprint without them.
func (h *IncidentHandler) incidentCreate(ctx context.Context, p *incidentProject, st *incidentState, now time.Time) {
	defer st.closeWindow()
	settings := st.settings
	issueCap := settings.HourlyIssueCap
	if issueCap <= 0 {
		issueCap = domain.LadoLogDefaultHourlyIssueCap
	}
	issues, err := h.deps.Store.CountLadoLogIssuesSince(ctx, settings.ProjectID, now.Add(-incidentIssueSpan))
	if err != nil {
		h.deps.Log.Warn("LADO could not count the log watch's issues", "project", settings.ProjectID, "err", err)
		st.retryAfter = now.Add(incidentRetryDelay)
		return
	}
	if issues >= issueCap {
		h.incidentBurst(ctx, p, settings.ProjectID, issueCap, now)
		return
	}

	title, body := IssueText(h.incidentIssueInput(st))
	task, err := h.deps.Tasks.CreateFromLog(ctx, ladotask.LogCreateInput{
		ProjectID:  settings.ProjectID,
		Repository: settings.Repository,
		Title:      title,
		Body:       body,
	})
	switch {
	case err == nil:
		if h.incidentLink(ctx, st, task, st.previous, now) {
			h.incidentCreated(ctx, st, task, st.previous, now)
		}
	case task.ID != "":
		// The issue may exist while the task waits (unconfirmed, or created
		// but not saved on the task): keep the task id; ResolvePending links
		// or drops it and a later occurrence finds out.
		task.IssueRef = ""
		h.incidentLink(ctx, st, task, st.previous, now)
	default:
		h.deps.Log.Warn("LADO could not create a log incident's issue", "project", settings.ProjectID, "err", err)
		st.retryAfter = now.Add(incidentRetryDelay)
	}
}

// incidentLink records a created task (and its issue, when confirmed) on the
// fingerprint's row. The task exists already, so the write goes on when the
// runner's context ends (the daemon stopping right after the tracker
// answered). When it still fails, the task is kept in memory and linked on
// the fingerprint's next sync, so no second issue is created for it. It
// reports whether the link was written.
func (h *IncidentHandler) incidentLink(ctx context.Context, st *incidentState, task domain.LadoTask, previous domain.IssueID, now time.Time) bool {
	if err := h.deps.Store.LinkLadoLogFingerprintTask(context.WithoutCancel(ctx), st.fp.ID, task.ID, task.IssueRef, previous, now); err != nil {
		h.deps.Log.Error("LADO could not record a log incident's task; it tries again on the next occurrence", "project", st.settings.ProjectID, "task", task.ID, "err", err)
		st.unlinked = &incidentUnlinked{task: task, previous: previous}
		return false
	}
	st.unlinked = nil
	st.fp.TaskID, st.fp.IssueRef, st.fp.PreviousIssueRef = task.ID, task.IssueRef, previous
	st.fp.AutoPlanRequestedAt, st.fp.AutoPlanHeld = time.Time{}, ""
	return true
}

// incidentIssueInput is what the issue of the window's occurrences says.
func (h *IncidentHandler) incidentIssueInput(st *incidentState) IssueInput {
	in := IssueInput{
		Service:          st.service,
		Incident:         st.sample.Incident,
		Count:            st.fp.Count,
		FirstSeen:        st.fp.FirstSeenAt,
		LastSeen:         st.fp.LastSeenAt,
		ServiceInstances: st.serviceInstances,
	}
	if in.Incident.Fingerprint == "" {
		in.Incident = Incident{ExceptionKind: st.kind, Location: st.location, Fingerprint: st.fingerprint}
	}
	instances := make([]string, 0, len(st.recent))
	for name := range st.recent {
		instances = append(instances, name)
	}
	sort.Strings(instances)
	for _, name := range instances {
		c := st.recent[name]
		in.Instances = append(in.Instances, IssueInstance{Name: name, Image: c.image, Count: c.count})
	}
	if st.previous != "" {
		in.PreviousIssue = incidentIssueNumber(st.previous)
	}
	for _, source := range st.settings.Sources {
		if host, _, err := domain.ParseLadoLogEndpoint(source.Endpoint); err == nil && host != "" {
			in.ExtraHosts = append(in.ExtraHosts, host)
		}
	}
	return in
}

// incidentCreated follows a confirmed issue: it requests the plan when the
// settings allow it and tells the orchestrator.
func (h *IncidentHandler) incidentCreated(ctx context.Context, st *incidentState, task domain.LadoTask, previous domain.IssueID, now time.Time) {
	plan := h.incidentAutoPlan(ctx, st, task, now)
	kind := st.kind
	if kind == "" {
		kind = CleanKind(st.fp.ExceptionKind)
	}
	number := incidentIssueNumber(task.IssueRef)
	text := domain.LadoNoticeLogIncident(number, kind, st.fp.Count, plan)
	if previous != "" {
		text = domain.LadoNoticeLogRecurred(number, incidentIssueNumber(previous), kind, plan)
	}
	h.incidentNotice(ctx, domain.LadoOrchestratorNotice{
		ProjectID:  st.settings.ProjectID,
		Key:        fmt.Sprintf("log:%d:%s", st.fp.ID, task.IssueRef),
		Text:       text,
		OccurredAt: now,
	})
}

// incidentAutoPlan requests the plan of a new incident's task, or records
// why it holds the request. It returns the plan part of the notice.
func (h *IncidentHandler) incidentAutoPlan(ctx context.Context, st *incidentState, task domain.LadoTask, now time.Time) string {
	settings := st.settings
	if !settings.AutoPlan {
		h.incidentSetPlan(ctx, st, time.Time{}, domain.LadoLogAutoPlanHeldOff)
		return domain.LadoLogPlanOff
	}
	minCount := max(settings.AutoPlanMinCount, 1)
	if st.fp.Count < minCount {
		h.incidentSetPlan(ctx, st, time.Time{}, domain.LadoLogAutoPlanHeldMinCount)
		return domain.LadoLogPlanHeldMinCount(minCount)
	}
	return h.incidentRequestPlan(ctx, st, task, now)
}

// incidentLatePlan requests the plan held because the fingerprint was seen
// too few times, once it reaches the minimum.
func (h *IncidentHandler) incidentLatePlan(ctx context.Context, st *incidentState, task domain.LadoTask, now time.Time) {
	settings := st.settings
	if st.fp.AutoPlanHeld != domain.LadoLogAutoPlanHeldMinCount || !st.fp.AutoPlanRequestedAt.IsZero() ||
		!settings.AutoPlan || st.fp.Count < max(settings.AutoPlanMinCount, 1) {
		return
	}
	h.incidentRequestPlan(ctx, st, task, now)
}

// incidentRequestPlan requests the plan within the daily cap.
func (h *IncidentHandler) incidentRequestPlan(ctx context.Context, st *incidentState, task domain.LadoTask, now time.Time) string {
	settings := st.settings
	planCap := settings.AutoPlanDailyCap
	if planCap <= 0 {
		planCap = domain.LadoLogDefaultAutoPlanDailyCap
	}
	plans, err := h.deps.Store.CountLadoLogAutoPlansSince(ctx, settings.ProjectID, now.Add(-incidentPlanSpan))
	if err != nil {
		h.deps.Log.Warn("LADO could not count the log watch's plan requests", "project", settings.ProjectID, "err", err)
		h.incidentSetPlan(ctx, st, time.Time{}, domain.LadoLogAutoPlanHeldFailed)
		return domain.LadoLogPlanFailed
	}
	if plans >= planCap {
		h.incidentSetPlan(ctx, st, time.Time{}, domain.LadoLogAutoPlanHeldCap)
		return domain.LadoLogPlanHeldCap
	}
	if _, err := h.deps.Plans.RequestPlan(ctx, ladotask.PlanRequestInput{TaskID: task.ID}); err != nil {
		h.deps.Log.Warn("LADO could not request a log incident's plan", "project", settings.ProjectID, "task", task.ID, "err", err)
		h.incidentSetPlan(ctx, st, time.Time{}, domain.LadoLogAutoPlanHeldFailed)
		return domain.LadoLogPlanFailed
	}
	h.incidentSetPlan(ctx, st, now, "")
	return domain.LadoLogPlanRequested
}

func (h *IncidentHandler) incidentSetPlan(ctx context.Context, st *incidentState, requestedAt time.Time, held domain.LadoLogAutoPlanHold) {
	if err := h.deps.Store.SetLadoLogAutoPlan(ctx, st.fp.ID, requestedAt, held); err != nil {
		h.deps.Log.Warn("LADO could not record a log incident's plan request", "project", st.settings.ProjectID, "err", err)
		return
	}
	st.fp.AutoPlanRequestedAt, st.fp.AutoPlanHeld = requestedAt, held
}

// incidentBurst tells of the hourly cap, at most once an hour.
func (h *IncidentHandler) incidentBurst(ctx context.Context, p *incidentProject, project domain.ProjectID, issueCap int, now time.Time) {
	if !p.burstNotifiedAt.IsZero() && now.Sub(p.burstNotifiedAt) < incidentBurstEvery {
		return
	}
	bucket := now.Truncate(time.Hour).Unix()
	if !h.incidentNotice(ctx, domain.LadoOrchestratorNotice{
		ProjectID:  project,
		Key:        "log_burst:" + string(project) + ":" + strconv.FormatInt(bucket, 10),
		Text:       domain.LadoNoticeLogBurst(issueCap),
		OccurredAt: now,
	}) {
		return
	}
	p.burstNotifiedAt = now
	if err := h.deps.Store.SetLadoLogBurstNotified(ctx, project, now); err != nil {
		h.deps.Log.Warn("LADO could not record the log watch's burst notice", "project", project, "err", err)
	}
}

// incidentNotice queues a notice. It reports whether the outbox took it.
func (h *IncidentHandler) incidentNotice(ctx context.Context, n domain.LadoOrchestratorNotice) bool {
	if h.deps.Notices == nil {
		return false
	}
	if _, err := h.deps.Notices.AddLadoOrchestratorNotice(ctx, n); err != nil {
		h.deps.Log.Warn("LADO could not queue a log watch notice", "project", n.ProjectID, "key", n.Key, "err", err)
		return false
	}
	return true
}

// incidentIssueNumber is the number of an issue ref, 0 when it has none.
func incidentIssueNumber(ref domain.IssueID) int {
	id, err := domain.ParseLadoIssueRef(ref)
	if err != nil {
		return 0
	}
	return domain.LadoIssueNumber(id)
}
