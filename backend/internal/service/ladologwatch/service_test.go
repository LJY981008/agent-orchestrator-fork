package ladologwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/ladotask"
)

// The runner serves the log watch API with the signature the controller
// defines.
var _ controllers.LadoLogWatchService = (*Service)(nil)

// serviceFakeStore is an in-memory ServiceStore.
type serviceFakeStore struct {
	mu           sync.Mutex
	settings     map[domain.ProjectID]domain.LadoLogWatchSettings
	cursors      map[string]domain.LadoLogCursor
	fingerprints map[domain.LadoLogFingerprintID]domain.LadoLogFingerprint
	puts         int
	down         []serviceDownCall
}

type serviceDownCall struct {
	source              domain.LadoLogSourceID
	downSince, notified time.Time
}

func newServiceFakeStore() *serviceFakeStore {
	return &serviceFakeStore{
		settings:     map[domain.ProjectID]domain.LadoLogWatchSettings{},
		cursors:      map[string]domain.LadoLogCursor{},
		fingerprints: map[domain.LadoLogFingerprintID]domain.LadoLogFingerprint{},
	}
}

func serviceCopySettings(s domain.LadoLogWatchSettings) domain.LadoLogWatchSettings {
	out := s
	out.Include = append([]string{}, s.Include...)
	out.Exclude = append([]string{}, s.Exclude...)
	out.Sources = make([]domain.LadoLogSource, 0, len(s.Sources))
	for _, src := range s.Sources {
		c := src
		c.Targets = append([]domain.LadoLogTarget{}, src.Targets...)
		out.Sources = append(out.Sources, c)
	}
	return out
}

func (s *serviceFakeStore) LadoLogWatchSettings(_ context.Context, project domain.ProjectID) (domain.LadoLogWatchSettings, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, ok := s.settings[project]
	return serviceCopySettings(settings), ok, nil
}

func (s *serviceFakeStore) ListEnabledLadoLogWatchSettings(context.Context) ([]domain.LadoLogWatchSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LadoLogWatchSettings
	for _, settings := range s.settings {
		if settings.Enabled {
			out = append(out, serviceCopySettings(settings))
		}
	}
	return out, nil
}

func (s *serviceFakeStore) PutLadoLogWatchSettings(_ context.Context, settings domain.LadoLogWatchSettings) ([]domain.LadoLogSourceID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[domain.LadoLogSourceID]bool{}
	for _, src := range settings.Sources {
		if src.ID == "" {
			return nil, errors.New("a source has no id")
		}
		for _, t := range src.Targets {
			if t.ID == "" {
				return nil, errors.New("a target has no id")
			}
		}
		keep[src.ID] = true
	}
	var removed []domain.LadoLogSourceID
	old := s.settings[settings.ProjectID]
	stored := map[domain.LadoLogSourceID]domain.LadoLogSource{}
	for _, src := range old.Sources {
		stored[src.ID] = src
		if !keep[src.ID] {
			removed = append(removed, src.ID)
		}
	}
	next := serviceCopySettings(settings)
	next.BurstNotifiedAt = old.BurstNotifiedAt
	for i, src := range next.Sources {
		if o, ok := stored[src.ID]; ok {
			next.Sources[i].DownSince, next.Sources[i].DownNotifiedAt = o.DownSince, o.DownNotifiedAt
			if o.CertKey != "" {
				next.Sources[i].CertKey = o.CertKey
			}
		}
	}
	s.settings[settings.ProjectID] = next
	s.puts++
	return removed, nil
}

func (s *serviceFakeStore) put(settings domain.LadoLogWatchSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[settings.ProjectID] = serviceCopySettings(settings)
}

func (s *serviceFakeStore) SetLadoLogSourceDown(_ context.Context, source domain.LadoLogSourceID, downSince, notified time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = append(s.down, serviceDownCall{source: source, downSince: downSince, notified: notified})
	return nil
}

func (s *serviceFakeStore) downCalls() []serviceDownCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]serviceDownCall(nil), s.down...)
}

func (s *serviceFakeStore) SetLadoLogSourceCertKey(_ context.Context, source domain.LadoLogSourceID, certKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for project, settings := range s.settings {
		for i, src := range settings.Sources {
			if src.ID == source {
				settings.Sources[i].CertKey = certKey
				s.settings[project] = settings
				return nil
			}
		}
	}
	return domain.ErrLadoLogSourceNotFound
}

func (s *serviceFakeStore) LadoLogCursor(_ context.Context, target domain.LadoLogTargetID, instance string) (domain.LadoLogCursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cursors[string(target)+"/"+instance]
	return c, ok, nil
}

func (s *serviceFakeStore) PutLadoLogCursor(_ context.Context, c domain.LadoLogCursor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors[string(c.TargetID)+"/"+c.Instance] = c
	return nil
}

func (s *serviceFakeStore) LadoLogFingerprintByID(_ context.Context, id domain.LadoLogFingerprintID) (domain.LadoLogFingerprint, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp, ok := s.fingerprints[id]
	return fp, ok, nil
}

func (s *serviceFakeStore) SetLadoLogFingerprintIgnored(_ context.Context, id domain.LadoLogFingerprintID, ignored bool, at time.Time) (domain.LadoLogFingerprint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp, ok := s.fingerprints[id]
	if !ok {
		return domain.LadoLogFingerprint{}, domain.ErrLadoLogFingerprintNotFound
	}
	fp.Ignored, fp.IgnoredAt = ignored, at
	s.fingerprints[id] = fp
	return fp, nil
}

func (s *serviceFakeStore) ListLadoLogIncidents(_ context.Context, project domain.ProjectID, _ int) ([]domain.LadoLogFingerprint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LadoLogFingerprint
	for _, fp := range s.fingerprints {
		if fp.ProjectID == project {
			out = append(out, fp)
		}
	}
	return out, nil
}

// serviceFakeProjects is a fake ServiceProjects.
type serviceFakeProjects struct {
	mu       sync.Mutex
	records  []domain.ProjectRecord
	repos    map[string][]domain.WorkspaceRepoRecord
	ladoMode map[domain.ProjectID]bool
}

func (p *serviceFakeProjects) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.ProjectRecord(nil), p.records...), nil
}

func (p *serviceFakeProjects) ListWorkspaceRepos(_ context.Context, project string) ([]domain.WorkspaceRepoRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.repos[project], nil
}

func (p *serviceFakeProjects) LadoProjectSettings(_ context.Context, project domain.ProjectID) (domain.LadoProjectSettings, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return domain.LadoProjectSettings{ProjectID: project, LadoMode: p.ladoMode[project]}, nil
}

// serviceFakeSink records what the runner hands on.
type serviceFakeSink struct {
	mu      sync.Mutex
	got     []serviceOccurrence
	flushes int
}

func (k *serviceFakeSink) Handle(_ context.Context, project domain.ProjectID, settings domain.LadoLogWatchSettings, o Occurrence) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.got = append(k.got, serviceOccurrence{project: project, settings: settings, o: o})
}

func (k *serviceFakeSink) Flush(context.Context, time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.flushes++
}

func (k *serviceFakeSink) occurrences() []serviceOccurrence {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]serviceOccurrence(nil), k.got...)
}

// serviceFakeNotices records the queued notices.
type serviceFakeNotices struct {
	mu   sync.Mutex
	keys map[string]bool
	got  []domain.LadoOrchestratorNotice
}

func (n *serviceFakeNotices) AddLadoOrchestratorNotice(_ context.Context, notice domain.LadoOrchestratorNotice) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.keys == nil {
		n.keys = map[string]bool{}
	}
	if n.keys[notice.Key] {
		return false, nil
	}
	n.keys[notice.Key] = true
	n.got = append(n.got, notice)
	return true, nil
}

func (n *serviceFakeNotices) list() []domain.LadoOrchestratorNotice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]domain.LadoOrchestratorNotice(nil), n.got...)
}

