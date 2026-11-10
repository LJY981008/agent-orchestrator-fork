package ladologwatch

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The runner (ADR 0012) keeps one Follower per source of every project whose
// log watch and LADO mode are on, and hands each line through the instance's
// Detector to the IncidentSink:
//
//   - A project with the log watch off, a project outside LADO mode (or
//     archived), a paused source, a source whose targets are all paused, and
//     a TLS source without its CA copy get no Follower: LADO does not connect
//     to them at all.
//   - Settings changes reach the Followers on Kick (the API methods kick) and
//     at the latest every serviceReconcileEvery. A source whose connection
//     values changed is restarted; one whose rules changed only gets new
//     Detectors.
//   - Every serviceFlushEvery the Detectors and the sink are flushed and the
//     disconnected sources are looked at: a source down longer than the
//     project's disconnect alert minutes is told of once
//     (log_source_down:<source>:<down since>), and no issue is made. When it
//     connects again the Follower reads on from the cursors.

const (
	// serviceReconcileEvery is how often the runner reads the settings even
	// without a Kick.
	serviceReconcileEvery = 30 * time.Second
	// serviceFlushEvery is how often the Detectors and the sink are flushed.
	serviceFlushEvery = time.Second
	// serviceQueue is how many occurrences wait for the sink before a line
	// hook waits.
	serviceQueue = 256
	// serviceIncidentLimit is how many fingerprints Incidents gives besides
	// those of open tasks.
	serviceIncidentLimit = 200
	// serviceCheckTimeout bounds a connection check.
	serviceCheckTimeout = 30 * time.Second
	// serviceMaxDetail caps the reason a check failed or a source cannot
	// start, as the settings screen shows it.
	serviceMaxDetail = 500
	// serviceNoCA is the status detail of a TLS source without its CA copy.
	serviceNoCA = "TLS 인증서(CA)를 가져오세요"
)

// IncidentSink takes the occurrences the runner finds: the IncidentHandler.
type IncidentSink interface {
	Handle(ctx context.Context, project domain.ProjectID, settings domain.LadoLogWatchSettings, o Occurrence)
	Flush(ctx context.Context, now time.Time)
}

// ServiceStore is what the runner and the API methods keep in LADO's store.
type ServiceStore interface {
	LadoLogWatchSettings(ctx context.Context, project domain.ProjectID) (domain.LadoLogWatchSettings, bool, error)
	ListEnabledLadoLogWatchSettings(ctx context.Context) ([]domain.LadoLogWatchSettings, error)
	PutLadoLogWatchSettings(ctx context.Context, settings domain.LadoLogWatchSettings) ([]domain.LadoLogSourceID, error)
	SetLadoLogSourceDown(ctx context.Context, source domain.LadoLogSourceID, downSince, notifiedAt time.Time) error
	SetLadoLogSourceCertKey(ctx context.Context, source domain.LadoLogSourceID, certKey string) error
	LadoLogCursor(ctx context.Context, target domain.LadoLogTargetID, instance string) (domain.LadoLogCursor, bool, error)
	PutLadoLogCursor(ctx context.Context, c domain.LadoLogCursor) error
	LadoLogFingerprintByID(ctx context.Context, id domain.LadoLogFingerprintID) (domain.LadoLogFingerprint, bool, error)
	SetLadoLogFingerprintIgnored(ctx context.Context, id domain.LadoLogFingerprintID, ignored bool, at time.Time) (domain.LadoLogFingerprint, error)
	ListLadoLogIncidents(ctx context.Context, project domain.ProjectID, otherLimit int) ([]domain.LadoLogFingerprint, error)
}

// ServiceProjects is what the runner reads of AO's projects: which exist,
// their folders, and their LADO mode.
type ServiceProjects interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
	LadoProjectSettings(ctx context.Context, project domain.ProjectID) (domain.LadoProjectSettings, error)
}

