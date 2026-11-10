package ladologwatch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// followTestLine is a line the fake source sends; end ends the stream with
// err instead.
type followTestLine struct {
	stream string
	ts     time.Time
	text   string
	end    bool
	err    error
}

// followTestStream is one fake instance's log: the lines sent so far (a
// follow with since gets them again, since included, as from an Engine) and
// a feed of new ones.
type followTestStream struct {
	history []followTestLine
	feed    chan followTestLine
}

type followTestCall struct {
	id   string
	from FollowFrom
}

// followTestSource is a fake LogSource.
type followTestSource struct {
	mu          sync.Mutex
	instances   []Instance
	discoverErr error
	discovers   int
	follows     []followTestCall
	streams     map[string]*followTestStream
	gone        map[string]bool
	open        map[string]int
	cancelled   map[string]int
}

func newFollowTestSource(instances ...Instance) *followTestSource {
	return &followTestSource{
		instances: instances,
		streams:   map[string]*followTestStream{},
		gone:      map[string]bool{},
		open:      map[string]int{},
		cancelled: map[string]int{},
	}
}

func (s *followTestSource) stream(id string) *followTestStream {
	st, ok := s.streams[id]
	if !ok {
		st = &followTestStream{feed: make(chan followTestLine)}
		s.streams[id] = st
	}
	return st
}

func (s *followTestSource) Discover(ctx context.Context) ([]Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discovers++
	if s.discoverErr != nil {
		return nil, s.discoverErr
	}
	return append([]Instance(nil), s.instances...), nil
}

func (s *followTestSource) Follow(ctx context.Context, inst Instance, from FollowFrom, onLine func(string, time.Time, string)) error {
	s.mu.Lock()
	s.follows = append(s.follows, followTestCall{id: inst.ID, from: from})
	if s.gone[inst.ID] {
		s.mu.Unlock()
		return &dockerStatusError{Status: http.StatusNotFound}
	}
	st := s.stream(inst.ID)
	history := append([]followTestLine(nil), st.history...)
	s.open[inst.ID]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.open[inst.ID]--
		if ctx.Err() != nil {
			s.cancelled[inst.ID]++
		}
		s.mu.Unlock()
	}()
	if !from.TailZero {
		for _, l := range history {
			if !l.ts.Before(from.Since) {
				onLine(l.stream, l.ts, l.text)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case l := <-st.feed:
			if l.end {
				return l.err
			}
			s.mu.Lock()
			st.history = append(st.history, l)
			s.mu.Unlock()
			onLine(l.stream, l.ts, l.text)
		}
	}
}

// send hands a line to the instance's open follow, waiting for one.
func (s *followTestSource) send(t *testing.T, id string, l followTestLine) {
	t.Helper()
	s.mu.Lock()
	st := s.stream(id)
	s.mu.Unlock()
	select {
	case st.feed <- l:
	case <-time.After(5 * time.Second):
		t.Fatalf("no follow of %s took line %q", id, l.text)
	}
}

// addHistory puts lines in an instance's log as if they were there before.
func (s *followTestSource) addHistory(id string, lines ...followTestLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stream(id)
	st.history = append(st.history, lines...)
}

func (s *followTestSource) setInstances(instances ...Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instances = instances
}

func (s *followTestSource) setDiscoverErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discoverErr = err
}

func (s *followTestSource) markGone(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gone[id] = true
}

func (s *followTestSource) calls(id string) []followTestCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []followTestCall
	for _, c := range s.follows {
		if c.id == id {
			out = append(out, c)
		}
	}
	return out
}

func (s *followTestSource) openCount(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open[id]
}

func (s *followTestSource) discoverCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discovers
}

// followTestRecorder records what a Follower hands on.
type followTestRecorder struct {
	mu       sync.Mutex
	cursors  map[followKey]time.Time
	saved    []followTestSave
	lines    []followTestGot
	statuses []FollowerStatus
}

type followTestSave struct {
	target, name string
	at           time.Time
}

