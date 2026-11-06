package gitlab

// LADO fork: tests for Provider.SetHosts, which swaps the self-managed
// allowlist and per-host tokens while the provider runs.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tokenOf(t *testing.T, c *Client) string {
	t.Helper()
	if c == nil {
		t.Fatal("client is nil")
	}
	if c.tokens == nil {
		t.Fatal("client has no token source")
	}
	tok, err := c.tokens.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return tok
}

func newHostsTestProvider(t *testing.T, opts ProviderOptions) *Provider {
	t.Helper()
	if opts.Token == nil {
		opts.Token = StaticTokenSource("default-token")
	}
	opts.SkipTokenPreflight = true
	p, err := NewProvider(opts)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

func TestSetHostsReplacesAllowlistAndTokens(t *testing.T) {
	const host = "gitlab.example.internal"
	const remote = "git@" + host + ":g/p.git"
	p := newHostsTestProvider(t, ProviderOptions{})

	if _, ok := p.ParseRepository(remote); ok {
		t.Fatal("ParseRepository succeeded before the host was allowed")
	}
	if c := p.clientForHost(host); c != nil {
		t.Fatal("clientForHost returned a client before the host was allowed")
	}

	p.SetHosts([]string{" GitLab.Example.Internal "}, map[string]TokenSource{host: StaticTokenSource("token-a")})
	repo, ok := p.ParseRepository(remote)
	if !ok {
		t.Fatal("ParseRepository failed after SetHosts")
	}
	if repo.Host != host || repo.Owner != "g" || repo.Name != "p" {
		t.Fatalf("repo = %+v", repo)
	}
	first := p.clientForHost(host)
	if got := tokenOf(t, first); got != "token-a" {
		t.Fatalf("token = %q, want token-a", got)
	}
	if again := p.clientForHost(host); again != first {
		t.Fatal("clientForHost did not reuse the cached client")
	}

	p.SetHosts([]string{host}, map[string]TokenSource{host: StaticTokenSource("token-b")})
	second := p.clientForHost(host)
	if second == first {
		t.Fatal("clientForHost reused the client from before SetHosts")
	}
	if got := tokenOf(t, second); got != "token-b" {
		t.Fatalf("token = %q, want token-b", got)
	}

	// A host allowed without its own token uses the default token, as at
	// construction.
	p.SetHosts([]string{host}, nil)
	if got := tokenOf(t, p.clientForHost(host)); got != "default-token" {
		t.Fatalf("token = %q, want default-token", got)
	}

	p.SetHosts(nil, nil)
	if _, ok := p.ParseRepository(remote); ok {
		t.Fatal("ParseRepository succeeded after SetHosts(nil, nil)")
	}
	if c := p.clientForHost(host); c != nil {
		t.Fatal("clientForHost returned a client after SetHosts(nil, nil)")
	}
	// gitlab.com stays allowed.
	if _, ok := p.ParseRepository("git@gitlab.com:g/p.git"); !ok {
		t.Fatal("gitlab.com remote rejected after SetHosts(nil, nil)")
	}
}

func TestSetHostsSSHPortRemote(t *testing.T) {
	p := newHostsTestProvider(t, ProviderOptions{})
	const remote = "ssh://git@gitlab.example.internal:30022/a/b/c.git"
	if _, ok := p.ParseRepository(remote); ok {
		t.Fatal("ssh remote parsed before the host was allowed")
	}
	p.SetHosts([]string{"gitlab.example.internal"}, nil)
	repo, ok := p.ParseRepository(remote)
	if !ok || repo.Host != "gitlab.example.internal" || repo.Owner != "a/b" || repo.Name != "c" {
		t.Fatalf("repo = %+v, ok = %v", repo, ok)
	}
	// A host:port web entry with the same host name is picked for the ssh
	// port remote.
	p.SetHosts([]string{"gitlab.example.internal:8443"}, nil)
	repo, ok = p.ParseRepository(remote)
	if !ok || repo.Host != "gitlab.example.internal:8443" {
		t.Fatalf("repo = %+v, ok = %v", repo, ok)
	}
}

func TestSetHostsCredentialsAvailable(t *testing.T) {
	ctx := context.Background()
	p := newHostsTestProvider(t, ProviderOptions{Token: StaticTokenSource("")})
	if ok, err := p.SCMCredentialsAvailable(ctx); err != nil || ok {
		t.Fatalf("before SetHosts: (%v, %v), want (false, nil)", ok, err)
	}
	p.SetHosts([]string{"h.internal"}, map[string]TokenSource{"h.internal": StaticTokenSource("tok")})
	if ok, err := p.SCMCredentialsAvailable(ctx); err != nil || !ok {
		t.Fatalf("after SetHosts: (%v, %v), want (true, nil)", ok, err)
	}
	p.SetHosts(nil, nil)
	if ok, err := p.SCMCredentialsAvailable(ctx); err != nil || ok {
		t.Fatalf("after SetHosts(nil, nil): (%v, %v), want (false, nil)", ok, err)
	}
}

// userServer answers /api/v4/user with a username built from the bearer
// token and counts requests.
func userServer(t *testing.T, tls bool, delay <-chan struct{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if delay != nil {
			select {
			case <-delay:
			case <-r.Context().Done():
				return
			}
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_ = json.NewEncoder(w).Encode(map[string]string{"username": "user-" + tok})
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestSetHostsRefetchesCachedIdentities(t *testing.T) {
	ctx := context.Background()
	selfSrv, selfCalls := userServer(t, true, nil)
	comSrv, comCalls := userServer(t, false, nil)
	host := selfSrv.Listener.Addr().String()

	p := newHostsTestProvider(t, ProviderOptions{
		HTTPClient: selfSrv.Client(),
		RESTBase:   comSrv.URL + "/api/v4",
		Token:      StaticTokenSource("com"),
	})
	p.SetHosts([]string{host}, map[string]TokenSource{host: StaticTokenSource("a")})

	ident, err := p.AuthenticatedIdentityForHost(ctx, host)
	if err != nil || ident.Login != "user-a" {
		t.Fatalf("identity = %+v, %v; want user-a", ident, err)
	}
	if ident, err := p.AuthenticatedIdentity(ctx); err != nil || ident.Login != "user-com" {
		t.Fatalf("gitlab.com identity = %+v, %v", ident, err)
	}
	// Cached: no new requests.
	_, _ = p.AuthenticatedIdentityForHost(ctx, host)
	_, _ = p.AuthenticatedIdentity(ctx)
	if selfCalls.Load() != 1 || comCalls.Load() != 1 {
		t.Fatalf("calls = %d/%d, want 1/1", selfCalls.Load(), comCalls.Load())
	}

	p.SetHosts([]string{host}, map[string]TokenSource{host: StaticTokenSource("b")})
	ident, err = p.AuthenticatedIdentityForHost(ctx, host)
	if err != nil || ident.Login != "user-b" {
		t.Fatalf("identity after SetHosts = %+v, %v; want user-b", ident, err)
	}
	if _, err := p.AuthenticatedIdentity(ctx); err != nil {
		t.Fatalf("gitlab.com identity after SetHosts: %v", err)
	}
	if selfCalls.Load() != 2 || comCalls.Load() != 2 {
		t.Fatalf("calls = %d/%d, want 2/2 (both cached identities fetched again)", selfCalls.Load(), comCalls.Load())
	}

	p.SetHosts(nil, nil)
	if _, err := p.AuthenticatedIdentityForHost(ctx, host); err == nil {
		t.Fatal("identity for a removed host succeeded")
	}
}

func TestSetHostsConcurrentReaders(t *testing.T) {
	ctx := context.Background()
	srv, _ := userServer(t, true, nil)
	host := srv.Listener.Addr().String()
	p := newHostsTestProvider(t, ProviderOptions{
		HTTPClient: srv.Client(),
		RESTBase:   "https://unused.invalid/api/v4",
	})

	const rounds = 200
	var wg sync.WaitGroup
	stop := make(chan struct{})
	reader := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					fn()
				}
			}
		}()
	}
	reader(func() { _, _ = p.ParseRepository("git@" + host + ":g/p.git") })
	reader(func() { _, _ = p.ParseRepository("ssh://git@127.0.0.1:30022/g/p.git") })
	reader(func() { _ = p.clientForHost(host) })
	reader(func() { _, _ = p.SCMCredentialsAvailable(ctx) })
	reader(func() { _, _ = p.AuthenticatedIdentityForHost(ctx, host) })

	for i := range rounds {
		if i%2 == 0 {
			p.SetHosts([]string{host}, map[string]TokenSource{host: StaticTokenSource("t")})
		} else {
			p.SetHosts(nil, nil)
		}
	}
	close(stop)
	wg.Wait()
}