// serviceFixture is a Service on fakes. Each endpoint has its own fake
// source.
type serviceFixture struct {
	svc      *Service
	store    *serviceFakeStore
	projects *serviceFakeProjects
	sink     *serviceFakeSink
	notices  *serviceFakeNotices
	certs    *CertStore
	offset   atomic.Int64

	mu      sync.Mutex
	sources map[string]*followTestSource
	made    []DockerConfig
	checks  []DockerConfig
	check   func(DockerConfig) (DockerCheck, error)
	ids     int
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	fx := &serviceFixture{
		store:    newServiceFakeStore(),
		projects: &serviceFakeProjects{repos: map[string][]domain.WorkspaceRepoRecord{}, ladoMode: map[domain.ProjectID]bool{}},
		sink:     &serviceFakeSink{},
		notices:  &serviceFakeNotices{},
		certs:    NewCertStore(t.TempDir()),
		sources:  map[string]*followTestSource{},
	}
	fx.svc = New(Deps{
		Store:    fx.store,
		Sink:     fx.sink,
		Certs:    fx.certs,
		Projects: fx.projects,
		Notices:  fx.notices,
		Clock:    fx.now,
		NewID: func() string {
			fx.mu.Lock()
			defer fx.mu.Unlock()
			fx.ids++
			return fmt.Sprintf("id-%d", fx.ids)
		},
		NewSource: func(cfg DockerConfig) (LogSource, error) {
			fx.mu.Lock()
			defer fx.mu.Unlock()
			fx.made = append(fx.made, cfg)
			return fx.sourceLocked(cfg.Endpoint), nil
		},
		Check: func(_ context.Context, cfg DockerConfig) (DockerCheck, error) {
			fx.mu.Lock()
			fx.checks = append(fx.checks, cfg)
			check := fx.check
			fx.mu.Unlock()
			if check == nil {
				return DockerCheck{}, errors.New("no check")
			}
			return check(cfg)
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	fx.svc.reconcileEvery = 40 * time.Millisecond
	fx.svc.flushEvery = 10 * time.Millisecond
	fx.svc.tuneFollower = func(f *Follower) {
		f.discoverEvery = 40 * time.Millisecond
		f.discoverGap = 5 * time.Millisecond
		f.cursorEvery = 20 * time.Millisecond
		f.backoffMin = 10 * time.Millisecond
		f.backoffMax = 20 * time.Millisecond
		f.now = fx.now
	}
	return fx
}

func (fx *serviceFixture) now() time.Time {
	return time.Now().Add(time.Duration(fx.offset.Load()))
}

func (fx *serviceFixture) advance(d time.Duration) { fx.offset.Add(int64(d)) }

// source is the fake source of an endpoint.
func (fx *serviceFixture) source(endpoint string) *followTestSource {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return fx.sourceLocked(endpoint)
}

func (fx *serviceFixture) sourceLocked(endpoint string) *followTestSource {
	src := fx.sources[endpoint]
	if src == nil {
		src = newFollowTestSource()
		fx.sources[endpoint] = src
	}
	return src
}

func (fx *serviceFixture) madeFor(endpoint string) int {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	n := 0
	for _, cfg := range fx.made {
		if cfg.Endpoint == endpoint {
			n++
		}
	}
	return n
}

// addProject registers a project with its LADO mode.
func (fx *serviceFixture) addProject(id domain.ProjectID, ladoMode bool) {
	fx.projects.mu.Lock()
	defer fx.projects.mu.Unlock()
	fx.projects.records = append(fx.projects.records, domain.ProjectRecord{ID: string(id), Kind: domain.ProjectKindSingleRepo})
	fx.projects.ladoMode[id] = ladoMode
}

func (fx *serviceFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := fx.svc.Start(ctx)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the runner did not stop")
		}
	})
}

func serviceWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func serviceSettings(project domain.ProjectID, sources ...domain.LadoLogSource) domain.LadoLogWatchSettings {
	s := domain.DefaultLadoLogWatchSettings(project)
	s.Enabled = true
	s.Sources = sources
	return s
}

func serviceTestSource(id, endpoint string, targets ...domain.LadoLogTarget) domain.LadoLogSource {
	for i := range targets {
		targets[i].SourceID = domain.LadoLogSourceID(id)
	}
	return domain.LadoLogSource{
		ID: domain.LadoLogSourceID(id), Kind: domain.LadoLogSourceKindDockerTCP, DisplayName: id,
		Endpoint: endpoint, Targets: targets,
	}
}

func serviceTarget(id, project string) domain.LadoLogTarget {
	return domain.LadoLogTarget{ID: domain.LadoLogTargetID(id), Kind: domain.LadoLogTargetComposeProject, Value: project}
}

func serviceAPIError(t *testing.T, err error, code string) *apierr.Error {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error = %v, want apierr %s", err, code)
	}
	if ae.Code != code {
		t.Fatalf("error code = %s (%s), want %s", ae.Code, ae.Message, code)
	}
	return ae
}

// TestServiceConnectsOnlyToWatchedSources: a project with the log watch off,
// one outside LADO mode, an archived one, a paused source, a source with
// only paused targets and a TLS source without its CA are never connected
// to; a TLS source with its CA copy is.
func TestServiceConnectsOnlyToWatchedSources(t *testing.T) {
	fx := newServiceFixture(t)
	const (
		active    = "tcp://192.0.2.10:2375"
		off       = "tcp://192.0.2.11:2375"
		modeOff   = "tcp://192.0.2.12:2375"
		archived  = "tcp://192.0.2.13:2375"
		paused    = "tcp://192.0.2.14:2375"
		allPaused = "tcp://192.0.2.15:2375"
		noCA      = "tcp://192.0.2.16:2376"
		withCA    = "tcp://192.0.2.17:2376"
	)
	fx.addProject("on", true)
	fx.addProject("off", true)
	fx.addProject("mode-off", false)
	fx.projects.records = append(fx.projects.records, domain.ProjectRecord{ID: "archived", ArchivedAt: time.Now()})
	fx.projects.ladoMode["archived"] = true

	pausedSrc := serviceTestSource("s-paused", paused, serviceTarget("t-paused", "grp"))
	pausedSrc.Paused = true
	pausedTarget := serviceTarget("t-all-paused", "grp")
	pausedTarget.Paused = true
	noCASrc := serviceTestSource("s-no-ca", noCA, serviceTarget("t-no-ca", "grp"))
	noCASrc.TLS = true
	withCASrc := serviceTestSource("s-with-ca", withCA, serviceTarget("t-with-ca", "grp"))
	withCASrc.TLS = true
	ca := newCertsTestCA(t, "ca")
	if err := fx.certs.Import(CertName("on", "s-with-ca"), certsTestWrite(t, t.TempDir(), "ca.crt", ca.pem), "", ""); err != nil {
		t.Fatalf("import CA: %v", err)
	}

	fx.store.put(serviceSettings("on",
		serviceTestSource("s-active", active, serviceTarget("t-active", "grp")),
		pausedSrc,
		serviceTestSource("s-all-paused", allPaused, pausedTarget),
		noCASrc,
		withCASrc,
	))
	offSettings := serviceSettings("off", serviceTestSource("s-off", off, serviceTarget("t-off", "grp")))
	offSettings.Enabled = false
	fx.store.put(offSettings)
	fx.store.put(serviceSettings("mode-off", serviceTestSource("s-mode-off", modeOff, serviceTarget("t-mode-off", "grp"))))
	fx.store.put(serviceSettings("archived", serviceTestSource("s-archived", archived, serviceTarget("t-archived", "grp"))))

	fx.start(t)
	serviceWait(t, "the active sources to be discovered twice", func() bool {
		return fx.source(active).discoverCount() >= 2 && fx.source(withCA).discoverCount() >= 2
	})
	// A few more reconciles.
	time.Sleep(4 * fx.svc.reconcileEvery)
	for _, endpoint := range []string{off, modeOff, archived, paused, allPaused, noCA} {
		src := fx.source(endpoint)
		if n := fx.madeFor(endpoint); n != 0 {
			t.Errorf("%s: %d sources made, want none", endpoint, n)
		}
		if n := src.discoverCount(); n != 0 {
			t.Errorf("%s: %d discovers, want none", endpoint, n)
		}
		if n := len(src.follows); n != 0 {
			t.Errorf("%s: %d follows, want none", endpoint, n)
		}
	}
	fx.mu.Lock()
	for _, cfg := range fx.made {
		if cfg.Endpoint == withCA && (cfg.CAFile == "" || !cfg.TLS) {
			t.Errorf("TLS source made without its CA copy: %+v", cfg)
		}
	}
	fx.mu.Unlock()

	view, err := fx.svc.Settings(context.Background(), "on")
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	states := map[domain.LadoLogSourceID]domain.LadoLogSourceStatus{}
	for _, st := range view.Statuses {
		states[st.SourceID] = st
	}
	if st := states["s-no-ca"]; st.State != domain.LadoLogSourceStateError || st.Detail != serviceNoCA || st.Insecure {
		t.Errorf("TLS source without CA status = %+v", st)
	}
	if st := states["s-paused"]; st.State != domain.LadoLogSourceStateOff || !st.Insecure {
		t.Errorf("paused source status = %+v", st)
	}
	if st := states["s-active"]; st.State != domain.LadoLogSourceStateWatching || !st.Insecure {
		t.Errorf("active source status = %+v", st)
	}
	if !view.TLSStored["s-with-ca"].CA || view.TLSStored["s-no-ca"].CA {
		t.Errorf("TLSStored = %+v", view.TLSStored)
	}
	if !view.LadoMode {
		t.Error("view of a LADO mode project says it is off")
	}
}