type followTestGot struct {
	inst   string
	stream string
	ts     time.Time
	text   string
}

func newFollowTestRecorder() *followTestRecorder {
	return &followTestRecorder{cursors: map[followKey]time.Time{}}
}

func (r *followTestRecorder) hooks() FollowerHooks {
	return FollowerHooks{
		Cursor: func(target, name string) (time.Time, bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			at, ok := r.cursors[followKey{target: target, name: name}]
			return at, ok
		},
		SaveCursor: func(target, name string, at time.Time) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.cursors[followKey{target: target, name: name}] = at
			r.saved = append(r.saved, followTestSave{target: target, name: name, at: at})
		},
		Line: func(inst Instance, stream string, ts time.Time, text string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.lines = append(r.lines, followTestGot{inst: inst.ID, stream: stream, ts: ts, text: text})
		},
		Status: func(st FollowerStatus) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.statuses = append(r.statuses, st)
		},
	}
}

func (r *followTestRecorder) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.lines))
	for _, l := range r.lines {
		out = append(out, l.text)
	}
	return out
}

func (r *followTestRecorder) savedCursor(target, name string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.cursors[followKey{target: target, name: name}]
	return at, ok
}

func (r *followTestRecorder) lastStatus() FollowerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.statuses) == 0 {
		return FollowerStatus{}
	}
	return r.statuses[len(r.statuses)-1]
}

// newFollowTestFollower makes a Follower with short timings.
func newFollowTestFollower(src LogSource, hooks FollowerHooks) *Follower {
	f := NewFollower(src, hooks)
	f.discoverEvery = 40 * time.Millisecond
	f.discoverGap = 5 * time.Millisecond
	f.cursorEvery = 15 * time.Millisecond
	f.backoffMin = 5 * time.Millisecond
	f.backoffMax = 40 * time.Millisecond
	return f
}

// followTestRun runs f until the test ends and returns a stop that cancels it
// and waits for Run to return.
func followTestRun(t *testing.T, f *Follower) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.Run(ctx)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Follower.Run did not return after cancel")
		}
	}
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return stop
}

func followTestEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func followTestAt(sec int) time.Time {
	return time.Date(2026, 11, 10, 8, 0, sec, 0, time.UTC)
}

func followTestEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestFollowerReconnectsAfterStreamEnd(t *testing.T) {
	api := Instance{ID: dockerTestID("api1"), Name: "grp-api-1", Service: "grp/api", TargetID: "t1", StartedAt: followTestAt(0)}
	src := newFollowTestSource(api)
	// Lines from before the Follower started are not read: no cursor, so
	// the first follow starts at the tail.
	src.addHistory(api.ID, followTestLine{stream: "stdout", ts: followTestAt(1), text: "old"})
	rec := newFollowTestRecorder()
	f := newFollowTestFollower(src, rec.hooks())
	stop := followTestRun(t, f)

	src.send(t, api.ID, followTestLine{stream: "stdout", ts: followTestAt(3), text: "three"})
	// The cursor is saved while the stream is still open.
	followTestEventually(t, "a saved cursor", func() bool {
		at, ok := rec.savedCursor("t1", "grp-api-1")
		return ok && at.Equal(followTestAt(3))
	})
	src.send(t, api.ID, followTestLine{end: true})
	// The follow opens again from the cursor; the Engine sends the line at
	// the cursor again and the Follower skips it.
	src.send(t, api.ID, followTestLine{stream: "stderr", ts: followTestAt(4), text: "four"})
	src.send(t, api.ID, followTestLine{end: true, err: errors.New("connection reset")})
	src.send(t, api.ID, followTestLine{stream: "stdout", ts: followTestAt(5), text: "five"})
	followTestEventually(t, "three lines", func() bool { return len(rec.texts()) >= 3 })
	stop()

	if got := rec.texts(); !followTestEqual(got, []string{"three", "four", "five"}) {
		t.Fatalf("lines = %q, want three, four, five once each", got)
	}
	calls := src.calls(api.ID)
	if len(calls) < 3 {
		t.Fatalf("follows = %+v, want three", calls)
	}
	if !calls[0].from.TailZero {
		t.Errorf("first follow from = %+v, want tail=0", calls[0].from)
	}
	if calls[1].from.TailZero || !calls[1].from.Since.Equal(followTestAt(3)) {
		t.Errorf("second follow from = %+v, want since the cursor", calls[1].from)
	}
	if !calls[2].from.Since.Equal(followTestAt(4)) {
		t.Errorf("third follow from = %+v, want since the new cursor", calls[2].from)
	}
	if at, _ := rec.savedCursor("t1", "grp-api-1"); !at.Equal(followTestAt(5)) {
		t.Errorf("cursor saved at the end = %v, want %v", at, followTestAt(5))
	}
	if st := rec.lastStatus(); st.State != domain.LadoLogSourceStateOff || len(st.Instances) != 0 {
		t.Errorf("status after Run = %+v, want off", st)
	}
}

