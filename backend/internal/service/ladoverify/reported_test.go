package ladoverify

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// verifyReported holds the store's reads and refusals of reported PRs.
type verifyReported struct {
	lists     int
	lastLimit int
	refusals  []verifyRefusal
}

type verifyRefusal struct {
	session domain.SessionID
	url     string
	reason  string
	at      time.Time
}

// ListLadoExecutionsWithReportedPRs lists the executions with a reported PR
// LADO still tries to attach, as the store does.
func (s *verifyStore) ListLadoExecutionsWithReportedPRs(_ context.Context, limit int) ([]domain.LadoExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reported.lists++
	s.reported.lastLimit = limit
	var out []domain.LadoExecution
	for _, exec := range s.executions {
		if len(out) == limit {
			break
		}
		if full := s.withTrackedLocked(exec); len(full.PendingReportedPRs()) > 0 {
			out = append(out, full)
		}
	}
	return out, nil
}

// RefuseLadoReportedPR marks the execution's reported PR refused, as the
// store's next read would.
func (s *verifyStore) RefuseLadoReportedPR(_ context.Context, session domain.SessionID, prURL, reason string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(domain.LadoReportedPRRefusalReasons, reason) {
		return errors.New("unknown reason " + reason)
	}
	s.reported.refusals = append(s.reported.refusals, verifyRefusal{session: session, url: prURL, reason: reason, at: at})
	for i := range s.executions {
		if s.executions[i].SessionID != session {
			continue
		}
		for j := range s.executions[i].ReportedPRs {
			if pr := &s.executions[i].ReportedPRs[j]; pr.URL == prURL {
				pr.Refused, pr.Reason = true, reason
			}
		}
	}
	return nil
}

func (s *verifyStore) AppendLadoAuditEvent(_ context.Context, ev domain.LadoAuditEvent) (domain.LadoAuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, ev)
	return ev, nil
}

func (s *verifyStore) refusals() []verifyRefusal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reported.refusals)
}

func (s *verifyStore) reportedLists() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reported.lists
}

// verifyClaims attaches reported PRs: a claim of a URL fails with the next
// of its errors, if any, else succeeds, which takes the PR off the
// execution's reported PRs as the store's next read would.
type verifyClaims struct {
	mu        sync.Mutex
	store     *verifyStore
	errs      map[string][]error
	always    map[string]error
	calls     []string
	deadlines []time.Duration
}

func (c *verifyClaims) ClaimReportedPR(ctx context.Context, session domain.SessionID, prURL string) error {
	c.mu.Lock()
	c.calls = append(c.calls, string(session)+" "+prURL)
	if deadline, ok := ctx.Deadline(); ok {
		c.deadlines = append(c.deadlines, time.Until(deadline))
	} else {
		c.deadlines = append(c.deadlines, -1)
	}
	var err error
	if queued := c.errs[prURL]; len(queued) > 0 {
		err, c.errs[prURL] = queued[0], queued[1:]
	} else {
		err = c.always[prURL]
	}
	c.mu.Unlock()
	if err == nil {
		c.store.update(session, func(e *domain.LadoExecution) {
			e.ReportedPRs = slices.DeleteFunc(e.ReportedPRs, func(pr domain.LadoReportedPR) bool { return pr.URL == prURL })
		})
	}
	return err
}

func (c *verifyClaims) made() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

// newReportedFixture is a verification fixture whose loop attaches
// reported PRs through claims.
func newReportedFixture(t *testing.T) (*verifyFixture, *verifyClaims) {
	t.Helper()
	claims := &verifyClaims{errs: map[string][]error{}, always: map[string]error{}}
	f := newVerifyFixture(t, func(d *Deps) { d.Claims = claims })
	claims.store = f.store
	return f, claims
}

// addReported adds an execution whose worker reported the PRs at urls,
// which its session does not have.
func (f *verifyFixture) addReported(key string, urls ...string) domain.LadoExecution {
	f.t.Helper()
	exec := f.addExecution(key, verifyHead1)
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) {
		for _, url := range urls {
			e.ReportedPRs = append(e.ReportedPRs, domain.LadoReportedPR{URL: url})
		}
	})
	return f.execution(exec.SessionID)
}