// serviceJavaLines is a Java stack trace and the ordinary lines after it,
// which close the incident.
var serviceJavaLines = []string{
	"java.lang.IllegalStateException: no stock for order 1234",
	"\tat com.example.shop.OrderService.place(OrderService.java:42)",
	"\tat com.example.shop.OrderController.post(OrderController.java:17)",
	"INFO request done",
	"INFO request done",
	"INFO request done",
	"INFO request done",
	"INFO request done",
}

func serviceSend(t *testing.T, src *followTestSource, id string, at time.Time, lines []string) {
	t.Helper()
	for i, text := range lines {
		src.send(t, id, followTestLine{stream: "stderr", ts: at.Add(time.Duration(i) * time.Millisecond), text: text})
	}
}

// TestServiceLinesBecomeOccurrences: lines go through a Detector per
// instance to the sink, with the instance's display name, image, and the
// number of attached instances of its service over every source.
func TestServiceLinesBecomeOccurrences(t *testing.T) {
	fx := newServiceFixture(t)
	const prod1, prod2 = "tcp://192.0.2.21:2375", "tcp://198.51.100.22:2375"
	fx.addProject("demo", true)
	settings := serviceSettings("demo",
		serviceTestSource("prod1", prod1, serviceTarget("t1", "grp")),
		serviceTestSource("prod2", prod2, serviceTarget("t2", "grp")),
	)
	fx.store.put(settings)
	a := Instance{ID: strings.Repeat("a", 64), Name: "grp-api-1", Image: "registry.example.test/grp/api:1.4.2", Service: "grp/api", TargetID: "t1"}
	b := Instance{ID: strings.Repeat("b", 64), Name: "grp-api-1", Image: "registry.example.test/grp/api:1.4.3", Service: "grp/api", TargetID: "t2"}
	other := Instance{ID: strings.Repeat("c", 64), Name: "grp-worker-1", Image: "worker:1", Service: "grp/worker", TargetID: "t2"}
	fx.source(prod1).setInstances(a)
	fx.source(prod2).setInstances(b, other)
	fx.start(t)
	serviceWait(t, "both sources to follow their instances", func() bool {
		return fx.source(prod1).openCount(a.ID) == 1 && fx.source(prod2).openCount(b.ID) == 1 && fx.source(prod2).openCount(other.ID) == 1
	})
	serviceWait(t, "the runner to see the attached instances", func() bool {
		return fx.svc.serviceInstances("demo", a) == 2
	})

	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	serviceSend(t, fx.source(prod1), a.ID, at, serviceJavaLines)
	serviceSend(t, fx.source(prod2), b.ID, at.Add(time.Second), serviceJavaLines)
	serviceWait(t, "two occurrences", func() bool { return len(fx.sink.occurrences()) >= 2 })

	got := fx.sink.occurrences()
	if len(got) != 2 {
		t.Fatalf("occurrences = %d, want 2", len(got))
	}
	byInstance := map[string]serviceOccurrence{}
	for _, o := range got {
		byInstance[o.o.Instance] = o
		if o.project != "demo" || o.settings.ProjectID != "demo" {
			t.Errorf("occurrence of project %q with settings of %q", o.project, o.settings.ProjectID)
		}
		if o.o.Service != "grp/api" || o.o.ServiceInstances != 2 {
			t.Errorf("occurrence service %q instances %d, want grp/api 2", o.o.Service, o.o.ServiceInstances)
		}
		if o.o.Incident.ExceptionKind != "java.lang.IllegalStateException" {
			t.Errorf("exception kind = %q", o.o.Incident.ExceptionKind)
		}
	}
	one, two := byInstance["prod1/grp-api-1"], byInstance["prod2/grp-api-1"]
	if one.o.Image != a.Image || two.o.Image != b.Image {
		t.Errorf("instances = %+v", byInstance)
	}
	if one.o.Incident.Fingerprint == "" || one.o.Incident.Fingerprint != two.o.Incident.Fingerprint {
		t.Errorf("replicas on two hosts got fingerprints %q and %q", one.o.Incident.Fingerprint, two.o.Incident.Fingerprint)
	}

	// The cursors of the lines handed on are saved.
	serviceWait(t, "the cursor to be saved", func() bool {
		c, ok, _ := fx.store.LadoLogCursor(context.Background(), "t1", a.Name)
		return ok && !c.Since.Before(at.Add(time.Duration(len(serviceJavaLines)-1)*time.Millisecond))
	})
	view, _ := fx.svc.Settings(context.Background(), "demo")
	for _, st := range view.Statuses {
		if st.SourceID == "prod2" && (len(st.Targets) != 1 || st.Targets[0].Instances != 2 || st.Targets[0].LastLineAt.IsZero()) {
			t.Errorf("prod2 target status = %+v", st.Targets)
		}
	}

	// The incidents say how many instances of their service are attached
	// now: the n of "n개 중 m개" in the task view.
	fx.store.mu.Lock()
	fx.store.fingerprints[1] = domain.LadoLogFingerprint{ID: 1, ProjectID: "demo", Fingerprint: "aa", Service: "grp/api"}
	fx.store.fingerprints[2] = domain.LadoLogFingerprint{ID: 2, ProjectID: "demo", Fingerprint: "bb", Service: "grp/worker"}
	fx.store.fingerprints[3] = domain.LadoLogFingerprint{ID: 3, ProjectID: "demo", Fingerprint: "cc", Service: "grp/gone"}
	fx.store.mu.Unlock()
	fps, err := fx.svc.Incidents(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	attached := map[string]int{}
	for _, fp := range fps {
		attached[fp.Service] = fp.ServiceInstances
	}
	if want := map[string]int{"grp/api": 2, "grp/worker": 1, "grp/gone": 0}; !reflect.DeepEqual(attached, want) {
		t.Errorf("service instances = %v, want %v", attached, want)
	}
}

// TestServiceSourceDownIsToldOnceAndReadsOnFromTheCursor: a source down
// longer than the alert minutes is told of once, without an issue; when it
// connects again the instance is read from its cursor.
func TestServiceSourceDownIsToldOnceAndReadsOnFromTheCursor(t *testing.T) {
	fx := newServiceFixture(t)
	const prod = "tcp://192.0.2.31:2375"
	fx.addProject("demo", true)
	settings := serviceSettings("demo", serviceTestSource("prod", prod, serviceTarget("t1", "grp")))
	settings.DisconnectAlertMinutes = 10
	fx.store.put(settings)
	inst := Instance{ID: strings.Repeat("d", 64), Name: "grp-api-1", Service: "grp/api", TargetID: "t1"}
	cursor := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	_ = fx.store.PutLadoLogCursor(context.Background(), domain.LadoLogCursor{TargetID: "t1", Instance: inst.Name, Since: cursor})
	src := fx.source(prod)
	src.setInstances(inst)
	src.setDiscoverErr(errors.New("dial tcp: connection refused"))
	fx.start(t)

	serviceWait(t, "the disconnection to be recorded", func() bool {
		for _, c := range fx.store.downCalls() {
			if !c.downSince.IsZero() && c.notified.IsZero() {
				return true
			}
		}
		return false
	})
	time.Sleep(5 * fx.svc.flushEvery)
	if n := len(fx.notices.list()); n != 0 {
		t.Fatalf("notices before the alert minutes = %d", n)
	}
	fx.advance(11 * time.Minute)
	serviceWait(t, "the source down notice", func() bool { return len(fx.notices.list()) == 1 })
	time.Sleep(10 * fx.svc.flushEvery)
	notices := fx.notices.list()
	if len(notices) != 1 {
		t.Fatalf("notices = %d, want 1: %+v", len(notices), notices)
	}
	n := notices[0]
	if !strings.HasPrefix(n.Key, "log_source_down:prod:") || n.ProjectID != "demo" {
		t.Errorf("notice = %+v", n)
	}
	if want := domain.LadoNoticeLogSourceDown("prod", 11); n.Text != want {
		t.Errorf("notice text = %q, want %q", n.Text, want)
	}
	if got := fx.sink.occurrences(); len(got) != 0 {
		t.Errorf("a disconnection made occurrences: %+v", got)
	}

	src.setDiscoverErr(nil)
	serviceWait(t, "the follow from the cursor", func() bool { return len(src.calls(inst.ID)) > 0 })
	if from := src.calls(inst.ID)[0].from; from.TailZero || !from.Since.Equal(cursor) {
		t.Errorf("follow after reconnecting from %+v, want since %v", from, cursor)
	}
	serviceWait(t, "the disconnection to be cleared", func() bool {
		calls := fx.store.downCalls()
		last := calls[len(calls)-1]
		return last.downSince.IsZero() && last.notified.IsZero()
	})
	if n := len(fx.notices.list()); n != 1 {
		t.Errorf("notices after reconnecting = %d, want 1", n)
	}
}

// TestServiceSettingsChangesReachTheFollowers: pausing a source closes its
// streams; turning it on again follows it again.
func TestServiceSettingsChangesReachTheFollowers(t *testing.T) {
	fx := newServiceFixture(t)
	fx.svc.reconcileEvery = time.Hour // only Kick reconciles
	const prod = "tcp://192.0.2.41:2375"
	fx.addProject("demo", true)
	fx.start(t)
	in := domain.LadoLogWatchInput{
		Enabled: true, HourlyIssueCap: 5, AutoPlan: true, AutoPlanDailyCap: 5, AutoPlanMinCount: 1, DisconnectAlertMinutes: 10,
		Sources: []domain.LadoLogSourceInput{{
			DisplayName: "prod", Endpoint: prod,
			Targets: []domain.LadoLogTargetInput{{Kind: domain.LadoLogTargetComposeProject, Value: "grp"}},
		}},
	}
	view, err := fx.svc.UpdateSettings(context.Background(), "demo", in)
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	srcID := view.Settings.Sources[0].ID
	targetID := view.Settings.Sources[0].Targets[0].ID
	inst := Instance{ID: strings.Repeat("e", 64), Name: "grp-api-1", Service: "grp/api", TargetID: string(targetID)}
	fx.source(prod).setInstances(inst)
	serviceWait(t, "the stream to open", func() bool { return fx.source(prod).openCount(inst.ID) == 1 })

	in.Sources[0].ID = srcID
	in.Sources[0].Targets[0].ID = targetID
	in.Sources[0].Paused = true
	if _, err := fx.svc.UpdateSettings(context.Background(), "demo", in); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	serviceWait(t, "the stream to close", func() bool { return fx.source(prod).openCount(inst.ID) == 0 })
	discovers := fx.source(prod).discoverCount()
	time.Sleep(100 * time.Millisecond)
	if n := fx.source(prod).discoverCount(); n != discovers {
		t.Errorf("a paused source was discovered %d more times", n-discovers)
	}

	in.Sources[0].Paused = false
	in.Include = []string{"WARN payment"}
	if _, err := fx.svc.UpdateSettings(context.Background(), "demo", in); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	serviceWait(t, "the stream to open again", func() bool { return fx.source(prod).openCount(inst.ID) == 1 })

	// A rules change alone keeps the stream and uses the new rules.
	in.Include = []string{"WARN refund"}
	if _, err := fx.svc.UpdateSettings(context.Background(), "demo", in); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	serviceWait(t, "the new rules", func() bool {
		for _, run := range fx.svc.runningSources() {
			run.mu.Lock()
			include := run.settings.Include
			run.mu.Unlock()
			if len(include) == 1 && include[0] == "WARN refund" {
				return true
			}
		}
		return false
	})
	calls := len(fx.source(prod).calls(inst.ID))
	fx.source(prod).send(t, inst.ID, followTestLine{stream: "stdout", ts: time.Now(), text: "WARN refund failed for order 7"})
	for i := 0; i < 5; i++ {
		fx.source(prod).send(t, inst.ID, followTestLine{stream: "stdout", ts: time.Now(), text: "INFO fine"})
	}
	serviceWait(t, "the incident of the new rule", func() bool { return len(fx.sink.occurrences()) == 1 })
	if n := len(fx.source(prod).calls(inst.ID)); n != calls {
		t.Errorf("a rules change reopened the stream (%d follows, was %d)", n, calls)
	}
}

func serviceInput(sources ...domain.LadoLogSourceInput) domain.LadoLogWatchInput {
	return domain.LadoLogWatchInput{
		Enabled: true, HourlyIssueCap: 5, AutoPlan: true, AutoPlanDailyCap: 5, AutoPlanMinCount: 1,
		DisconnectAlertMinutes: 10, Include: []string{}, Exclude: []string{}, Sources: sources,
	}
}

func serviceSourceInput(endpoint string, targets ...domain.LadoLogTargetInput) domain.LadoLogSourceInput {
	return domain.LadoLogSourceInput{DisplayName: "prod", Endpoint: endpoint, Targets: targets}
}

var serviceGrp = domain.LadoLogTargetInput{Kind: domain.LadoLogTargetComposeProject, Value: "grp"}

// TestServiceUpdateSettingsRefusals: a refused input is
// LADO_LOG_WATCH_INVALID with its field, and nothing is saved.
func TestServiceUpdateSettingsRefusals(t *testing.T) {
	fx := newServiceFixture(t)
	fx.addProject("demo", true)
	label := func(v string) domain.LadoLogTargetInput {
		return domain.LadoLogTargetInput{Kind: domain.LadoLogTargetLabel, Value: v}
	}
	badRegexp := serviceInput(serviceSourceInput("tcp://192.0.2.1:2375", serviceGrp))
	badRegexp.Include = []string{"(unclosed"}
	cases := []struct {
		name string
		in   domain.LadoLogWatchInput
	}{
		{"unix endpoint", serviceInput(serviceSourceInput("unix:///var/run/docker.sock", serviceGrp))},
		{"npipe endpoint", serviceInput(serviceSourceInput("npipe:////./pipe/docker_engine", serviceGrp))},
		{"ssh endpoint", serviceInput(serviceSourceInput("ssh://deploy@192.0.2.1", serviceGrp))},
		{"http endpoint", serviceInput(serviceSourceInput("http://192.0.2.1:2375", serviceGrp))},
		{"user information", serviceInput(serviceSourceInput("tcp://user:pw@192.0.2.1:2375", serviceGrp))},
		{"no port", serviceInput(serviceSourceInput("tcp://192.0.2.1", serviceGrp))},
		{"port 0", serviceInput(serviceSourceInput("tcp://192.0.2.1:0", serviceGrp))},
		{"port 70000", serviceInput(serviceSourceInput("tcp://192.0.2.1:70000", serviceGrp))},
		{"path", serviceInput(serviceSourceInput("tcp://192.0.2.1:2375/v1.43", serviceGrp))},
		{"query", serviceInput(serviceSourceInput("tcp://192.0.2.1:2375?x=1", serviceGrp))},
		{"host starting with -", serviceInput(serviceSourceInput("tcp://-oProxy.example.test:2375", serviceGrp))},
		{"bad label", serviceInput(serviceSourceInput("tcp://192.0.2.1:2375", label("app=`whoami`")))},
		{"label with a space", serviceInput(serviceSourceInput("tcp://192.0.2.1:2375", label("app name")))},
		{"bad compose project", serviceInput(serviceSourceInput("tcp://192.0.2.1:2375", domain.LadoLogTargetInput{Kind: domain.LadoLogTargetComposeProject, Value: "grp;rm"}))},
		{"bad container name", serviceInput(serviceSourceInput("tcp://192.0.2.1:2375", domain.LadoLogTargetInput{Kind: domain.LadoLogTargetName, Value: "/api"}))},
		{"bad regexp", badRegexp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fx.svc.UpdateSettings(context.Background(), "demo", tc.in)
			ae := serviceAPIError(t, err, codeInvalid)
			if ae.Kind != apierr.KindInvalid {
				t.Errorf("kind = %v, want invalid", ae.Kind)
			}
			if field, _ := ae.Details["field"].(string); field == "" {
				t.Errorf("details = %+v, want a field", ae.Details)
			}
		})
	}
	if fx.store.puts != 0 {
		t.Fatalf("a refused input was saved %d times", fx.store.puts)
	}
}

