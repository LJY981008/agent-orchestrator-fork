package ladologwatch

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A Follower watches one source (ADR 0012). It finds the source's instances
// with Discover every followDiscoverEvery, and sooner when a stream ends,
// and keeps one Follow open per container id. A container that a restart, a
// replacement or a scale-out brings is followed from its start; one that is
// gone is let go without a word. When Discover fails the Follower waits with
// a Backoff (5s doubling to 5m), reports the source down since the first
// failure, and on the next success reads on from each instance's cursor, so
// the lines of the time it was down are not lost. A stream that ends is
// opened again from the cursor after its own backoff.
//
// The cursor of an instance is the timestamp, on the source's clock, of the
// last line handed on, kept per (target id, container name). Lines at or
// before the cursor are skipped (the Engine's since includes its own
// second), and the cursor is saved every followCursorEvery and when a stream
// ends. An instance with no cursor that was there when the Follower started
// is read from its tail (tail=0), and when that stream ends before a line
// came, from a minute before it opened; one that came while it watched is
// read from its start time, or a newer cursor. A stream's backoff starts
// over when it gave lines or stayed open for followHealthyFor.
//
// Each source has its own Follower, so one that cannot connect does not hold
// up the others. The hooks are called one at a time.

const (
	// followDiscoverEvery is how often a Follower looks for instances.
	followDiscoverEvery = 60 * time.Second
	// followCursorEvery is how often a Follower saves the cursors that moved.
	followCursorEvery = 10 * time.Second
	// followBackoffFirst and followBackoffLast are the first and the longest
	// wait before connecting again.
	followBackoffFirst = 5 * time.Second
	followBackoffLast  = 5 * time.Minute
	// followDiscoverGap is the least time between two Discovers that ended
	// streams ask for.
	followDiscoverGap = 2 * time.Second
	// followHealthyFor is how long a stream must have stayed open for its
	// end to start the backoff over: a quiet container's stream that a
	// proxy's idle timeout or a dockerd restart cuts now and then is not a
	// failing one.
	followHealthyFor = 30 * time.Second
	// followClockSlack is how far before the previous open a stream with no
	// cursor yet is opened again, for a source clock that runs ahead of
	// LADO's (the Engine reads since on its own clock).
	followClockSlack = time.Minute
)

// FollowerHooks are how a Follower reads and saves cursors and hands on
// lines and its status. Any hook may be nil.
type FollowerHooks struct {
	// Cursor gives the saved cursor of an instance (target id, container
	// name).
	Cursor func(targetID, instance string) (time.Time, bool)
	// SaveCursor saves the cursor of an instance.
	SaveCursor func(targetID, instance string, at time.Time)
	// Line hands on one log line of an instance.
	Line func(inst Instance, stream string, ts time.Time, text string)
	// Status reports the Follower's status when it changes.
	Status func(FollowerStatus)
}

// FollowerStatus is how a Follower is doing. State is one of the
// domain.LadoLogSourceState values; DownSince is when the source stopped
// answering, zero while it answers; Instances are the instances followed.
type FollowerStatus struct {
	State     string
	Detail    string
	DownSince time.Time
	Instances []Instance
}

// Backoff is a wait that doubles from Min to Max on each Next and starts
// over after Reset.
type Backoff struct {
	Min, Max time.Duration
	next     time.Duration
}

// NewBackoff makes a Backoff from lo to hi.
func NewBackoff(lo, hi time.Duration) *Backoff {
	return &Backoff{Min: lo, Max: hi}
}

// Next is the wait to take now; the one after it is twice as long, up to
// Max.
func (b *Backoff) Next() time.Duration {
	if b.next <= 0 {
		b.next = b.Min
	}
	d := b.next
	b.next = min(b.next*2, b.Max)
	if d > b.Max {
		d = b.Max
	}
	return d
}

// Reset starts the waits over from Min.
func (b *Backoff) Reset() { b.next = 0 }