// Deps wires a Service.
type Deps struct {
	Store ServiceStore
	Sink  IncidentSink
	// Certs keeps the TLS copies of the sources.
	Certs *CertStore
	// NewSource makes the LogSource of a source; nil uses NewDockerSource.
	NewSource func(DockerConfig) (LogSource, error)
	// Check checks a source's connection; nil uses CheckDocker.
	Check    func(ctx context.Context, cfg DockerConfig) (DockerCheck, error)
	Projects ServiceProjects
	Notices  IncidentNotices
	// Clock stamps what the runner records; nil uses time.Now.
	Clock func() time.Time
	// NewID gives new source and target ids; nil uses uuid.NewString.
	NewID func() string
	Log   *slog.Logger
}

// Service runs the log watch and serves its API
// (controllers.LadoLogWatchService).
type Service struct {
	deps Deps

	// The timings; tests make them short.
	reconcileEvery time.Duration
	flushEvery     time.Duration
	// tuneFollower adjusts a new Follower; tests shorten its timings.
	tuneFollower func(*Follower)

	kick  chan struct{}
	occ   chan serviceOccurrence
	start sync.Once
	done  chan struct{}

	// mu guards sources and restart. It is never held while a Follower is
	// waited for: a Follower's hooks take it.
	mu      sync.Mutex
	sources map[domain.LadoLogSourceID]*serviceSource
	// restart are the sources whose TLS copies changed: their Followers
	// start over on the next reconcile.
	restart map[domain.LadoLogSourceID]bool
	// running is whether Start was called; ctx is the runner's context.
	running bool
	ctx     context.Context
}

// serviceOccurrence is one occurrence on its way to the sink.
type serviceOccurrence struct {
	project  domain.ProjectID
	settings domain.LadoLogWatchSettings
	o        Occurrence
}

// serviceSource is one followed source.
type serviceSource struct {
	id      domain.LadoLogSourceID
	project domain.ProjectID
	// sig is the connection values the Follower was made with.
	sig      string
	follower *Follower
	cancel   context.CancelFunc
	done     chan struct{}

	mu       sync.Mutex
	settings domain.LadoLogWatchSettings
	source   domain.LadoLogSource
	rules    string
	status   FollowerStatus
	// detectors are the Detectors by container id.
	detectors map[string]*Detector
	// lastLine is the time of the newest line by target.
	lastLine map[string]time.Time
	// storedDown and notifiedFor are the disconnection the store knows of and
	// the one LADO told of.
	storedDown  time.Time
	notifiedFor time.Time
}

// New returns a Service; Start runs it.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	if deps.NewSource == nil {
		deps.NewSource = func(cfg DockerConfig) (LogSource, error) {
			return NewDockerSource(cfg)
		}
	}
	if deps.Check == nil {
		deps.Check = CheckDocker
	}
	return &Service{
		deps:           deps,
		reconcileEvery: serviceReconcileEvery,
		flushEvery:     serviceFlushEvery,
		kick:           make(chan struct{}, 1),
		occ:            make(chan serviceOccurrence, serviceQueue),
		done:           make(chan struct{}),
		sources:        map[domain.LadoLogSourceID]*serviceSource{},
		restart:        map[domain.LadoLogSourceID]bool{},
	}
}

// Start runs the log watch until ctx ends. The channel closes once every
// log stream is closed. A second Start returns the same channel.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	s.start.Do(func() {
		s.mu.Lock()
		s.running = true
		s.ctx = ctx
		s.mu.Unlock()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.runReconcile(ctx)
		}()
		go func() {
			defer wg.Done()
			s.runSink(ctx)
		}()
		go func() {
			wg.Wait()
			close(s.done)
		}()
	})
	return s.done
}

// Kick makes the runner read the settings again soon.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Service) runReconcile(ctx context.Context) {
	defer s.stopAll()
	tick := time.NewTicker(s.reconcileEvery)
	defer tick.Stop()
	for {
		s.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-tick.C:
		}
	}
}