// followTestClock is a clock the test moves.
type followTestClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *followTestClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *followTestClock) add(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// TestFollowerQuietStreamEndKeepsTheLinesWrittenMeanwhile: an instance that
// was there before the watch and has not logged a line has no cursor. When
// its stream ends, the next one reads from the clock slack before the last
// open, not from the tail again, so the first error of a quiet service
// written while the Follower waited is read.
func TestFollowerQuietStreamEndKeepsTheLinesWrittenMeanwhile(t *testing.T) {
	api := Instance{ID: dockerTestID("api1"), Name: "grp-api-1", Service: "grp/api", TargetID: "t1", StartedAt: followTestAt(0)}
	src := newFollowTestSource(api)
	src.addHistory(api.ID, followTestLine{stream: "stdout", ts: followTestAt(1), text: "before the watch"})
	rec := newFollowTestRecorder()
	f := newFollowTestFollower(src, rec.hooks())
	clock := &followTestClock{at: followTestAt(10)}
	f.now = clock.now
	f.clockSlack = 2 * time.Second
	stop := followTestRun(t, f)

	followTestEventually(t, "the first follow", func() bool { return src.openCount(api.ID) == 1 })
	// Written while the stream was open but not handed on (as when a
	// proxy cut it), then the stream ends with no line.
	src.addHistory(api.ID, followTestLine{stream: "stderr", ts: followTestAt(20), text: "first error"})
	src.send(t, api.ID, followTestLine{end: true})
	followTestEventually(t, "the first error", func() bool { return len(rec.texts()) >= 1 })
	stop()

	if got := rec.texts(); !followTestEqual(got, []string{"first error"}) {
		t.Fatalf("lines = %q, want only the first error", got)
	}
	calls := src.calls(api.ID)
	if len(calls) < 2 || !calls[0].from.TailZero {
		t.Fatalf("follows = %+v, want tail=0 then since", calls)
	}
	if calls[1].from.TailZero || !calls[1].from.Since.Equal(followTestAt(8)) {
		t.Fatalf("second follow from = %+v, want since %v (the open less the slack)", calls[1].from, followTestAt(8))
	}
}

// TestFollowerBackoffStartsOverAfterALongOpenStream: a stream that ends
// quickly with no line waits longer each time, and one that stayed open for
// healthyFor starts the waits over even with no line.
func TestFollowerBackoffStartsOverAfterALongOpenStream(t *testing.T) {
	api := Instance{ID: dockerTestID("api1"), Name: "grp-api-1", Service: "grp/api", TargetID: "t1", StartedAt: followTestAt(0)}
	src := newFollowTestSource(api)
	rec := newFollowTestRecorder()
	f := newFollowTestFollower(src, rec.hooks())
	clock := &followTestClock{at: followTestAt(10)}
	f.now = clock.now
	f.healthyFor = time.Minute
	var mu sync.Mutex
	var waits []time.Duration
	f.onWait = func(d time.Duration) {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
	}
	got := func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), waits...)
	}
	stop := followTestRun(t, f)

	src.send(t, api.ID, followTestLine{end: true})
	src.send(t, api.ID, followTestLine{end: true})
	followTestEventually(t, "the third follow", func() bool { return len(src.calls(api.ID)) == 3 })
	clock.add(time.Minute)
	src.send(t, api.ID, followTestLine{end: true})
	followTestEventually(t, "three waits", func() bool { return len(got()) == 3 })
	stop()

	want := []time.Duration{f.backoffMin, 2 * f.backoffMin, f.backoffMin}
	if w := got(); len(w) != 3 || w[0] != want[0] || w[1] != want[1] || w[2] != want[2] {
		t.Fatalf("waits = %v, want %v", w, want)
	}
	if len(rec.texts()) != 0 {
		t.Fatalf("lines = %q, want none", rec.texts())
	}
}