// Follower follows the instances of one source.
type Follower struct {
	src   LogSource
	hooks FollowerHooks

	// The timings; tests make them short.
	discoverEvery time.Duration
	discoverGap   time.Duration
	cursorEvery   time.Duration
	backoffMin    time.Duration
	backoffMax    time.Duration
	healthyFor    time.Duration
	clockSlack    time.Duration
	now           func() time.Time
	// onWait, when set, is told each wait before a stream opens again.
	onWait func(time.Duration)

	// hookMu makes the hooks one at a time.
	hookMu sync.Mutex

	mu      sync.Mutex
	cursors map[followKey]*followCursor
	status  FollowerStatus
}

// followKey is the key of an instance's cursor.
type followKey struct {
	target string
	name   string
}

// followCursor is an instance's cursor in memory.
type followCursor struct {
	at    time.Time
	dirty bool
}

// followStream is one instance's open follow loop.
type followStream struct {
	inst   Instance
	cancel context.CancelFunc
	done   chan struct{}
}

// NewFollower makes a Follower of src; Run starts it.
func NewFollower(src LogSource, hooks FollowerHooks) *Follower {
	return &Follower{
		src:           src,
		hooks:         hooks,
		discoverEvery: followDiscoverEvery,
		discoverGap:   followDiscoverGap,
		cursorEvery:   followCursorEvery,
		backoffMin:    followBackoffFirst,
		backoffMax:    followBackoffLast,
		healthyFor:    followHealthyFor,
		clockSlack:    followClockSlack,
		now:           time.Now,
		cursors:       map[followKey]*followCursor{},
		status:        FollowerStatus{State: domain.LadoLogSourceStateOff},
	}
}

// Run follows the source until ctx ends. It returns after every stream it
// opened is closed and the cursors are saved; the status is then off.
func (f *Follower) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	streams := map[string]*followStream{}
	ended := make(chan string, 64)
	var wg sync.WaitGroup
	stopAll := func() {
		cancel()
		wg.Wait()
		f.saveCursors()
		f.setStatus(func(st *FollowerStatus) {
			st.State = domain.LadoLogSourceStateOff
			st.Detail = ""
			st.Instances = nil
		})
	}
	defer stopAll()

	cursorTick := time.NewTicker(f.cursorEvery)
	defer cursorTick.Stop()

	backoff := NewBackoff(f.backoffMin, f.backoffMax)
	first := true
	var lastDiscover time.Time
	f.setStatus(func(st *FollowerStatus) { st.State = domain.LadoLogSourceStateConnecting; st.Detail = "" })
	for {
		lastDiscover = time.Now()
		instances, err := f.src.Discover(ctx)
		if ctx.Err() != nil {
			return
		}
		var wait time.Duration
		if err != nil {
			wait = backoff.Next()
			f.setStatus(func(st *FollowerStatus) {
				st.State = domain.LadoLogSourceStateBackoff
				var se *dockerStatusError
				if errors.As(err, &se) {
					st.State = domain.LadoLogSourceStateError
				}
				st.Detail = dockerClip(err.Error(), 300)
				if st.DownSince.IsZero() {
					st.DownSince = f.now()
				}
			})
		} else {
			backoff.Reset()
			wait = f.discoverEvery
			f.reconcile(ctx, streams, instances, first, ended, &wg)
			first = false
			attached := f.attached(streams)
			f.setStatus(func(st *FollowerStatus) {
				st.State = domain.LadoLogSourceStateWatching
				st.Detail = ""
				st.DownSince = time.Time{}
				st.Instances = attached
			})
		}
		timer := time.NewTimer(wait)
	waiting:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-cursorTick.C:
				f.saveCursors()
			case id := <-ended:
				if s, ok := streams[id]; ok {
					select {
					case <-s.done:
						delete(streams, id)
						attached := f.attached(streams)
						f.setStatus(func(st *FollowerStatus) { st.Instances = attached })
					default:
					}
				}
				if err != nil {
					// Discover is backing off; the stream's end does not
					// hurry it.
					continue
				}
				// A stream ended: look for its replacement soon.
				gap := f.discoverGap - time.Since(lastDiscover)
				if gap <= 0 {
					timer.Stop()
					break waiting
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(gap)
			case <-timer.C:
				break waiting
			}
		}
	}
}