func (s *Service) runSink(ctx context.Context) {
	tick := time.NewTicker(s.flushEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case o := <-s.occ:
			s.deps.Sink.Handle(ctx, o.project, o.settings, o.o)
		case <-tick.C:
			now := s.deps.Clock()
			for _, o := range s.flushDetectors(now) {
				s.deps.Sink.Handle(ctx, o.project, o.settings, o.o)
			}
			s.deps.Sink.Flush(ctx, now)
			s.checkDown(ctx, now)
		}
	}
}

// serviceWanted is a source that should be followed now.
type serviceWanted struct {
	settings domain.LadoLogWatchSettings
	source   domain.LadoLogSource
	cfg      DockerConfig
	sig      string
}

// reconcile makes the running Followers match the settings.
func (s *Service) reconcile(ctx context.Context) {
	all, err := s.deps.Store.ListEnabledLadoLogWatchSettings(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.deps.Log.Warn("lado log watch: read settings", "err", err)
		}
		return
	}
	live, ok := s.liveProjects(ctx)
	if !ok {
		return
	}
	wanted := map[domain.LadoLogSourceID]serviceWanted{}
	// keep are projects whose state LADO could not read: their running
	// sources stay as they are.
	keep := map[domain.ProjectID]bool{}
	for _, settings := range all {
		if live != nil && !live[settings.ProjectID] {
			continue
		}
		on, known := s.ladoMode(ctx, settings.ProjectID)
		if !known {
			keep[settings.ProjectID] = true
			continue
		}
		if !on {
			continue
		}
		for _, src := range settings.Sources {
			cfg, follow, _ := s.sourceConfig(settings.ProjectID, src, false)
			if !follow {
				continue
			}
			wanted[src.ID] = serviceWanted{settings: settings, source: src, cfg: cfg, sig: serviceSignature(settings.ProjectID, cfg)}
		}
	}

	var stop []*serviceSource
	s.mu.Lock()
	restart := s.restart
	s.restart = map[domain.LadoLogSourceID]bool{}
	for id, run := range s.sources {
		w, ok := wanted[id]
		switch {
		case !ok && keep[run.project]:
			continue
		case !ok || w.sig != run.sig || restart[id]:
			stop = append(stop, run)
			delete(s.sources, id)
		}
	}
	s.mu.Unlock()
	s.stopSources(stop)

	if ctx.Err() != nil {
		return
	}
	ids := make([]domain.LadoLogSourceID, 0, len(wanted))
	for id := range wanted {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		w := wanted[id]
		s.mu.Lock()
		run, have := s.sources[id]
		s.mu.Unlock()
		if have {
			run.update(w.settings, w.source)
			continue
		}
		s.startSource(ctx, w)
	}
}

// liveProjects are the projects that exist and are not archived; nil when
// the runner has no project list. ok is false when the list failed.
func (s *Service) liveProjects(ctx context.Context) (map[domain.ProjectID]bool, bool) {
	if s.deps.Projects == nil {
		return nil, true
	}
	records, err := s.deps.Projects.ListProjects(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.deps.Log.Warn("lado log watch: list projects", "err", err)
		}
		return nil, false
	}
	live := make(map[domain.ProjectID]bool, len(records))
	for _, p := range records {
		if p.ArchivedAt.IsZero() {
			live[domain.ProjectID(p.ID)] = true
		}
	}
	return live, true
}

// ladoMode is a project's LADO mode; known is false when it could not be
// read.
func (s *Service) ladoMode(ctx context.Context, project domain.ProjectID) (on, known bool) {
	if s.deps.Projects == nil {
		return true, true
	}
	settings, err := s.deps.Projects.LadoProjectSettings(ctx, project)
	if err != nil {
		if ctx.Err() == nil {
			s.deps.Log.Warn("lado log watch: read LADO mode", "project", project, "err", err)
		}
		return false, false
	}
	return settings.LadoMode, true
}