// TestServiceUpdateSettingsGivesIDsAndDeletesRemovedCopies: new sources and
// targets get ids, ids the project does not have are replaced, saved ids
// stay, and a removed source's TLS copies go with it.
func TestServiceUpdateSettingsGivesIDsAndDeletesRemovedCopies(t *testing.T) {
	fx := newServiceFixture(t)
	fx.addProject("demo", true)
	ctx := context.Background()
	foreign := serviceSourceInput("tcp://192.0.2.2:2376", domain.LadoLogTargetInput{ID: "not-ours", Kind: domain.LadoLogTargetName, Value: "api"})
	foreign.ID = "someone-elses"
	foreign.DisplayName = "prod2"
	foreign.TLS = true
	view, err := fx.svc.UpdateSettings(ctx, "demo", serviceInput(serviceSourceInput("tcp://192.0.2.1:2375", serviceGrp), foreign))
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	sources := view.Settings.Sources
	if len(sources) != 2 || sources[0].ID == "" || sources[1].ID == "" || sources[1].ID == "someone-elses" ||
		sources[1].Targets[0].ID == "not-ours" || sources[0].Targets[0].ID == "" {
		t.Fatalf("sources = %+v", sources)
	}
	for _, src := range sources {
		if src.CertKey != CertName("demo", string(src.ID)) || src.Kind != domain.LadoLogSourceKindDockerTCP {
			t.Errorf("source %s: cert key %q kind %q", src.ID, src.CertKey, src.Kind)
		}
	}

	// TLS copies for the second source.
	dir := t.TempDir()
	ca := newCertsTestCA(t, "ca")
	caPath := certsTestWrite(t, dir, "ca.crt", ca.pem)
	view, err = fx.svc.ImportTLS(ctx, "demo", sources[1].ID, domain.LadoLogTLSImport{CAPath: caPath})
	if err != nil {
		t.Fatalf("ImportTLS: %v", err)
	}
	if !view.TLSStored[sources[1].ID].CA {
		t.Fatalf("TLSStored = %+v", view.TLSStored)
	}

	// Keep the first source with its ids, drop the second.
	keep := serviceSourceInput("tcp://192.0.2.1:2375", domain.LadoLogTargetInput{ID: sources[0].Targets[0].ID, Kind: domain.LadoLogTargetComposeProject, Value: "grp2"})
	keep.ID = sources[0].ID
	view, err = fx.svc.UpdateSettings(ctx, "demo", serviceInput(keep))
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got := view.Settings.Sources; len(got) != 1 || got[0].ID != sources[0].ID || got[0].Targets[0].ID != sources[0].Targets[0].ID || got[0].Targets[0].Value != "grp2" {
		t.Fatalf("sources after the second save = %+v", got)
	}
	if hasCA, _, _ := fx.certs.Stored(CertName("demo", string(sources[1].ID))); hasCA {
		t.Fatal("the removed source's TLS copies are still there")
	}
}