// A reported PR is claimed for its execution's session within the claim
// timeout, the claim is audited and the loop asks for another pass; once
// attached it is not claimed again.
func TestReportedPRIsClaimedAndAudited(t *testing.T) {
	f, claims := newReportedFixture(t)
	url := "https://github.com/acme/demo/pull/90"
	exec := f.addReported("1", url)

	f.tick()
	if got := claims.made(); !slices.Equal(got, []string{string(exec.SessionID) + " " + url}) {
		t.Fatalf("claims = %v, want the reported PR for its session", got)
	}
	if d := claims.deadlines[0]; d <= 0 || d > reportedClaimTimeout {
		t.Errorf("claim deadline in %v, want within %v", d, reportedClaimTimeout)
	}
	if f.store.reported.lastLimit != trackBatch {
		t.Errorf("list limit = %d, want %d", f.store.reported.lastLimit, trackBatch)
	}
	claimed := slices.DeleteFunc(f.store.auditEvents(), func(e domain.LadoAuditEvent) bool { return e.Action != ladoAuditReportedPRClaimed })
	if len(claimed) != 1 || claimed[0].SubjectID != string(exec.SessionID) || !strings.Contains(string(claimed[0].Payload), url) {
		t.Errorf("claim audit events = %+v, want one naming the PR", claimed)
	}
	select {
	case <-f.svc.kick:
	default:
		t.Error("no pass was kicked after the claim")
	}

	f.tick()
	if got := claims.made(); len(got) != 1 {
		t.Errorf("claims after a second pass = %v, want none more", got)
	}
}

// One pass makes at most reportedClaimsPerPass claims; the next pass takes
// the rest.
func TestReportedPRClaimsAreCappedPerPass(t *testing.T) {
	f, claims := newReportedFixture(t)
	var urls []string
	for i := range 3 {
		key := strconv.Itoa(i + 1)
		one, two := "https://github.com/acme/demo/pull/"+key+"0", "https://github.com/acme/demo/pull/"+key+"1"
		f.addReported(key, one, two)
		urls = append(urls, one, two)
	}

	f.tick()
	if got := claims.made(); len(got) != reportedClaimsPerPass {
		t.Fatalf("claims of the first pass = %v, want %d", got, reportedClaimsPerPass)
	}
	f.tick()
	got := claims.made()
	if len(got) != len(urls) {
		t.Fatalf("claims after two passes = %v, want each of %d once", got, len(urls))
	}
	for i, call := range got {
		if !strings.HasSuffix(call, " "+urls[i]) {
			t.Errorf("claim %d = %q, want %s in report order", i, call, urls[i])
		}
	}
}

// Without Claims the loop does not read reported PRs.
func TestReportedPRsWithoutClaimsAreLeftAlone(t *testing.T) {
	f := newVerifyFixture(t)
	f.addReported("1", "https://github.com/acme/demo/pull/90")
	f.tick()
	if n := f.store.reportedLists(); n != 0 {
		t.Errorf("reported PR reads = %d, want none", n)
	}
}

// A refused reported PR, such as one another session has, and the reported
// PRs of an ended session are never claimed.
func TestReportedPRRefusedOrOfAnEndedSessionIsNotClaimed(t *testing.T) {
	f, claims := newReportedFixture(t)
	owned := f.addExecution("1", verifyHead1)
	f.store.update(owned.SessionID, func(e *domain.LadoExecution) {
		e.ReportedPRs = []domain.LadoReportedPR{{URL: "https://github.com/acme/demo/pull/90", Refused: true, Reason: domain.LadoReportedPROwnedElsewhere}}
	})
	ended := f.addReported("2", "https://github.com/acme/demo/pull/91")
	f.store.update(ended.SessionID, func(e *domain.LadoExecution) { e.SessionTerminated = true })

	f.tick()
	if got := claims.made(); len(got) != 0 {
		t.Errorf("claims = %v, want none", got)
	}
}