// sourceConfig is the DockerConfig of a source. follow is whether the source
// may be followed: not paused, with a target that is not paused, and with
// its CA copy when it uses TLS. allTargets keeps the paused targets too (a
// connection check shows them). detail says why a source that is not paused
// cannot be followed.
func (s *Service) sourceConfig(project domain.ProjectID, src domain.LadoLogSource, allTargets bool) (DockerConfig, bool, string) {
	cfg := DockerConfig{Endpoint: src.Endpoint, TLS: src.TLS}
	for _, t := range src.Targets {
		if t.Paused && !allTargets {
			continue
		}
		cfg.Targets = append(cfg.Targets, DockerTarget{
			ID: string(t.ID), Kind: string(t.Kind), Value: t.Value,
			ComposeProject: t.ComposeProject, Service: t.Service,
		})
	}
	if src.TLS {
		ca, cert, key, ok := s.certPaths(project, src)
		if !ok {
			return cfg, false, serviceNoCA
		}
		cfg.CAFile, cfg.CertFile, cfg.KeyFile = ca, cert, key
	}
	if src.Paused || len(cfg.Targets) == 0 {
		return cfg, false, ""
	}
	return cfg, true, ""
}

// serviceCertKey is the name of a source's TLS copies.
func serviceCertKey(project domain.ProjectID, src domain.LadoLogSource) string {
	if src.CertKey != "" {
		return src.CertKey
	}
	return CertName(string(project), string(src.ID))
}

func (s *Service) certPaths(project domain.ProjectID, src domain.LadoLogSource) (ca, cert, key string, ok bool) {
	if s.deps.Certs == nil {
		return "", "", "", false
	}
	return s.deps.Certs.Paths(serviceCertKey(project, src))
}

// serviceSignature is the connection values of a source: a Follower made
// with other values starts over.
func serviceSignature(project domain.ProjectID, cfg DockerConfig) string {
	var b strings.Builder
	field := func(v string) {
		b.WriteString(strconv.Itoa(len(v)))
		b.WriteByte(':')
		b.WriteString(v)
	}
	field(string(project))
	field(cfg.Endpoint)
	field(strconv.FormatBool(cfg.TLS))
	field(cfg.CAFile)
	field(cfg.CertFile)
	field(cfg.KeyFile)
	for _, t := range cfg.Targets {
		field(t.ID)
		field(t.Kind)
		field(t.Value)
		field(t.ComposeProject)
		field(t.Service)
	}
	return b.String()
}

// serviceRules is the key of a project's rules.
func serviceRules(settings domain.LadoLogWatchSettings) string {
	return strings.Join(settings.Include, "\x00") + "\x01" + strings.Join(settings.Exclude, "\x00")
}

// startSource starts the Follower of a source.
func (s *Service) startSource(ctx context.Context, w serviceWanted) {
	src, err := s.deps.NewSource(w.cfg)
	run := &serviceSource{
		id:         w.source.ID,
		project:    w.settings.ProjectID,
		sig:        w.sig,
		settings:   w.settings,
		source:     w.source,
		rules:      serviceRules(w.settings),
		detectors:  map[string]*Detector{},
		lastLine:   map[string]time.Time{},
		storedDown: w.source.DownSince,
		status:     FollowerStatus{State: domain.LadoLogSourceStateConnecting},
	}
	if !w.source.DownNotifiedAt.IsZero() {
		run.notifiedFor = w.source.DownSince
	}
	if err != nil {
		// A source the client refuses (a CA copy that no longer reads, say)
		// stays in error until its settings change.
		run.status = FollowerStatus{State: domain.LadoLogSourceStateError, Detail: dockerClip(err.Error(), serviceMaxDetail)}
		s.mu.Lock()
		s.sources[run.id] = run
		s.mu.Unlock()
		return
	}
	run.follower = NewFollower(src, FollowerHooks{
		Cursor: func(targetID, instance string) (time.Time, bool) {
			c, ok, err := s.deps.Store.LadoLogCursor(ctx, domain.LadoLogTargetID(targetID), instance)
			if err != nil || !ok {
				return time.Time{}, false
			}
			return c.Since, true
		},
		SaveCursor: func(targetID, instance string, at time.Time) {
			// The runner's own context may already be done while the last
			// cursors are saved.
			err := s.deps.Store.PutLadoLogCursor(context.WithoutCancel(ctx), domain.LadoLogCursor{
				TargetID: domain.LadoLogTargetID(targetID), Instance: instance, Since: at, UpdatedAt: s.deps.Clock(),
			})
			if err != nil {
				s.deps.Log.Warn("lado log watch: save cursor", "source", run.id, "err", err)
			}
		},
		Line: func(inst Instance, stream string, ts time.Time, text string) {
			s.line(ctx, run, inst, stream, ts, text)
		},
		Status: func(st FollowerStatus) {
			s.status(ctx, run, st)
		},
	})
	if s.tuneFollower != nil {
		s.tuneFollower(run.follower)
	}
	fctx, cancel := context.WithCancel(ctx)
	run.cancel = cancel
	run.done = make(chan struct{})
	s.mu.Lock()
	s.sources[run.id] = run
	s.mu.Unlock()
	go func() {
		defer close(run.done)
		run.follower.Run(fctx)
	}()
}

