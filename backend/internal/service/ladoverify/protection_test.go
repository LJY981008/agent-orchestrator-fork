package ladoverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var protectionRepo = domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "acme/demo"}

// protectionRules builds branch rules: one rule per type, and a
// required_status_checks rule naming checks when any are given.
func protectionRules(types []string, checks ...string) ports.LadoBranchRules {
	var rules ports.LadoBranchRules
	for _, typ := range types {
		rule := ports.LadoBranchRule{Type: typ}
		if typ == ports.LadoBranchRuleRequiredStatusChecks {
			for _, check := range checks {
				rule.RequiredChecks = append(rule.RequiredChecks, ports.LadoRequiredCheck{Context: check})
			}
		}
		rules.Rules = append(rules.Rules, rule)
	}
	return rules
}

var protectionAllTypes = []string{
	ports.LadoBranchRulePullRequest, ports.LadoBranchRuleRequiredStatusChecks,
	ports.LadoBranchRuleNonFastForward, ports.LadoBranchRuleDeletion,
}

func protectionDetails(t *testing.T, item domain.LadoVerifyItem) domain.LadoProtectionDetails {
	t.Helper()
	if item.Kind != domain.LadoVerifyProtection {
		t.Fatalf("item kind = %s, want protection", item.Kind)
	}
	var details domain.LadoProtectionDetails
	if err := json.Unmarshal(item.Details, &details); err != nil {
		t.Fatalf("details %s: %v", item.Details, err)
	}
	return details
}