func TestSetHostsDoesNotWaitForIdentityRequest(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	srv, calls := userServer(t, true, release)
	host := srv.Listener.Addr().String()
	p := newHostsTestProvider(t, ProviderOptions{
		HTTPClient: srv.Client(),
		RESTBase:   "https://unused.invalid/api/v4",
	})
	p.SetHosts([]string{host}, map[string]TokenSource{host: StaticTokenSource("a")})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	identityDone := make(chan error, 1)
	go func() {
		_, err := p.AuthenticatedIdentityForHost(ctx, host)
		identityDone <- err
	}()

	// Wait until the slow /user request is in flight (identityMu is held).
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("identity request never reached the server")
		}
		time.Sleep(5 * time.Millisecond)
	}

	setDone := make(chan struct{})
	go func() {
		p.SetHosts([]string{host}, map[string]TokenSource{host: StaticTokenSource("b")})
		close(setDone)
	}()
	select {
	case <-setDone:
	case <-time.After(time.Second):
		unblock()
		t.Fatal("SetHosts waited for the identity request in flight")
	}

	unblock()
	select {
	case err := <-identityDone:
		if err != nil {
			t.Fatalf("identity: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("identity request did not finish within 5s")
	}

	// The identity resolved with the old token is stale after SetHosts.
	ident, err := p.AuthenticatedIdentityForHost(context.Background(), host)
	if err != nil || ident.Login != "user-b" {
		t.Fatalf("identity after SetHosts = %+v, %v; want user-b", ident, err)
	}
}