// stopSources stops Followers and waits for them, then gives out what their
// Detectors still hold.
func (s *Service) stopSources(runs []*serviceSource) {
	for _, run := range runs {
		if run.cancel != nil {
			run.cancel()
		}
	}
	for _, run := range runs {
		if run.done != nil {
			<-run.done
		}
	}
	now := s.deps.Clock()
	for _, run := range runs {
		for _, o := range run.drain(now) {
			s.send(s.runCtx(), o)
		}
	}
}

func (s *Service) stopAll() {
	s.mu.Lock()
	runs := make([]*serviceSource, 0, len(s.sources))
	for id, run := range s.sources {
		runs = append(runs, run)
		delete(s.sources, id)
	}
	s.mu.Unlock()
	for _, run := range runs {
		if run.cancel != nil {
			run.cancel()
		}
	}
	for _, run := range runs {
		if run.done != nil {
			<-run.done
		}
	}
}

func (s *Service) runCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// update gives a running source its new settings. New rules make new
// Detectors.
func (run *serviceSource) update(settings domain.LadoLogWatchSettings, src domain.LadoLogSource) {
	run.mu.Lock()
	defer run.mu.Unlock()
	run.settings = settings
	run.source = src
	if rules := serviceRules(settings); rules != run.rules {
		run.rules = rules
		run.detectors = map[string]*Detector{}
	}
}

// drain gives out everything the source's Detectors hold.
func (run *serviceSource) drain(now time.Time) []serviceOccurrence {
	run.mu.Lock()
	defer run.mu.Unlock()
	var out []serviceOccurrence
	for id, det := range run.detectors {
		inst := run.instance(id)
		incs := det.Flush(now)
		incs = append(incs, det.Flush(now.Add(DetectIdle))...)
		for _, inc := range incs {
			out = append(out, run.occurrence(inst, inc, 0))
		}
	}
	run.detectors = map[string]*Detector{}
	return out
}

// instance is the attached instance with a container id; the caller holds
// run.mu.
func (run *serviceSource) instance(id string) Instance {
	for _, inst := range run.status.Instances {
		if inst.ID == id {
			return inst
		}
	}
	return Instance{ID: id}
}

// occurrence makes the occurrence of an incident; the caller holds run.mu.
func (run *serviceSource) occurrence(inst Instance, inc Incident, serviceInstances int) serviceOccurrence {
	return serviceOccurrence{
		project:  run.project,
		settings: run.settings,
		o: Occurrence{
			Service:          inst.Service,
			Instance:         run.source.DisplayName + "/" + inst.Name,
			Image:            inst.Image,
			ServiceInstances: serviceInstances,
			Incident:         inc,
		},
	}
}