func TestFollowerSkipsLinesAtOrBeforeTheCursor(t *testing.T) {
	api := Instance{ID: dockerTestID("api1"), Name: "grp-api-1", TargetID: "t1", StartedAt: followTestAt(0)}
	src := newFollowTestSource(api)
	src.addHistory(api.ID,
		followTestLine{stream: "stdout", ts: followTestAt(4), text: "before"},
		followTestLine{stream: "stdout", ts: followTestAt(5), text: "at the cursor"},
		followTestLine{stream: "stdout", ts: followTestAt(6), text: "after"},
	)
	rec := newFollowTestRecorder()
	rec.cursors[followKey{target: "t1", name: "grp-api-1"}] = followTestAt(5)
	// The fake sends lines since the cursor, the cursor's own included, and
	// the Follower must drop that one; send an older one too, as a sloppy
	// Engine might.
	f := newFollowTestFollower(src, rec.hooks())
	stop := followTestRun(t, f)
	src.send(t, api.ID, followTestLine{stream: "stdout", ts: followTestAt(2), text: "late old"})
	src.send(t, api.ID, followTestLine{stream: "stdout", ts: followTestAt(7), text: "new"})
	followTestEventually(t, "the new line", func() bool { return len(rec.texts()) >= 2 })
	stop()
	if got := rec.texts(); !followTestEqual(got, []string{"after", "new"}) {
		t.Fatalf("lines = %q, want after, new", got)
	}
	if calls := src.calls(api.ID); len(calls) == 0 || !calls[0].from.Since.Equal(followTestAt(5)) {
		t.Fatalf("follow from = %+v, want since the saved cursor", calls)
	}
}