// TestServiceImportTLSRefusals: an encrypted key, a certificate and key that
// do not pair, a file that is not PEM and an unknown source are refused with
// their codes, and the messages name no path.
func TestServiceImportTLSRefusals(t *testing.T) {
	fx := newServiceFixture(t)
	fx.addProject("demo", true)
	ctx := context.Background()
	src := serviceTestSource("prod", "tcp://192.0.2.1:2376", serviceTarget("t1", "grp"))
	src.TLS = true
	fx.store.put(serviceSettings("demo", src))

	dir := t.TempDir()
	ca := newCertsTestCA(t, "ca")
	certPEM, keyPEM, _ := ca.issue(t, false)
	_, otherKey, _ := ca.issue(t, false)
	caPath := certsTestWrite(t, dir, "ca.crt", ca.pem)
	certPath := certsTestWrite(t, dir, "client.crt", certPEM)
	keyPath := certsTestWrite(t, dir, "client.key", keyPEM)
	otherKeyPath := certsTestWrite(t, dir, "other.key", otherKey)
	textPath := certsTestWrite(t, dir, "notes.txt", []byte("not a certificate\n"))
	encrypted := certsTestWrite(t, dir, "enc.key", []byte("-----BEGIN ENCRYPTED PRIVATE KEY-----\nc2VhbGVk\n-----END ENCRYPTED PRIVATE KEY-----\n"))

	cases := []struct {
		name   string
		source domain.LadoLogSourceID
		in     domain.LadoLogTLSImport
		code   string
	}{
		{"encrypted key", "prod", domain.LadoLogTLSImport{CAPath: caPath, CertPath: certPath, KeyPath: encrypted}, codeCertKeyEncrypted},
		{"certificate and key do not pair", "prod", domain.LadoLogTLSImport{CAPath: caPath, CertPath: certPath, KeyPath: otherKeyPath}, codeCertInvalid},
		{"CA not PEM", "prod", domain.LadoLogTLSImport{CAPath: textPath}, codeCertInvalid},
		{"unknown source", "nope", domain.LadoLogTLSImport{CAPath: caPath}, codeSourceNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fx.svc.ImportTLS(ctx, "demo", tc.source, tc.in)
			ae := serviceAPIError(t, err, tc.code)
			if strings.Contains(ae.Message, dir) {
				t.Errorf("message %q names a path", ae.Message)
			}
		})
	}
	if hasCA, _, _ := fx.certs.Stored(CertName("demo", "prod")); hasCA {
		t.Fatal("a refused import stored copies")
	}

	view, err := fx.svc.ImportTLS(ctx, "demo", "prod", domain.LadoLogTLSImport{CAPath: caPath, CertPath: certPath, KeyPath: keyPath})
	if err != nil {
		t.Fatalf("ImportTLS: %v", err)
	}
	if got := view.TLSStored["prod"]; !got.CA || !got.Cert || !got.Key {
		t.Fatalf("TLSStored = %+v", got)
	}
	if s, _, _ := fx.store.LadoLogWatchSettings(ctx, "demo"); s.Sources[0].CertKey != CertName("demo", "prod") {
		t.Errorf("cert key = %q", s.Sources[0].CertKey)
	}
	view, err = fx.svc.DeleteTLS(ctx, "demo", "prod")
	if err != nil {
		t.Fatalf("DeleteTLS: %v", err)
	}
	if got := view.TLSStored["prod"]; got.CA || got.Cert || got.Key {
		t.Fatalf("TLSStored after delete = %+v", got)
	}
	if _, err := fx.svc.DeleteTLS(ctx, "demo", "nope"); err == nil {
		t.Fatal("DeleteTLS of an unknown source succeeded")
	} else {
		serviceAPIError(t, err, codeSourceNotFound)
	}
}

// TestServiceCheck: a connection check of a saved source, a failed one, a
// TLS source without its CA and an unknown source.
func TestServiceCheck(t *testing.T) {
	fx := newServiceFixture(t)
	fx.addProject("demo", true)
	ctx := context.Background()
	paused := serviceTarget("t2", "other")
	paused.Paused = true
	noCA := serviceTestSource("tls", "tcp://192.0.2.2:2376", serviceTarget("t3", "grp"))
	noCA.TLS = true
	fx.store.put(serviceSettings("demo", serviceTestSource("prod", "tcp://192.0.2.1:2375", serviceTarget("t1", "grp"), paused), noCA))

	fx.check = func(cfg DockerConfig) (DockerCheck, error) {
		return DockerCheck{EngineVersion: "27.3.1", APIVersion: "1.47", Insecure: !cfg.TLS, Instances: []Instance{
			{ID: strings.Repeat("a", 64), Name: "grp-api-1", Image: "api:1", Service: "grp/api", TargetID: "t1"},
		}}, nil
	}
	check, err := fx.svc.Check(ctx, "demo", "prod")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !check.OK || check.EngineVersion != "27.3.1" || check.APIVersion != "1.47" || !check.Insecure ||
		len(check.Instances) != 1 || check.Instances[0].Service != "grp/api" || check.Instances[0].State != "running" {
		t.Fatalf("check = %+v", check)
	}
	fx.mu.Lock()
	cfg := fx.checks[0]
	fx.mu.Unlock()
	if len(cfg.Targets) != 2 {
		t.Errorf("check targets = %+v, want the paused one too", cfg.Targets)
	}

	fx.check = func(DockerConfig) (DockerCheck, error) {
		return DockerCheck{}, errors.New("dial tcp: connection refused")
	}
	check, err = fx.svc.Check(ctx, "demo", "prod")
	if err != nil || check.OK || !strings.Contains(check.Detail, "connection refused") {
		t.Fatalf("failed check = %+v, %v", check, err)
	}

	before := len(fx.checks)
	check, err = fx.svc.Check(ctx, "demo", "tls")
	if err != nil || check.OK || check.Detail != serviceNoCA || len(fx.checks) != before {
		t.Fatalf("check of a TLS source without CA = %+v, %v (%d checks)", check, err, len(fx.checks)-before)
	}

	_, err = fx.svc.Check(ctx, "demo", "nope")
	serviceAPIError(t, err, codeSourceNotFound)
	fx.addProject("other", false)
	_, err = fx.svc.Check(ctx, "other", "prod")
	serviceAPIError(t, err, codeSourceNotFound)
}