// line hands one line of an instance to its Detector and its incidents to
// the sink.
func (s *Service) line(ctx context.Context, run *serviceSource, inst Instance, stream string, ts time.Time, text string) {
	run.mu.Lock()
	if !ts.IsZero() && ts.After(run.lastLine[inst.TargetID]) {
		run.lastLine[inst.TargetID] = ts
	}
	det := run.detectors[inst.ID]
	if det == nil {
		det = NewDetector(inst.Service, Rules{Include: run.settings.Include, Exclude: run.settings.Exclude})
		run.detectors[inst.ID] = det
	}
	run.mu.Unlock()
	incs := det.Line(stream, ts, text)
	if len(incs) == 0 {
		return
	}
	n := s.serviceInstances(run.project, inst)
	run.mu.Lock()
	out := make([]serviceOccurrence, 0, len(incs))
	for _, inc := range incs {
		out = append(out, run.occurrence(inst, inc, n))
	}
	run.mu.Unlock()
	for _, o := range out {
		s.send(ctx, o)
	}
}

// send queues an occurrence for the sink; it waits while the queue is full.
func (s *Service) send(ctx context.Context, o serviceOccurrence) {
	select {
	case s.occ <- o:
	case <-ctx.Done():
	}
}

// serviceInstances is how many instances of inst's service the project's
// sources follow now, counted once per container id; at least one.
func (s *Service) serviceInstances(project domain.ProjectID, inst Instance) int {
	s.mu.Lock()
	runs := make([]*serviceSource, 0, len(s.sources))
	for _, run := range s.sources {
		if run.project == project {
			runs = append(runs, run)
		}
	}
	s.mu.Unlock()
	ids := map[string]bool{inst.ID: true}
	for _, run := range runs {
		run.mu.Lock()
		for _, other := range run.status.Instances {
			if other.Service == inst.Service {
				ids[other.ID] = true
			}
		}
		run.mu.Unlock()
	}
	return len(ids)
}

// status records a Follower's status and lets go of the Detectors of
// instances that are gone.
func (s *Service) status(ctx context.Context, run *serviceSource, st FollowerStatus) {
	attached := make(map[string]bool, len(st.Instances))
	for _, inst := range st.Instances {
		attached[inst.ID] = true
	}
	now := s.deps.Clock()
	var out []serviceOccurrence
	run.mu.Lock()
	gone := map[string]Instance{}
	for id := range run.detectors {
		if !attached[id] && st.State == domain.LadoLogSourceStateWatching {
			gone[id] = run.instance(id)
		}
	}
	for id, inst := range gone {
		det := run.detectors[id]
		incs := det.Flush(now)
		incs = append(incs, det.Flush(now.Add(DetectIdle))...)
		for _, inc := range incs {
			out = append(out, run.occurrence(inst, inc, 0))
		}
		delete(run.detectors, id)
	}
	run.status = st
	run.mu.Unlock()
	for _, o := range out {
		s.send(ctx, o)
	}
}

// flushDetectors flushes every Detector on the runner's clock.
func (s *Service) flushDetectors(now time.Time) []serviceOccurrence {
	runs := s.runningSources()
	var out []serviceOccurrence
	for _, run := range runs {
		type found struct {
			inst Instance
			incs []Incident
		}
		var all []found
		run.mu.Lock()
		for id, det := range run.detectors {
			if incs := det.Flush(now); len(incs) > 0 {
				all = append(all, found{inst: run.instance(id), incs: incs})
			}
		}
		run.mu.Unlock()
		for _, f := range all {
			n := s.serviceInstances(run.project, f.inst)
			run.mu.Lock()
			for _, inc := range f.incs {
				out = append(out, run.occurrence(f.inst, inc, n))
			}
			run.mu.Unlock()
		}
	}
	return out
}

func (s *Service) runningSources() []*serviceSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make([]*serviceSource, 0, len(s.sources))
	for _, run := range s.sources {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].id < runs[j].id })
	return runs
}