func TestFollowerFollowsReplacementsAndNewInstances(t *testing.T) {
	old := Instance{ID: dockerTestID("old"), Name: "grp-api-1", Service: "grp/api", TargetID: "t1", StartedAt: followTestAt(0)}
	worker := Instance{ID: dockerTestID("worker"), Name: "grp-worker-1", Service: "grp/worker", TargetID: "t1", StartedAt: followTestAt(0)}
	src := newFollowTestSource(old, worker)
	rec := newFollowTestRecorder()
	f := newFollowTestFollower(src, rec.hooks())
	f.discoverEvery = time.Hour // only a stream's end brings a Discover on
	stop := followTestRun(t, f)

	src.send(t, old.ID, followTestLine{stream: "stdout", ts: followTestAt(10), text: "old line"})
	followTestEventually(t, "the old line", func() bool { return len(rec.texts()) == 1 })

	// The old container is replaced (same name, new id), the worker scaled
	// in, and a second api replica comes. The old stream ends: that brings a
	// Discover on at once.
	replacement := Instance{ID: dockerTestID("new"), Name: "grp-api-1", Service: "grp/api", TargetID: "t1", StartedAt: followTestAt(20)}
	replica := Instance{ID: dockerTestID("replica"), Name: "grp-api-2", Service: "grp/api", TargetID: "t1", StartedAt: followTestAt(21)}
	src.setInstances(replacement, replica)
	src.markGone(old.ID)
	src.send(t, old.ID, followTestLine{end: true})

	src.send(t, replacement.ID, followTestLine{stream: "stdout", ts: followTestAt(22), text: "replacement line"})
	src.send(t, replica.ID, followTestLine{stream: "stderr", ts: followTestAt(23), text: "replica line"})
	followTestEventually(t, "the worker let go", func() bool { return src.openCount(worker.ID) == 0 })
	followTestEventually(t, "status with the new instances", func() bool {
		st := rec.lastStatus()
		return st.State == domain.LadoLogSourceStateWatching && len(st.Instances) == 2 &&
			st.Instances[0].ID == replacement.ID && st.Instances[1].ID == replica.ID
	})
	stop()

	// The two new instances stream side by side, so their lines come in
	// either order.
	got := rec.texts()
	sort.Strings(got)
	if !followTestEqual(got, []string{"old line", "replacement line", "replica line"}) {
		t.Fatalf("lines = %q", got)
	}
	// The replacement starts at its start time, which is after the name's
	// cursor; the replica, with no cursor, at its start time too.
	if calls := src.calls(replacement.ID); len(calls) == 0 || !calls[0].from.Since.Equal(followTestAt(20)) {
		t.Errorf("replacement follow = %+v, want since its start", calls)
	}
	if calls := src.calls(replica.ID); len(calls) == 0 || !calls[0].from.Since.Equal(followTestAt(21)) {
		t.Errorf("replica follow = %+v, want since its start", calls)
	}
	// The old container is not followed again once gone.
	if calls := src.calls(old.ID); len(calls) > 2 {
		t.Errorf("old container followed %d times after it was gone", len(calls))
	}
	src.mu.Lock()
	cancelled := src.cancelled[worker.ID]
	src.mu.Unlock()
	if cancelled != 1 {
		t.Errorf("worker stream cancelled %d times, want 1", cancelled)
	}
}

func TestFollowerOneSourceDownOthersGoOn(t *testing.T) {
	down := newFollowTestSource(Instance{ID: dockerTestID("down"), Name: "down-1", TargetID: "td"})
	down.setDiscoverErr(errors.New("dial tcp 192.0.2.10:2375: connection refused"))
	up := newFollowTestSource(Instance{ID: dockerTestID("up"), Name: "up-1", TargetID: "tu"})
	downRec, upRec := newFollowTestRecorder(), newFollowTestRecorder()
	downF := newFollowTestFollower(down, downRec.hooks())
	upF := newFollowTestFollower(up, upRec.hooks())
	stopDown := followTestRun(t, downF)
	stopUp := followTestRun(t, upF)

	up.send(t, dockerTestID("up"), followTestLine{stream: "stdout", ts: followTestAt(1), text: "up line"})
	followTestEventually(t, "retries of the down source", func() bool { return down.discoverCount() >= 3 })
	up.send(t, dockerTestID("up"), followTestLine{stream: "stdout", ts: followTestAt(2), text: "still up"})
	followTestEventually(t, "lines of the up source", func() bool { return len(upRec.texts()) == 2 })

	st := downF.Status()
	if st.State != domain.LadoLogSourceStateBackoff || st.DownSince.IsZero() || !strings.Contains(st.Detail, "connection refused") {
		t.Fatalf("down status = %+v, want backoff with down since", st)
	}
	downSince := st.DownSince
	if up := upF.Status(); up.State != domain.LadoLogSourceStateWatching || !up.DownSince.IsZero() || len(up.Instances) != 1 {
		t.Fatalf("up status = %+v", up)
	}

	// The down source comes back: it watches again and the down time clears.
	// An Engine that answers with an error is "error", still down.
	down.setDiscoverErr(&dockerStatusError{Status: http.StatusInternalServerError, Message: "daemon busy"})
	followTestEventually(t, "error state", func() bool { return downF.Status().State == domain.LadoLogSourceStateError })
	if got := downF.Status().DownSince; !got.Equal(downSince) {
		t.Fatalf("down since moved from %v to %v", downSince, got)
	}
	down.setDiscoverErr(nil)
	followTestEventually(t, "the down source watching", func() bool {
		st := downF.Status()
		return st.State == domain.LadoLogSourceStateWatching && st.DownSince.IsZero()
	})
	down.send(t, dockerTestID("down"), followTestLine{stream: "stdout", ts: followTestAt(3), text: "back"})
	followTestEventually(t, "the line of the source that came back", func() bool { return len(downRec.texts()) == 1 })
	stopDown()
	stopUp()
}