const serviceCIExample = `variables:
  PROD1_HOST: 192.0.2.10
  PROD2_HOST:
    value: 198.51.100.20
    description: second server
deploy:
  script:
    - DOCKER_HOST=tcp://$PROD1_HOST:2375 docker-compose -p grp up -d
    - DOCKER_HOST=tcp://${PROD2_HOST}:2375 docker-compose -p grp up -d
    - DOCKER_HOST=tcp://$SECRET_HOST:2375 docker-compose -p grp up -d
`

// TestServiceImportFromCI: the candidates of a project's .gitlab-ci.yml and
// of a workspace's repositories, without a change to the saved settings.
func TestServiceImportFromCI(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	single := t.TempDir()
	if err := os.WriteFile(filepath.Join(single, ".gitlab-ci.yml"), []byte(serviceCIExample), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	for _, sub := range []string{"back", "front", "linked"} {
		if err := os.MkdirAll(filepath.Join(workspace, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "back", ".gitlab-ci.yml"), []byte(serviceCIExample), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(single, ".gitlab-ci.yml"), filepath.Join(workspace, "linked", ".gitlab-ci.yml")); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	fx.projects.records = []domain.ProjectRecord{
		{ID: "single", Path: single, Kind: domain.ProjectKindSingleRepo},
		{ID: "ws", Path: workspace, Kind: domain.ProjectKindWorkspace},
		{ID: "empty", Path: empty},
		{ID: "scratch", Path: empty, Kind: domain.ProjectKindScratch},
		{ID: "archived", Path: single, ArchivedAt: time.Now()},
		{ID: "gone", Path: filepath.Join(empty, "missing")},
	}
	fx.projects.repos["ws"] = []domain.WorkspaceRepoRecord{
		{RelativePath: "back"}, {RelativePath: "front"}, {RelativePath: "linked"}, {RelativePath: "../escape"},
	}
	saved := serviceSettings("single", serviceTestSource("prod", "tcp://192.0.2.99:2375", serviceTarget("t1", "old")))
	fx.store.put(saved)
	before, _, _ := fx.store.LadoLogWatchSettings(ctx, "single")

	found, err := fx.svc.ImportFromCI(ctx, "single")
	if err != nil {
		t.Fatalf("ImportFromCI: %v", err)
	}
	if len(found.Files) != 1 {
		t.Fatalf("files = %+v", found.Files)
	}
	file := found.Files[0]
	endpoints := map[string]bool{}
	unresolved := false
	for _, h := range file.Hosts {
		if h.Endpoint != "" {
			endpoints[h.Endpoint] = true
		}
		if strings.Contains(h.Unresolved, "SECRET_HOST") {
			unresolved = true
		}
	}
	if file.Repo != "" || file.Path != ".gitlab-ci.yml" || !endpoints["tcp://192.0.2.10:2375"] || !endpoints["tcp://198.51.100.20:2375"] ||
		!unresolved || !reflect.DeepEqual(file.ComposeProjects, []string{"grp"}) {
		t.Fatalf("file = %+v", file)
	}
	after, _, _ := fx.store.LadoLogWatchSettings(ctx, "single")
	if fx.store.puts != 0 || !reflect.DeepEqual(before, after) {
		t.Fatalf("ImportFromCI changed the settings: %+v -> %+v", before, after)
	}

	found, err = fx.svc.ImportFromCI(ctx, "ws")
	if err != nil {
		t.Fatalf("ImportFromCI of a workspace: %v", err)
	}
	repos := map[string]domain.LadoLogCIFile{}
	for _, f := range found.Files {
		repos[f.Repo] = f
	}
	if len(found.Files) != 2 || len(repos["back"].Hosts) == 0 || len(repos["linked"].Hosts) != 0 || len(repos["linked"].Notes) != 1 {
		t.Fatalf("workspace files = %+v", found.Files)
	}

	for project, code := range map[domain.ProjectID]string{
		"empty": codeCINotFound, "scratch": codeCIUnsupported, "archived": codeCIUnsupported, "gone": codeCIUnsupported,
		"unknown": "PROJECT_NOT_FOUND",
	} {
		_, err := fx.svc.ImportFromCI(ctx, project)
		serviceAPIError(t, err, code)
	}
	if fx.store.puts != 0 {
		t.Fatal("ImportFromCI saved settings")
	}
}

// TestServiceSettingsIncidentsAndIgnore: a project that never saved settings
// gets the defaults; the ignore list holds only the project's own
// fingerprints.
func TestServiceSettingsIncidentsAndIgnore(t *testing.T) {
	fx := newServiceFixture(t)
	fx.addProject("demo", false)
	ctx := context.Background()
	view, err := fx.svc.Settings(ctx, "demo")
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !reflect.DeepEqual(view.Settings, domain.DefaultLadoLogWatchSettings("demo")) || view.LadoMode || len(view.Statuses) != 0 {
		t.Fatalf("default view = %+v", view)
	}

	fx.store.fingerprints[1] = domain.LadoLogFingerprint{ID: 1, ProjectID: "demo", Fingerprint: "aa", Count: 3}
	fx.store.fingerprints[2] = domain.LadoLogFingerprint{ID: 2, ProjectID: "other", Fingerprint: "bb"}
	fps, err := fx.svc.Incidents(ctx, "demo")
	if err != nil || len(fps) != 1 || fps[0].ID != 1 {
		t.Fatalf("Incidents = %+v, %v", fps, err)
	}
	fx.addProject("none", false)
	if fps, err := fx.svc.Incidents(ctx, "none"); err != nil || fps == nil || len(fps) != 0 {
		t.Fatalf("Incidents of a project without any = %#v, %v", fps, err)
	}

	fp, err := fx.svc.SetIgnored(ctx, "demo", 1, true)
	if err != nil || !fp.Ignored || fp.IgnoredAt.IsZero() {
		t.Fatalf("SetIgnored = %+v, %v", fp, err)
	}
	fp, err = fx.svc.SetIgnored(ctx, "demo", 1, false)
	if err != nil || fp.Ignored {
		t.Fatalf("SetIgnored(false) = %+v, %v", fp, err)
	}
	_, err = fx.svc.SetIgnored(ctx, "demo", 2, true)
	serviceAPIError(t, err, codeFingerprintAbsent)
	_, err = fx.svc.SetIgnored(ctx, "demo", 99, true)
	serviceAPIError(t, err, codeFingerprintAbsent)
	if fx.store.fingerprints[2].Ignored {
		t.Fatal("another project's fingerprint was ignored")
	}
}

// TestServiceUnknownProjectIsNotFound: the settings rows have no foreign key
// to projects, so every method refuses a project AO does not have with
// PROJECT_NOT_FOUND, and a PUT saves nothing. An archived project still
// reads and saves its settings.
func TestServiceUnknownProjectIsNotFound(t *testing.T) {
	fx := newServiceFixture(t)
	fx.addProject("demo", true)
	ctx := context.Background()
	in := domain.LadoLogWatchInput{Enabled: true, HourlyIssueCap: 5, AutoPlanDailyCap: 5, AutoPlanMinCount: 1, DisconnectAlertMinutes: 10,
		Sources: []domain.LadoLogSourceInput{{DisplayName: "prod1", Endpoint: "tcp://192.0.2.10:2375",
			Targets: []domain.LadoLogTargetInput{{Kind: domain.LadoLogTargetComposeProject, Value: "grp"}}}}}
	const missing = domain.ProjectID("demo-typo")
	calls := map[string]func() error{
		"Settings":       func() error { _, err := fx.svc.Settings(ctx, missing); return err },
		"UpdateSettings": func() error { _, err := fx.svc.UpdateSettings(ctx, missing, in); return err },
		"ImportTLS": func() error {
			_, err := fx.svc.ImportTLS(ctx, missing, "s1", domain.LadoLogTLSImport{CAPath: "/nowhere/ca.pem"})
			return err
		},
		"DeleteTLS":    func() error { _, err := fx.svc.DeleteTLS(ctx, missing, "s1"); return err },
		"Check":        func() error { _, err := fx.svc.Check(ctx, missing, "s1"); return err },
		"ImportFromCI": func() error { _, err := fx.svc.ImportFromCI(ctx, missing); return err },
		"Incidents":    func() error { _, err := fx.svc.Incidents(ctx, missing); return err },
		"SetIgnored":   func() error { _, err := fx.svc.SetIgnored(ctx, missing, 1, true); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) { serviceAPIError(t, call(), "PROJECT_NOT_FOUND") })
	}
	if fx.store.puts != 0 {
		t.Fatalf("settings of an unknown project were saved %d times", fx.store.puts)
	}

	fx.projects.mu.Lock()
	fx.projects.records[0].ArchivedAt = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	fx.projects.mu.Unlock()
	if _, err := fx.svc.UpdateSettings(ctx, "demo", in); err != nil {
		t.Fatalf("UpdateSettings of an archived project: %v", err)
	}
	if view, err := fx.svc.Settings(ctx, "demo"); err != nil || len(view.Settings.Sources) != 1 {
		t.Fatalf("Settings of an archived project = %+v, %v", view, err)
	}
}

// serviceFakeRepos is a fake ServiceRepos.
type serviceFakeRepos struct {
	repos []ladotask.ProjectRepo
	err   error
	calls int
}

func (r *serviceFakeRepos) Repositories(context.Context, domain.ProjectID) ([]ladotask.ProjectRepo, error) {
	r.calls++
	return r.repos, r.err
}

var (
	serviceBackRepo  = domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/back"}
	serviceFrontRepo = domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/front"}
)

// serviceTwoRepos is a workspace project's two repositories.
func serviceTwoRepos() *serviceFakeRepos {
	return &serviceFakeRepos{repos: []ladotask.ProjectRepo{{Name: "back", Repo: serviceBackRepo}, {Name: "front", Repo: serviceFrontRepo}}}
}

func serviceRepoTarget(value, repo string) domain.LadoLogTargetInput {
	return domain.LadoLogTargetInput{Kind: domain.LadoLogTargetComposeProject, Value: value, Repository: repo}
}

// TestServiceUpdateSettingsChecksRepositories: in a project with two
// repositories, a repository that is not one of them is refused with its
// field, a followed target whose issues would have no repository is
// refused, and what is saved is the canonical ref.
func TestServiceUpdateSettingsChecksRepositories(t *testing.T) {
	const endpoint = "tcp://192.0.2.1:2375"
	refused := []struct {
		name, field string
		in          domain.LadoLogWatchInput
	}{
		{"project repository not in the list", "repository", func() domain.LadoLogWatchInput {
			in := serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("back", "")))
			in.Repository = "github:acme/other"
			return in
		}()},
		{"project repository that does not parse", "repository", func() domain.LadoLogWatchInput {
			in := serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("back", "")))
			in.Repository = "acme/back"
			return in
		}()},
		{"target repository not in the list", "sources[0].targets[1].repository",
			serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("back", "github:acme/back"), serviceRepoTarget("front", "gitlab:acme/front")))},
		{"target repository of a paused target not in the list", "sources[1].targets[0].repository", func() domain.LadoLogWatchInput {
			paused := serviceSourceInput("tcp://192.0.2.2:2375", serviceRepoTarget("front", "github:acme/other"))
			paused.Paused = true
			paused.DisplayName = "prod2"
			in := serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("back", "github:acme/back")), paused)
			in.Enabled = false
			return in
		}()},
		{"followed target without any repository", "sources[0].targets[1].repository",
			serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("back", "github:acme/back"), serviceRepoTarget("front", "")))},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			fx := newServiceFixture(t)
			fx.addProject("demo", true)
			fx.svc.deps.Repos = serviceTwoRepos()
			_, err := fx.svc.UpdateSettings(context.Background(), "demo", tc.in)
			ae := serviceAPIError(t, err, codeInvalid)
			if ae.Kind != apierr.KindInvalid {
				t.Errorf("kind = %v, want invalid", ae.Kind)
			}
			if field, _ := ae.Details["field"].(string); field != tc.field {
				t.Errorf("field = %q, want %q", field, tc.field)
			}
			if fx.store.puts != 0 {
				t.Errorf("a refused input was saved")
			}
		})
	}

	t.Run("canonical refs are saved, the project default serves targets without one", func(t *testing.T) {
		fx := newServiceFixture(t)
		fx.addProject("demo", true)
		fx.svc.deps.Repos = serviceTwoRepos()
		in := serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("back", ""), serviceRepoTarget("front", "github:ACME/Front")))
		in.Repository = "github:Acme/BACK"
		view, err := fx.svc.UpdateSettings(context.Background(), "demo", in)
		if err != nil {
			t.Fatalf("UpdateSettings: %v", err)
		}
		targets := view.Settings.Sources[0].Targets
		if view.Settings.Repository != "github:acme/back" || targets[0].Repository != "" || targets[1].Repository != "github:acme/front" {
			t.Fatalf("saved project %q targets %+v", view.Settings.Repository, targets)
		}
		if in.Repository != "github:Acme/BACK" || in.Sources[0].Targets[1].Repository != "github:ACME/Front" {
			t.Fatal("UpdateSettings changed the caller's input")
		}
	})
}

