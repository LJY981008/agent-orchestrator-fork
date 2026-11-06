package gitlab

// LADO fork: tests for SkipTokenPreflight and SetHosts (issue #19).

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// countingTokenSource counts Token calls so tests can assert when a token
// is (not) read.
type countingTokenSource struct {
	token string
	calls atomic.Int32
}

func (c *countingTokenSource) Token(context.Context) (string, error) {
	c.calls.Add(1)
	if c.token == "" {
		return "", scmgitlab.ErrNoToken
	}
	return c.token, nil
}

// tokenForHost reads the token the tracker would attach for host.
func tokenForHost(t *testing.T, tr *Tracker, host string) string {
	t.Helper()
	he, err := tr.configForHost(host)
	if err != nil {
		t.Fatalf("configForHost(%q): %v", host, err)
	}
	tok, err := he.tokens.Token(context.Background())
	if err != nil {
		t.Fatalf("token for %q: %v", host, err)
	}
	return tok
}

func TestNew_SkipTokenPreflight(t *testing.T) {
	src := &countingTokenSource{}
	tr, err := New(Options{Token: src, SkipTokenPreflight: true})
	if err != nil {
		t.Fatalf("New with SkipTokenPreflight and no usable token: %v", err)
	}
	if tr == nil {
		t.Fatal("New returned nil tracker")
	}
	if n := src.calls.Load(); n != 0 {
		t.Fatalf("Token called %d times during New, want 0", n)
	}

	// Without the option, a missing default token still fails New.
	if _, err := New(Options{Token: &countingTokenSource{}}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New without SkipTokenPreflight = %v, want ErrNoToken", err)
	}
	// A nil source is still rejected: there is nothing to try at call time.
	if _, err := New(Options{SkipTokenPreflight: true}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New with nil source = %v, want ErrNoToken", err)
	}
}

func TestSkipTokenPreflight_GitLabComFailsAtCallTime(t *testing.T) {
	f := newFakeGL(t) // any request would fail the test as unexpected
	tr, err := New(Options{
		BaseURL:            f.server.URL,
		HTTPClient:         f.server.Client(),
		Token:              scmgitlab.StaticTokenSource(""),
		SkipTokenPreflight: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "o/r#1"})
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("Get without default token = %v, want ErrNoToken", err)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("server received calls without a token: %#v", calls)
	}
}

func TestSetHosts_AllowsThenRejectsHost(t *testing.T) {
	const host = "gitlab.example.com"
	tr, err := New(Options{Token: scmgitlab.StaticTokenSource(""), SkipTokenPreflight: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := tr.ConfigForHost(host); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("ConfigForHost before SetHosts = %v, want ErrHostNotAllowed", err)
	}

	tr.SetHosts([]string{host}, map[string]scmgitlab.TokenSource{host: scmgitlab.StaticTokenSource("tok-a")})
	if err := tr.ConfigForHost(host); err != nil {
		t.Fatalf("ConfigForHost after SetHosts = %v, want nil", err)
	}
	if got := tokenForHost(t, tr, host); got != "tok-a" {
		t.Fatalf("token = %q, want tok-a", got)
	}

	tr.SetHosts(nil, nil)
	if err := tr.ConfigForHost(host); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("ConfigForHost after SetHosts(nil, nil) = %v, want ErrHostNotAllowed", err)
	}
	// gitlab.com stays allowed whatever the host list is.
	for _, h := range []string{"", "gitlab.com"} {
		if err := tr.ConfigForHost(h); err != nil {
			t.Fatalf("ConfigForHost(%q) = %v, want nil", h, err)
		}
	}
}

func TestSetHosts_TokenRules(t *testing.T) {
	tr, err := New(Options{
		Token:        scmgitlab.StaticTokenSource("tok-default"),
		AllowedHosts: []string{"old.example.com"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tr.SetHosts(
		[]string{" GitLab.Example.com ", "plain.example.com", "gitlab.com", ""},
		map[string]scmgitlab.TokenSource{
			"GITLAB.EXAMPLE.COM": scmgitlab.StaticTokenSource("tok-a"),
			"unlisted.example":   scmgitlab.StaticTokenSource("tok-x"),
			"plain.example.com":  nil,
		},
	)

	// Host token keys are normalized like the host list.
	if got := tokenForHost(t, tr, "gitlab.example.com"); got != "tok-a" {
		t.Fatalf("gitlab.example.com token = %q, want tok-a", got)
	}
	// An allowed host without its own token falls back to the default token.
	if got := tokenForHost(t, tr, "plain.example.com"); got != "tok-default" {
		t.Fatalf("plain.example.com token = %q, want tok-default", got)
	}
	// A token alone does not allow a host.
	if err := tr.ConfigForHost("unlisted.example"); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("unlisted host = %v, want ErrHostNotAllowed", err)
	}
	// The previous host list is replaced, not merged.
	if err := tr.ConfigForHost("old.example.com"); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("old host = %v, want ErrHostNotAllowed", err)
	}
	// gitlab.com in the list does not change the default config.
	if got := tokenForHost(t, tr, "gitlab.com"); got != "tok-default" {
		t.Fatalf("gitlab.com token = %q, want tok-default", got)
	}
	he, _ := tr.configForHost("gitlab.example.com")
	if he.baseURL != "https://gitlab.example.com/api/v4" {
		t.Fatalf("baseURL = %q", he.baseURL)
	}

	// Replacing the token takes effect on the next lookup.
	tr.SetHosts([]string{"gitlab.example.com"}, map[string]scmgitlab.TokenSource{
		"gitlab.example.com": scmgitlab.StaticTokenSource("tok-b"),
	})
	if got := tokenForHost(t, tr, "gitlab.example.com"); got != "tok-b" {
		t.Fatalf("token after replace = %q, want tok-b", got)
	}
}

func TestSetHosts_DoesNotReadTokens(t *testing.T) {
	def := &countingTokenSource{token: "tok-default"}
	host := &countingTokenSource{token: "tok-host"}
	tr, err := New(Options{Token: def, SkipTokenPreflight: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr.SetHosts([]string{"a.example", "b.example"}, map[string]scmgitlab.TokenSource{"a.example": host})
	if n := def.calls.Load() + host.calls.Load(); n != 0 {
		t.Fatalf("SetHosts read tokens %d times, want 0", n)
	}
}

func TestSetHosts_Concurrent(t *testing.T) {
	f := newFakeGL(t)
	tr, err := New(Options{
		BaseURL:            f.server.URL,
		HTTPClient:         f.server.Client(),
		Token:              scmgitlab.StaticTokenSource(""),
		SkipTokenPreflight: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const host = "gitlab.example.com"
	on := []string{host}
	tokens := map[string]scmgitlab.TokenSource{host: scmgitlab.StaticTokenSource("tok")}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if i%2 == 0 {
				tr.SetHosts(on, tokens)
			} else {
				tr.SetHosts(nil, nil)
			}
		}
		close(stop)
	}()
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := tr.ConfigForHost(host)
				if err != nil && !errors.Is(err, ErrHostNotAllowed) {
					t.Errorf("ConfigForHost: %v", err)
					return
				}
				// gitlab.com has no default token: fails before any request.
				if _, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "o/r#1"}); !errors.Is(err, ErrNoToken) {
					t.Errorf("Get gitlab.com = %v, want ErrNoToken", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