func TestProtectionItem(t *testing.T) {
	read := func(rules ports.LadoBranchRules) ProtectionRead {
		return ProtectionRead{Provider: domain.TrackerProviderGitHub, Repo: protectionRepo, Branch: "main", Rules: rules, ReadAt: time.Now()}
	}

	t.Run("protected", func(t *testing.T) {
		// Contexts compare without case, as GitHub compares them.
		item, err := EvaluateProtection(read(protectionRules(protectionAllTypes, "build", "LADO/Integration")))
		if err != nil {
			t.Fatal(err)
		}
		if item.Outcome != domain.LadoVerifyPass || item.Code != "" {
			t.Fatalf("item = %s %q, want a plain pass", item.Outcome, item.Code)
		}
		details := protectionDetails(t, item)
		want := domain.LadoProtectionDetails{Provider: "github", Repo: "acme/demo", Branch: "main", Missing: []string{}, RequiredChecks: []string{"build"}}
		if !protectionDetailsEqual(details, want) {
			t.Fatalf("details = %+v, want %+v", details, want)
		}
	})

	t.Run("each missing part needs confirming", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			rules  ports.LadoBranchRules
			want   []string
			checks []string
		}{
			{"no pull request", protectionRules(protectionAllTypes[1:], "lado/integration"), []string{"pull_request"}, nil},
			{"no status check rule", protectionRules([]string{ports.LadoBranchRulePullRequest, ports.LadoBranchRuleNonFastForward, ports.LadoBranchRuleDeletion}),
				[]string{"required_status_checks:lado/integration"}, nil},
			{"checks without lado/integration", protectionRules(protectionAllTypes, "build"), []string{"required_status_checks:lado/integration"}, []string{"build"}},
			{"force pushes allowed", protectionRules([]string{ports.LadoBranchRulePullRequest, ports.LadoBranchRuleRequiredStatusChecks, ports.LadoBranchRuleDeletion}, "lado/integration"),
				[]string{"non_fast_forward"}, nil},
			{"deletion allowed", protectionRules(protectionAllTypes[:3], "lado/integration"), []string{"deletion"}, nil},
			{"no rules", ports.LadoBranchRules{}, []string{"pull_request", "required_status_checks:lado/integration", "non_fast_forward", "deletion"}, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				item, err := EvaluateProtection(read(tc.rules))
				if err != nil {
					t.Fatal(err)
				}
				if item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeUnprotected {
					t.Fatalf("item = %s %q, want confirm unprotected", item.Outcome, item.Code)
				}
				details := protectionDetails(t, item)
				if !slices.Equal(details.Missing, tc.want) {
					t.Fatalf("missing = %q, want %q", details.Missing, tc.want)
				}
				if !slices.Equal(details.RequiredChecks, protectionOrEmpty(tc.checks)) || details.ErrorClass != "" {
					t.Fatalf("details = %+v, want required checks %q and no error", details, tc.checks)
				}
				if got := MissingProtection(tc.rules); !slices.Equal(got, tc.want) {
					t.Fatalf("MissingProtection = %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("unreadable rules", func(t *testing.T) {
		const token = "ghp_LadoSecretToken0123456789abcdefghijk"
		r := read(protectionRules(protectionAllTypes, "build", "lado/integration"))
		r.Err = fmt.Errorf("github tracker: read the rules of main:\n%w", &ports.LadoHTTPError{Status: 403, Message: "Bad credentials for " + token + "\nAuthorization: Bearer tkn-plain"})
		item, err := EvaluateProtection(r)
		if err != nil {
			t.Fatal(err)
		}
		if item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeProtectionUnknown {
			t.Fatalf("item = %s %q, want confirm protection_unknown", item.Outcome, item.Code)
		}
		details := protectionDetails(t, item)
		if details.ErrorClass != "http_403" {
			t.Fatalf("error class = %q, want http_403", details.ErrorClass)
		}
		message := r.ErrorMessage()
		if strings.ContainsAny(message, "\r\n") || strings.Contains(message, token) || strings.Contains(message, "tkn-plain") {
			t.Fatalf("error = %q, want one line without the token", message)
		}
		if !strings.Contains(message, "the host answered 403: Bad credentials for [redacted]") {
			t.Fatalf("error = %q, want the host's answer", message)
		}
		// The last good read still names the required checks.
		if !slices.Equal(details.RequiredChecks, []string{"build"}) || len(details.Missing) != 0 {
			t.Fatalf("details = %+v, want the last good required checks and nothing missing", details)
		}
		long := r
		long.Err = errors.New(strings.Repeat("x", 2000))
		if got := long.ErrorMessage(); len([]rune(got)) > redactedErrorMax+1 {
			t.Fatalf("error has %d characters, want at most %d", len([]rune(got)), redactedErrorMax+1)
		}
	})

	// V8 of #15: a host's message can change from one request to the next
	// (a request id, a local port). The item, and so the record's hash that
	// a dry run shows and the approval signs, holds only the error's class.
	t.Run("the hash holds the error's class, not its message", func(t *testing.T) {
		first, second := read(ports.LadoBranchRules{}), read(ports.LadoBranchRules{})
		first.Err = fmt.Errorf("github tracker: %w", &ports.LadoHTTPError{Status: 502, Message: "Server Error (request 1A2B:3C4D)"})
		second.Err = fmt.Errorf("github tracker: %w", &ports.LadoHTTPError{Status: 502, Message: "Server Error (request 9F8E:7D6C)"})
		a, err := EvaluateProtection(first)
		if err != nil {
			t.Fatal(err)
		}
		b, err := EvaluateProtection(second)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("items of two reads that failed alike differ:\n%s\n%s", a.Details, b.Details)
		}
		if first.ErrorMessage() == second.ErrorMessage() {
			t.Fatal("the messages kept beside the record are the same")
		}
	})

	t.Run("error classes", func(t *testing.T) {
		for _, tc := range []struct {
			err  error
			want string
		}{
			{&ports.LadoHTTPError{Status: 404, Message: "Not Found"}, "http_404"},
			{fmt.Errorf("wrapped: %w", &ports.LadoHTTPError{Status: 401}), "http_401"},
			{&ports.LadoHTTPError{Status: 429}, "rate_limited"},
			{&ports.LadoHTTPError{Status: 403, Message: "API rate limit exceeded for user ID 1."}, "rate_limited"},
			{&ports.LadoHTTPError{Status: 403, Message: "Resource not accessible by integration"}, "http_403"},
			{fmt.Errorf("get: %w", context.DeadlineExceeded), "transport"},
			{&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}, "transport"},
			{errors.New("no token for github.com"), "other"},
		} {
			if got := ProtectionErrorClass(tc.err); got != tc.want {
				t.Errorf("ProtectionErrorClass(%v) = %q, want %q", tc.err, got, tc.want)
			}
		}
	})

	t.Run("GitLab", func(t *testing.T) {
		item, err := EvaluateProtection(ProtectionRead{
			Provider: domain.TrackerProviderGitLab, Repo: domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/project"},
			Branch: "main", Err: ports.ErrLadoUnsupported,
		})
		if err != nil {
			t.Fatal(err)
		}
		if item.Outcome != domain.LadoVerifyConfirm || item.Code != domain.LadoCodeUnprotectedGitLab {
			t.Fatalf("item = %s %q, want confirm unprotected_gitlab", item.Outcome, item.Code)
		}
		details := protectionDetails(t, item)
		if details.Provider != "gitlab" || details.Repo != "group/project" || details.ErrorClass != "" || len(details.Missing) != 0 {
			t.Fatalf("details = %+v", details)
		}
	})

	t.Run("required contexts", func(t *testing.T) {
		rules := protectionRules(protectionAllTypes, "build", "LADO/Integration", "", "Build", "lint")
		rules.Rules = append(rules.Rules, ports.LadoBranchRule{Type: ports.LadoBranchRuleRequiredStatusChecks, RequiredChecks: []ports.LadoRequiredCheck{
			{Context: "LINT"}, {Context: "e2e"}, {Context: "lado/integration"},
		}})
		// A rule of another type never names checks.
		rules.Rules = append(rules.Rules, ports.LadoBranchRule{Type: ports.LadoBranchRulePullRequest, RequiredChecks: []ports.LadoRequiredCheck{{Context: "stray"}}})
		contexts, named := RequiredContexts(rules)
		if !slices.Equal(contexts, []string{"build", "lint", "e2e"}) || !named {
			t.Fatalf("RequiredContexts = %q, %v; want build, lint, e2e", contexts, named)
		}
		contexts, named = RequiredContexts(protectionRules(protectionAllTypes, "Lado/Integration"))
		if len(contexts) != 0 || named {
			t.Fatalf("RequiredContexts of lado/integration alone = %q, %v; want none", contexts, named)
		}
		if contexts, named = RequiredContexts(ports.LadoBranchRules{}); len(contexts) != 0 || named {
			t.Fatalf("RequiredContexts of no rules = %q, %v; want none", contexts, named)
		}
	})
}

func protectionOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func protectionDetailsEqual(a, b domain.LadoProtectionDetails) bool {
	return a.Provider == b.Provider && a.Repo == b.Repo && a.Branch == b.Branch && a.ErrorClass == b.ErrorClass &&
		slices.Equal(a.Missing, b.Missing) && slices.Equal(a.RequiredChecks, b.RequiredChecks)
}

// protectionClock is a clock the test moves.
type protectionClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *protectionClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *protectionClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// protectionHost answers branch rule reads and counts them per branch.
type protectionHost struct {
	mu         sync.Mutex
	reads      map[string]int
	rules      ports.LadoBranchRules
	err        error
	forRepoErr error
	// hold keeps each read open a while, so overlapping reads would show.
	hold         time.Duration
	atOnce, most int
}

func (h *protectionHost) ForRepo(_ context.Context, repo domain.TrackerRepo) (ports.LadoRepoClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.forRepoErr != nil {
		return nil, h.forRepoErr
	}
	return protectionClient{host: h}, nil
}

func (h *protectionHost) set(rules ports.LadoBranchRules, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rules, h.err = rules, err
}

func (h *protectionHost) count(branch string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads[branch]
}

func (h *protectionHost) mostAtOnce() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.most
}

// protectionClient is a repository client that only reads branch rules.
type protectionClient struct {
	ports.LadoRepoClient
	host *protectionHost
}

func (c protectionClient) BranchRules(ctx context.Context, _ domain.TrackerRepo, branch string) (ports.LadoBranchRules, error) {
	h := c.host
	h.mu.Lock()
	if h.reads == nil {
		h.reads = map[string]int{}
	}
	h.reads[branch]++
	h.atOnce++
	h.most = max(h.most, h.atOnce)
	rules, err, hold := h.rules, h.err, h.hold
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.atOnce--
		h.mu.Unlock()
	}()
	time.Sleep(hold)
	if ctx.Err() != nil {
		return ports.LadoBranchRules{}, ctx.Err()
	}
	return rules, err
}