// A claim that can never pass is stored as refused with its reason and not
// tried again.
func TestReportedPRRefusalIsStored(t *testing.T) {
	f, claims := newReportedFixture(t)
	url := "https://github.com/acme/demo/pull/90"
	exec := f.addReported("1", url)
	claims.always[url] = &ReportedPRRefusal{Reason: domain.LadoReportedPRNotOpen, Err: errors.New("closed")}

	f.tick()
	refusals := f.store.refusals()
	if len(refusals) != 1 || refusals[0].session != exec.SessionID || refusals[0].url != url ||
		refusals[0].reason != domain.LadoReportedPRNotOpen || !refusals[0].at.Equal(f.clock.Now().UTC()) {
		t.Fatalf("refusals = %+v, want not_open for the PR now", refusals)
	}
	f.clock.advance(failedRetryAfter)
	f.tick()
	if got := claims.made(); len(got) != 1 {
		t.Errorf("claims = %v, want the one", got)
	}
	if got := f.execution(exec.SessionID).ReportedPRs; len(got) != 1 || !got[0].Refused {
		t.Errorf("reported PRs = %+v, want it refused", got)
	}
}

// A claim that fails for now rests failedRetryAfter and is tried again; the
// reportedClaimMaxFailures-th failure in a row refuses it as unreachable. A
// refusal with a reason LADO does not store counts as a failure for now.
func TestReportedPRFailingForNowIsRetriedThenUnreachable(t *testing.T) {
	f, claims := newReportedFixture(t)
	url := "https://github.com/acme/demo/pull/90"
	f.addReported("1", url)
	claims.errs[url] = []error{&ReportedPRRefusal{Reason: "made_up"}}
	claims.always[url] = errors.New("the host is down")

	for attempt := 1; attempt <= reportedClaimMaxFailures; attempt++ {
		f.tick()
		if got := claims.made(); len(got) != attempt {
			t.Fatalf("claims after attempt %d = %d, want %d", attempt, len(got), attempt)
		}
		if refusals := f.store.refusals(); attempt < reportedClaimMaxFailures && len(refusals) != 0 {
			t.Fatalf("refusals after %d failures = %+v, want none yet", attempt, refusals)
		}
		// It rests until failedRetryAfter passed.
		f.clock.advance(failedRetryAfter - time.Second)
		f.tick()
		if got := claims.made(); len(got) != attempt {
			t.Fatalf("claims while resting = %d, want %d", len(got), attempt)
		}
		f.clock.advance(time.Second)
	}
	refusals := f.store.refusals()
	if len(refusals) != 1 || refusals[0].reason != domain.LadoReportedPRUnreachable {
		t.Fatalf("refusals = %+v, want unreachable", refusals)
	}
	f.tick()
	if got := claims.made(); len(got) != reportedClaimMaxFailures {
		t.Errorf("claims after the refusal = %d, want %d", len(got), reportedClaimMaxFailures)
	}
}

// A claim that succeeds after failures starts its count again.
func TestReportedPRFailuresResetOnSuccess(t *testing.T) {
	f, claims := newReportedFixture(t)
	url := "https://github.com/acme/demo/pull/90"
	exec := f.addReported("1", url)
	claims.errs[url] = []error{errors.New("down"), errors.New("down")}
	for range 3 {
		f.tick()
		f.clock.advance(failedRetryAfter)
	}
	if got := claims.made(); len(got) != 3 || len(f.store.refusals()) != 0 {
		t.Fatalf("claims = %v, refusals = %+v; want three claims and no refusal", got, f.store.refusals())
	}
	if got := f.execution(exec.SessionID).ReportedPRs; len(got) != 0 {
		t.Errorf("reported PRs = %+v, want it attached", got)
	}
	f.svc.mu.Lock()
	left := len(f.svc.reportedFailed)
	f.svc.mu.Unlock()
	if left != 0 {
		t.Errorf("failure counts left = %d, want none", left)
	}
}