// checkDown records the sources that lost their connection and tells of the
// ones down longer than the project's alert minutes, once per disconnection.
func (s *Service) checkDown(ctx context.Context, now time.Time) {
	for _, run := range s.runningSources() {
		run.mu.Lock()
		down := run.status.DownSince
		watching := run.status.State == domain.LadoLogSourceStateWatching
		stored := run.storedDown
		notified := run.notifiedFor
		minutes := run.settings.DisconnectAlertMinutes
		name := run.source.DisplayName
		run.mu.Unlock()
		if minutes <= 0 {
			minutes = domain.LadoLogDefaultDisconnectAlertMinutes
		}
		if down.IsZero() {
			// Only a source that answers again is connected: one still
			// connecting after a restart keeps its stored disconnection.
			if !stored.IsZero() && watching {
				// Connected again: the Follower reads on from the cursors.
				if err := s.deps.Store.SetLadoLogSourceDown(ctx, run.id, time.Time{}, time.Time{}); err == nil {
					run.mu.Lock()
					run.storedDown, run.notifiedFor = time.Time{}, time.Time{}
					run.mu.Unlock()
				}
			}
			continue
		}
		// A disconnection the store knew of before this Follower started
		// counts from its start.
		if !stored.IsZero() && stored.Before(down) {
			down = stored
		}
		if stored.IsZero() {
			if err := s.deps.Store.SetLadoLogSourceDown(ctx, run.id, down, time.Time{}); err != nil {
				continue
			}
			run.mu.Lock()
			run.storedDown = down
			run.mu.Unlock()
		}
		if notified.Equal(down) || now.Sub(down) < time.Duration(minutes)*time.Minute {
			continue
		}
		down = down.UTC()
		if s.deps.Notices != nil {
			_, err := s.deps.Notices.AddLadoOrchestratorNotice(ctx, domain.LadoOrchestratorNotice{
				ProjectID:  run.project,
				Key:        fmt.Sprintf("log_source_down:%s:%d", run.id, down.Unix()),
				Text:       domain.LadoNoticeLogSourceDown(name, int(now.Sub(down)/time.Minute)),
				OccurredAt: now,
			})
			if err != nil {
				s.deps.Log.Warn("lado log watch: queue source down notice", "source", run.id, "err", err)
				continue
			}
		}
		if err := s.deps.Store.SetLadoLogSourceDown(ctx, run.id, down, now); err != nil {
			s.deps.Log.Warn("lado log watch: record source down notice", "source", run.id, "err", err)
		}
		run.mu.Lock()
		run.storedDown, run.notifiedFor = down, down
		run.mu.Unlock()
	}
}

// sourceStatus is what the view shows of a source.
func (s *Service) sourceStatus(project domain.ProjectID, src domain.LadoLogSource, watching bool) domain.LadoLogSourceStatus {
	st := domain.LadoLogSourceStatus{SourceID: src.ID, State: domain.LadoLogSourceStateOff, Insecure: !src.TLS}
	s.mu.Lock()
	run := s.sources[src.ID]
	s.mu.Unlock()
	if run != nil && run.project == project {
		run.mu.Lock()
		st.State = run.status.State
		st.Detail = run.status.Detail
		st.DownSince = run.status.DownSince
		counts := map[string]int{}
		for _, inst := range run.status.Instances {
			counts[inst.TargetID]++
		}
		for _, t := range src.Targets {
			st.Targets = append(st.Targets, domain.LadoLogTargetStatus{
				TargetID: t.ID, Instances: counts[string(t.ID)], LastLineAt: run.lastLine[string(t.ID)],
			})
		}
		run.mu.Unlock()
		if st.State == "" {
			st.State = domain.LadoLogSourceStateOff
		}
		return st
	}
	if watching && !src.Paused && src.TLS {
		if _, _, _, ok := s.certPaths(project, src); !ok {
			st.State = domain.LadoLogSourceStateError
			st.Detail = serviceNoCA
		}
	}
	for _, t := range src.Targets {
		st.Targets = append(st.Targets, domain.LadoLogTargetStatus{TargetID: t.ID})
	}
	return st
}