func TestProtectionCacheRefreshes(t *testing.T) {
	ctx := context.Background()
	clock := &protectionClock{now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	good := protectionRules(protectionAllTypes, "build", "lado/integration")
	host := &protectionHost{rules: good}
	p := NewProtection(host, clock.Now, 10*time.Minute)

	first := p.Read(ctx, protectionRepo, "main", false)
	if first.Err != nil || len(first.Rules.Rules) != 4 || host.count("main") != 1 {
		t.Fatalf("first read = %+v (reads %d), want the host's rules", first, host.count("main"))
	}
	if first.Provider != domain.TrackerProviderGitHub || first.Repo != protectionRepo || first.Branch != "main" || !first.ReadAt.Equal(clock.Now()) {
		t.Fatalf("first read = %+v, want the repository, branch and read time", first)
	}

	clock.advance(10*time.Minute - time.Second)
	if again := p.Read(ctx, protectionRepo, "main", false); host.count("main") != 1 || !again.ReadAt.Equal(first.ReadAt) {
		t.Fatalf("read within the ttl went to the host (reads %d, read at %v)", host.count("main"), again.ReadAt)
	}
	if other := p.Read(ctx, protectionRepo, "release/1.0", false); other.Err != nil || host.count("release/1.0") != 1 {
		t.Fatalf("another branch = %+v (reads %d), want its own read", other, host.count("release/1.0"))
	}

	clock.advance(time.Second)
	if expired := p.Read(ctx, protectionRepo, "main", false); host.count("main") != 2 || !expired.ReadAt.Equal(clock.Now()) {
		t.Fatalf("read after the ttl = %+v (reads %d), want a new read", expired, host.count("main"))
	}
	if forced := p.Read(ctx, protectionRepo, "main", true); host.count("main") != 3 || forced.Err != nil {
		t.Fatalf("forced read = %+v (reads %d), want a new read at once", forced, host.count("main"))
	}

	// A failed read keeps the last good rules next to its error.
	host.set(ports.LadoBranchRules{}, &ports.LadoHTTPError{Status: 502, Message: "Bad Gateway"})
	failed := p.Read(ctx, protectionRepo, "main", true)
	var httpErr *ports.LadoHTTPError
	if host.count("main") != 4 || !errors.As(failed.Err, &httpErr) || httpErr.Status != 502 || len(failed.Rules.Rules) != 4 {
		t.Fatalf("failed read = %+v (reads %d), want the error and the last good rules", failed, host.count("main"))
	}
	if contexts, named := RequiredContexts(failed.Rules); !named || !slices.Equal(contexts, []string{"build"}) {
		t.Fatalf("required checks after a failure = %q, want the last good ones", contexts)
	}
	clock.advance(time.Minute)
	if cached := p.Read(ctx, protectionRepo, "main", false); host.count("main") != 4 || cached.Err == nil || len(cached.Rules.Rules) != 4 {
		t.Fatalf("read within the ttl after a failure = %+v (reads %d), want the cached failure", cached, host.count("main"))
	}

	// Recovery replaces the rules; a later failure keeps the newer ones.
	clock.advance(10 * time.Minute)
	host.set(protectionRules(protectionAllTypes[:2], "lint"), nil)
	if recovered := p.Read(ctx, protectionRepo, "main", false); recovered.Err != nil || len(recovered.Rules.Rules) != 2 {
		t.Fatalf("recovered read = %+v, want the new rules", recovered)
	}
	host.set(ports.LadoBranchRules{}, errors.New("dial tcp: i/o timeout"))
	if failedAgain := p.Read(ctx, protectionRepo, "main", true); failedAgain.Err == nil || len(failedAgain.Rules.Rules) != 2 {
		t.Fatalf("second failure = %+v, want the newer good rules", failedAgain)
	}

	// Without any good read, a failure has no rules.
	host.forRepoErr = errors.New("no token stored for github.com")
	if none := p.Read(ctx, protectionRepo, "develop", false); none.Err == nil || len(none.Rules.Rules) != 0 {
		t.Fatalf("read without a client = %+v, want the error and no rules", none)
	}
	host.forRepoErr = nil
}

func TestProtectionReadsABranchOnceAtATime(t *testing.T) {
	ctx := context.Background()
	clock := &protectionClock{now: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}
	host := &protectionHost{rules: protectionRules(protectionAllTypes, "lado/integration"), hold: 2 * time.Millisecond}
	p := NewProtection(host, clock.Now, 10*time.Minute)

	read := func(force bool, n int) {
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r := p.Read(ctx, protectionRepo, "main", force); r.Err != nil || len(r.Rules.Rules) != 4 {
					t.Errorf("read = %+v, want the rules", r)
				}
			}()
		}
		wg.Wait()
	}
	// Callers at the same time find the first one's read in the cache.
	read(false, 8)
	if n := host.count("main"); n != 1 {
		t.Fatalf("host reads = %d, want 1 for concurrent callers", n)
	}
	// Forced reads of one branch never overlap.
	read(true, 8)
	if n, most := host.count("main"), host.mostAtOnce(); n != 9 || most != 1 {
		t.Fatalf("host reads = %d with up to %d at once, want 9 one at a time", n, most)
	}

	// A read its caller cancelled is not kept.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if r := p.Read(cancelled, protectionRepo, "main", true); !errors.Is(r.Err, context.Canceled) || len(r.Rules.Rules) != 4 {
		t.Fatalf("cancelled read = %+v, want the cancellation and the last good rules", r)
	}
	if r := p.Read(ctx, protectionRepo, "main", false); r.Err != nil || host.count("main") != 10 {
		t.Fatalf("read after a cancelled one = %+v (reads %d), want the cached good read", r, host.count("main"))
	}
}