func TestBackoffDoublesToMax(t *testing.T) {
	b := NewBackoff(followBackoffFirst, followBackoffLast)
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Fatalf("wait %d = %v, want %v", i, got, w)
		}
	}
	b.Reset()
	if got := b.Next(); got != 5*time.Second {
		t.Fatalf("after Reset = %v, want 5s", got)
	}
}

// TestFollowerCancelClosesTheLogStream runs a Follower on a DockerSource
// against a fake Engine whose log stream stays open, and checks that
// cancelling closes the stream's connection and that the Engine got only
// GETs on the read paths.
func TestFollowerCancelClosesTheLogStream(t *testing.T) {
	id := dockerTestID("api")
	var mu sync.Mutex
	var requests []string
	streamOpen := make(chan struct{}, 1)
	streamClosed := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/containers/json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"Id": id, "Names": []string{"/grp-api-1"}, "Image": "registry.example.test/api:1",
				"Labels": map[string]string{dockerLabelComposeProject: "grp", dockerLabelComposeService: "api"},
			}})
		case "/containers/" + id + "/json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": id, "Name": "/grp-api-1",
				"Config": map[string]any{"Image": "registry.example.test/api:1", "Labels": map[string]string{dockerLabelComposeProject: "grp", dockerLabelComposeService: "api"}},
				"State":  map[string]any{"Running": true, "StartedAt": "2026-11-10T08:00:00Z"},
			})
		case "/containers/" + id + "/logs":
			w.Header().Set("Content-Type", logsContentMultiplexed)
			_, _ = w.Write(logsTestFrame(2, "2026-11-10T08:00:01Z ERROR streaming\n"))
			w.(http.Flusher).Flush()
			streamOpen <- struct{}{}
			<-r.Context().Done()
			streamClosed <- struct{}{}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	src, err := NewDockerSource(DockerConfig{Endpoint: "tcp://" + u.Host, Targets: []DockerTarget{{ID: "t1", Kind: dockerTargetComposeProject, Value: "grp"}}})
	if err != nil {
		t.Fatal(err)
	}
	rec := newFollowTestRecorder()
	f := newFollowTestFollower(src, rec.hooks())
	f.discoverEvery = time.Hour
	stop := followTestRun(t, f)
	select {
	case <-streamOpen:
	case <-time.After(5 * time.Second):
		t.Fatal("the log stream never opened")
	}
	followTestEventually(t, "the streamed line", func() bool { return len(rec.texts()) == 1 })
	stop()
	select {
	case <-streamClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("the log stream's connection stayed open after cancel")
	}
	rec.mu.Lock()
	line := rec.lines[0]
	rec.mu.Unlock()
	if line.stream != "stderr" || line.text != "ERROR streaming" || line.inst != id {
		t.Fatalf("line = %+v", line)
	}
	if at, ok := rec.savedCursor("t1", "grp-api-1"); !ok || !at.Equal(followTestAt(1)) {
		t.Fatalf("cursor saved on cancel = %v, %v", at, ok)
	}
	read := regexp.MustCompile(`^GET (/version|/containers/json|/containers/[0-9a-f]{12,64}/(json|logs))$`)
	mu.Lock()
	defer mu.Unlock()
	for _, r := range requests {
		if !read.MatchString(r) {
			t.Errorf("engine received %s", r)
		}
	}
}