// reconcile starts a follow loop for each new instance and stops the loops
// of instances that are gone.
func (f *Follower) reconcile(ctx context.Context, streams map[string]*followStream, instances []Instance, first bool, ended chan<- string, wg *sync.WaitGroup) {
	seen := map[string]bool{}
	for _, inst := range instances {
		if inst.ID == "" || seen[inst.ID] {
			continue
		}
		seen[inst.ID] = true
		if s, ok := streams[inst.ID]; ok {
			select {
			case <-s.done:
				// Ended on its own; open it again below.
			default:
				s.inst = inst
				continue
			}
		}
		sctx, cancel := context.WithCancel(ctx)
		s := &followStream{inst: inst, cancel: cancel, done: make(chan struct{})}
		streams[inst.ID] = s
		notify := func() {
			// One pending note is enough to bring the next Discover on.
			select {
			case ended <- inst.ID:
			default:
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.followLoop(sctx, inst, first, notify)
			// The loop ends by itself only when the container is gone.
			gone := sctx.Err() == nil
			cancel()
			close(s.done)
			if gone {
				notify()
			}
		}()
	}
	for id, s := range streams {
		if !seen[id] {
			s.cancel()
			delete(streams, id)
		}
	}
}

// followLoop follows one instance until ctx ends or the instance is gone.
// existing is true for an instance that was there when the Follower started.
// ended is called each time a stream ends while the instance may still be
// there, so that the Follower looks for a replacement.
func (f *Follower) followLoop(ctx context.Context, inst Instance, existing bool, ended func()) {
	key := followKey{target: inst.TargetID, name: inst.Name}
	backoff := NewBackoff(f.backoffMin, f.backoffMax)
	from, skip := f.followFrom(key, inst, existing)
	for {
		lines := 0
		opened := f.now()
		err := f.src.Follow(ctx, inst, from, func(stream string, ts time.Time, text string) {
			if !skip.IsZero() && !ts.IsZero() && !ts.After(skip) {
				return
			}
			lines++
			if !ts.IsZero() {
				f.moveCursor(key, ts)
			}
			if f.hooks.Line != nil {
				f.hookMu.Lock()
				f.hooks.Line(inst, stream, ts, text)
				f.hookMu.Unlock()
			}
		})
		f.saveCursor(key)
		if ctx.Err() != nil || dockerNotFound(err) {
			return
		}
		ended()
		// A follow that ended reads on from the cursor after a wait, which
		// starts over when the stream gave lines or stayed open a while.
		// Without a cursor yet, one that started at its tail reads on from
		// when it opened (less the clock slack), so the lines written while
		// it waits are not lost; one that started at its start time starts
		// there again.
		if cursor, ok := f.cursor(key); ok {
			from, skip = FollowFrom{Since: cursor}, cursor
		} else if from.TailZero {
			from = FollowFrom{Since: opened.Add(-f.clockSlack)}
		}
		if lines > 0 || f.now().Sub(opened) >= f.healthyFor {
			backoff.Reset()
		}
		wait := backoff.Next()
		if f.onWait != nil {
			f.onWait(wait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// followFrom is where an instance's follow starts and the time at or before
// which its lines are skipped.
func (f *Follower) followFrom(key followKey, inst Instance, existing bool) (FollowFrom, time.Time) {
	cursor, ok := f.cursor(key)
	switch {
	case ok && !existing && inst.StartedAt.After(cursor):
		return FollowFrom{Since: inst.StartedAt}, cursor
	case ok:
		return FollowFrom{Since: cursor}, cursor
	case !existing && !inst.StartedAt.IsZero():
		return FollowFrom{Since: inst.StartedAt}, time.Time{}
	}
	return FollowFrom{TailZero: true}, time.Time{}
}

// cursor is an instance's cursor: the one in memory, else the saved one.
func (f *Follower) cursor(key followKey) (time.Time, bool) {
	f.mu.Lock()
	var at time.Time
	c, ok := f.cursors[key]
	if ok {
		at = c.at
	}
	f.mu.Unlock()
	if ok {
		return at, true
	}
	if f.hooks.Cursor == nil {
		return time.Time{}, false
	}
	f.hookMu.Lock()
	at, ok = f.hooks.Cursor(key.target, key.name)
	f.hookMu.Unlock()
	if !ok || at.IsZero() {
		return time.Time{}, false
	}
	f.mu.Lock()
	if _, have := f.cursors[key]; !have {
		f.cursors[key] = &followCursor{at: at}
	}
	f.mu.Unlock()
	return at, true
}

// moveCursor moves an instance's cursor forward to ts.
func (f *Follower) moveCursor(key followKey, ts time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cursors[key]
	if !ok {
		f.cursors[key] = &followCursor{at: ts, dirty: true}
		return
	}
	if ts.After(c.at) {
		c.at = ts
		c.dirty = true
	}
}

// saveCursor saves one instance's cursor if it moved.
func (f *Follower) saveCursor(key followKey) {
	f.mu.Lock()
	c, ok := f.cursors[key]
	if !ok || !c.dirty {
		f.mu.Unlock()
		return
	}
	at := c.at
	c.dirty = false
	f.mu.Unlock()
	f.callSave(key, at)
}

// saveCursors saves every cursor that moved.
func (f *Follower) saveCursors() {
	type save struct {
		key followKey
		at  time.Time
	}
	f.mu.Lock()
	var saves []save
	for key, c := range f.cursors {
		if c.dirty {
			c.dirty = false
			saves = append(saves, save{key: key, at: c.at})
		}
	}
	f.mu.Unlock()
	sort.Slice(saves, func(i, j int) bool {
		if saves[i].key.target != saves[j].key.target {
			return saves[i].key.target < saves[j].key.target
		}
		return saves[i].key.name < saves[j].key.name
	})
	for _, s := range saves {
		f.callSave(s.key, s.at)
	}
}

func (f *Follower) callSave(key followKey, at time.Time) {
	if f.hooks.SaveCursor == nil {
		return
	}
	f.hookMu.Lock()
	f.hooks.SaveCursor(key.target, key.name, at)
	f.hookMu.Unlock()
}

// attached is the instances followed, in a steady order.
func (f *Follower) attached(streams map[string]*followStream) []Instance {
	out := make([]Instance, 0, len(streams))
	for _, s := range streams {
		out = append(out, s.inst)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// setStatus changes the status and reports it when it changed.
func (f *Follower) setStatus(change func(*FollowerStatus)) {
	f.mu.Lock()
	before := f.status
	next := before
	next.Instances = append([]Instance(nil), before.Instances...)
	change(&next)
	f.status = next
	same := followSameStatus(before, next)
	f.mu.Unlock()
	if same || f.hooks.Status == nil {
		return
	}
	report := next
	report.Instances = append([]Instance(nil), next.Instances...)
	f.hookMu.Lock()
	f.hooks.Status(report)
	f.hookMu.Unlock()
}

// Status is the Follower's status now.
func (f *Follower) Status() FollowerStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status
	st.Instances = append([]Instance(nil), f.status.Instances...)
	return st
}

func followSameStatus(a, b FollowerStatus) bool {
	if a.State != b.State || a.Detail != b.Detail || !a.DownSince.Equal(b.DownSince) || len(a.Instances) != len(b.Instances) {
		return false
	}
	for i := range a.Instances {
		x, y := a.Instances[i], b.Instances[i]
		if x.ID != y.ID || x.Name != y.Name || x.Image != y.Image || x.Service != y.Service || x.TargetID != y.TargetID {
			return false
		}
	}
	return true
}