// TestServiceUpdateSettingsRepositoryNotNeededWhenNotFollowed: a target
// without a repository is saved when the log watch is off or the target or
// its source is paused; nothing could make an issue for it.
func TestServiceUpdateSettingsRepositoryNotNeededWhenNotFollowed(t *testing.T) {
	const endpoint = "tcp://192.0.2.1:2375"
	empty := serviceRepoTarget("front", "")
	off := serviceInput(serviceSourceInput(endpoint, empty))
	off.Enabled = false
	pausedSource := serviceSourceInput(endpoint, empty)
	pausedSource.Paused = true
	pausedTarget := empty
	pausedTarget.Paused = true
	for name, in := range map[string]domain.LadoLogWatchInput{
		"log watch off": off,
		"source paused": serviceInput(pausedSource),
		"target paused": serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("back", "github:acme/back"), pausedTarget)),
		"no sources":    serviceInput(),
		"project default": func() domain.LadoLogWatchInput {
			in := serviceInput(serviceSourceInput(endpoint, empty))
			in.Repository = "github:acme/front"
			return in
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			fx := newServiceFixture(t)
			fx.addProject("demo", true)
			fx.svc.deps.Repos = serviceTwoRepos()
			if _, err := fx.svc.UpdateSettings(context.Background(), "demo", in); err != nil {
				t.Fatalf("UpdateSettings: %v", err)
			}
		})
	}
}

// TestServiceUpdateSettingsRepositoryList: a project with one repository
// saves empty repositories as before; a list that cannot be read for a
// reason other than an internal error skips the checks; an internal error
// is LADO_LOG_WATCH_FAILED.
func TestServiceUpdateSettingsRepositoryList(t *testing.T) {
	const endpoint = "tcp://192.0.2.1:2375"
	ctx := context.Background()
	in := serviceInput(serviceSourceInput(endpoint, serviceRepoTarget("api", "")))

	fx := newServiceFixture(t)
	fx.addProject("demo", true)
	repos := &serviceFakeRepos{repos: []ladotask.ProjectRepo{{Name: "demo", Repo: serviceBackRepo}}}
	fx.svc.deps.Repos = repos
	view, err := fx.svc.UpdateSettings(ctx, "demo", in)
	if err != nil {
		t.Fatalf("UpdateSettings with one repository: %v", err)
	}
	if view.Settings.Repository != "" || view.Settings.Sources[0].Targets[0].Repository != "" || repos.calls != 1 {
		t.Fatalf("saved %+v after %d list reads", view.Settings, repos.calls)
	}
	wrong := in
	wrong.Repository = "github:acme/front"
	_, err = fx.svc.UpdateSettings(ctx, "demo", wrong)
	if ae := serviceAPIError(t, err, codeInvalid); ae.Details["field"] != "repository" {
		t.Fatalf("details = %+v", ae.Details)
	}

	unchecked := in
	unchecked.Repository = "github:acme/elsewhere"
	for name, listErr := range map[string]error{
		"archived project":     apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project"),
		"unsupported origin":   apierr.Conflict("LADO_REPOSITORY_UNSUPPORTED", "unsupported", nil),
		"no repository reader": nil,
	} {
		t.Run(name, func(t *testing.T) {
			fx := newServiceFixture(t)
			fx.addProject("demo", true)
			if listErr != nil {
				fx.svc.deps.Repos = &serviceFakeRepos{err: listErr}
			}
			view, err := fx.svc.UpdateSettings(ctx, "demo", unchecked)
			if err != nil {
				t.Fatalf("UpdateSettings: %v", err)
			}
			if view.Settings.Repository != "github:acme/elsewhere" {
				t.Fatalf("saved repository %q", view.Settings.Repository)
			}
		})
	}

	for name, listErr := range map[string]error{
		"internal apierr": apierr.Internal("PROJECT_LOAD_FAILED", "Failed to load project"),
		"plain error":     errors.New("disk I/O error"),
	} {
		t.Run(name, func(t *testing.T) {
			fx := newServiceFixture(t)
			fx.addProject("demo", true)
			fx.svc.deps.Repos = &serviceFakeRepos{err: listErr}
			_, err := fx.svc.UpdateSettings(ctx, "demo", in)
			if ae := serviceAPIError(t, err, codeFailed); ae.Kind != apierr.KindInternal {
				t.Fatalf("kind = %v, want internal", ae.Kind)
			}
			if fx.store.puts != 0 {
				t.Fatal("settings were saved")
			}
		})
	}
}

// TestServiceOccurrencesCarryTheTargetsRepository: an occurrence carries
// its target's repository, or the project's when the target names none; a
// change of a target's repository alone keeps the stream and reaches the
// next occurrence.
func TestServiceOccurrencesCarryTheTargetsRepository(t *testing.T) {
	fx := newServiceFixture(t)
	fx.svc.reconcileEvery = time.Hour // only Kick reconciles
	const prod = "tcp://192.0.2.51:2375"
	fx.addProject("demo", true)
	front := serviceTarget("t-front", "front")
	front.Repository = "github:acme/front"
	settings := serviceSettings("demo", serviceTestSource("prod", prod, front, serviceTarget("t-back", "back")))
	settings.Repository = "github:acme/back"
	fx.store.put(settings)
	f := Instance{ID: strings.Repeat("f", 64), Name: "front-web-1", Service: "front/web", TargetID: "t-front"}
	b := Instance{ID: strings.Repeat("b", 64), Name: "back-api-1", Service: "back/api", TargetID: "t-back"}
	fx.source(prod).setInstances(f, b)
	fx.start(t)
	serviceWait(t, "both streams", func() bool {
		return fx.source(prod).openCount(f.ID) == 1 && fx.source(prod).openCount(b.ID) == 1
	})
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	serviceSend(t, fx.source(prod), f.ID, at, serviceJavaLines)
	serviceSend(t, fx.source(prod), b.ID, at, serviceJavaLines)
	serviceWait(t, "two occurrences", func() bool { return len(fx.sink.occurrences()) == 2 })
	repos := map[string]string{}
	for _, o := range fx.sink.occurrences() {
		repos[o.o.Service] = o.o.Repository
	}
	if want := map[string]string{"front/web": "github:acme/front", "back/api": "github:acme/back"}; !reflect.DeepEqual(repos, want) {
		t.Fatalf("repositories = %v, want %v", repos, want)
	}

	// Only the front target's repository changes: same connection values,
	// so the Follower keeps its streams.
	before := serviceSignature("demo", func() DockerConfig { cfg, _, _ := fx.svc.sourceConfig("demo", settings.Sources[0], false); return cfg }())
	settings.Sources[0].Targets[0].Repository = "github:acme/back"
	after := serviceSignature("demo", func() DockerConfig { cfg, _, _ := fx.svc.sourceConfig("demo", settings.Sources[0], false); return cfg }())
	if before != after {
		t.Fatal("a repository change changed the connection signature")
	}
	fx.store.put(settings)
	fx.svc.Kick()
	serviceWait(t, "the new repository", func() bool {
		for _, run := range fx.svc.runningSources() {
			run.mu.Lock()
			repo := run.repository("t-front")
			run.mu.Unlock()
			if repo == "github:acme/back" {
				return true
			}
		}
		return false
	})
	serviceSend(t, fx.source(prod), f.ID, at.Add(time.Minute), serviceJavaLines)
	serviceWait(t, "the third occurrence", func() bool { return len(fx.sink.occurrences()) == 3 })
	if got := fx.sink.occurrences()[2].o; got.Service != "front/web" || got.Repository != "github:acme/back" {
		t.Fatalf("occurrence after the change = %+v", got)
	}
	if n := fx.madeFor(prod); n != 1 {
		t.Fatalf("the source was made %d times, want 1", n)
	}
	if n := len(fx.source(prod).calls(f.ID)); n != 1 {
		t.Fatalf("the front stream was opened %d times, want 1", n)
	}
}

// TestServiceDrainedOccurrencesKeepTheirTarget: when a settings change
// restarts a source, what its Detectors still hold is given out after the
// Follower stopped and its status lost the instances; the occurrence still
// carries its target's repository and service.
func TestServiceDrainedOccurrencesKeepTheirTarget(t *testing.T) {
	fx := newServiceFixture(t)
	fx.svc.reconcileEvery = time.Hour // only Kick reconciles
	fx.svc.flushEvery = time.Hour     // only the drain gives the incident out
	const prod = "tcp://192.0.2.52:2375"
	fx.addProject("demo", true)
	front := serviceTarget("t-front", "front")
	front.Repository = "github:acme/front"
	settings := serviceSettings("demo", serviceTestSource("prod", prod, front))
	settings.Repository = "github:acme/back"
	fx.store.put(settings)
	f := Instance{ID: strings.Repeat("f", 64), Name: "front-web-1", Image: "front:1", Service: "front/web", TargetID: "t-front"}
	fx.source(prod).setInstances(f)
	fx.start(t)
	serviceWait(t, "the stream", func() bool { return fx.source(prod).openCount(f.ID) == 1 })
	// The stack trace without the lines that close it: the Detector holds
	// the incident.
	serviceSend(t, fx.source(prod), f.ID, time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), serviceJavaLines[:3])
	serviceWait(t, "the Detector", func() bool {
		for _, run := range fx.svc.runningSources() {
			run.mu.Lock()
			n := len(run.detectors)
			run.mu.Unlock()
			if n == 1 {
				return true
			}
		}
		return false
	})
	if n := len(fx.sink.occurrences()); n != 0 {
		t.Fatalf("occurrences before the restart = %d", n)
	}

	// Another target changes the connection values: the source restarts.
	settings.Sources[0].Targets = append(settings.Sources[0].Targets, serviceTarget("t-other", "other"))
	settings.Sources[0].Targets[1].SourceID = "prod"
	fx.store.put(settings)
	fx.svc.Kick()
	serviceWait(t, "the drained occurrence", func() bool { return len(fx.sink.occurrences()) == 1 })
	o := fx.sink.occurrences()[0].o
	if o.Repository != "github:acme/front" || o.Service != "front/web" || o.Instance != "prod/front-web-1" || o.Image != "front:1" {
		t.Fatalf("drained occurrence = %+v", o)
	}
	if n := fx.madeFor(prod); n != 2 {
		t.Fatalf("the source was made %d times, want a restart", n)
	}
}

// TestServiceImportFromCIRepositories: a workspace child's file carries its
// repository's canonical ref; the project root's file and a child without a
// supported origin carry none.
func TestServiceImportFromCIRepositories(t *testing.T) {
	fx := newServiceFixture(t)
	ctx := context.Background()
	workspace := t.TempDir()
	for _, dir := range []string{"", "back", "front", "local", "odd"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, dir, ".gitlab-ci.yml"), []byte(serviceCIExample), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fx.projects.records = []domain.ProjectRecord{{ID: "ws", Path: workspace, Kind: domain.ProjectKindWorkspace}}
	fx.projects.repos["ws"] = []domain.WorkspaceRepoRecord{
		{RelativePath: "back", RepoOriginURL: "https://github.com/Acme/Back.git"},
		{RelativePath: "front", RepoOriginURL: "git@gitlab.example.test:acme/front.git"},
		{RelativePath: "local"},
		{RelativePath: "odd", RepoOriginURL: "not a url"},
	}
	found, err := fx.svc.ImportFromCI(ctx, "ws")
	if err != nil {
		t.Fatalf("ImportFromCI: %v", err)
	}
	got := map[string]string{}
	for _, f := range found.Files {
		got[f.Repo] = f.Repository
	}
	want := map[string]string{"": "", "back": "github:Acme/Back", "front": "gitlab:acme/front@gitlab.example.test", "local": "", "odd": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("file repositories = %v, want %v", got, want)
	}
}
