package gitlab

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newSSHTestProvider(t *testing.T, allowed ...string) *Provider {
	t.Helper()
	p, err := NewProvider(ProviderOptions{
		Token:              StaticTokenSource("tok"),
		SkipTokenPreflight: true,
		AllowedHosts:       allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestParseRepositorySSHPortMapsToAllowlistedHost covers self-managed GitLab
// installs whose SSH port differs from the web port: the SSH port is never the
// API authority, so the remote must resolve to the allowlisted web host.
func TestParseRepositorySSHPortMapsToAllowlistedHost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed []string
		remote  string
		ok      bool
		want    ports.SCMRepo
	}{
		{
			name:    "ssh port dropped for bare allowlisted host",
			allowed: []string{"host"},
			remote:  "ssh://git@host:30022/a/b/c.git",
			ok:      true,
			want:    ports.SCMRepo{Provider: "gitlab", Host: "host", Owner: "a/b", Name: "c", Repo: "a/b/c"},
		},
		{
			name:    "ssh without port keeps host",
			allowed: []string{"host"},
			remote:  "ssh://git@host/group/repo.git",
			ok:      true,
			want:    ports.SCMRepo{Provider: "gitlab", Host: "host", Owner: "group", Name: "repo", Repo: "group/repo"},
		},
		{
			name:    "ssh port maps to the single allowlisted web port",
			allowed: []string{"host:8443"},
			remote:  "ssh://git@host:30022/group/repo.git",
			ok:      true,
			want:    ports.SCMRepo{Provider: "gitlab", Host: "host:8443", Owner: "group", Name: "repo", Repo: "group/repo"},
		},
		{
			name:    "bare host wins over a web port entry",
			allowed: []string{"host:8443", "host"},
			remote:  "ssh://git@host:30022/group/repo.git",
			ok:      true,
			want:    ports.SCMRepo{Provider: "gitlab", Host: "host", Owner: "group", Name: "repo", Repo: "group/repo"},
		},
		{
			name:    "ambiguous web ports fall back to the literal host:port",
			allowed: []string{"host:8443", "host:30022"},
			remote:  "ssh://git@host:30022/group/repo.git",
			ok:      true,
			want:    ports.SCMRepo{Provider: "gitlab", Host: "host:30022", Owner: "group", Name: "repo", Repo: "group/repo"},
		},
		{
			name:    "ambiguous web ports without literal match are rejected",
			allowed: []string{"host:8443", "host:9443"},
			remote:  "ssh://git@host:30022/group/repo.git",
		},
		{
			name:   "gitlab.com ssh port",
			remote: "ssh://git@gitlab.com:443/group/repo.git",
			ok:     true,
			want:   ports.SCMRepo{Provider: "gitlab", Host: "gitlab.com", Owner: "group", Name: "repo", Repo: "group/repo"},
		},
		{
			name:    "https keeps the web port",
			allowed: []string{"host:8443"},
			remote:  "https://host:8443/a/b.git",
			ok:      true,
			want:    ports.SCMRepo{Provider: "gitlab", Host: "host:8443", Owner: "a", Name: "b", Repo: "a/b"},
		},
		{
			name:    "https port must be allowlisted as written",
			allowed: []string{"host"},
			remote:  "https://host:8443/a/b.git",
		},
		{
			name:    "scp form with subgroup",
			allowed: []string{"host"},
			remote:  "git@host:group/sub/repo.git",
			ok:      true,
			want:    ports.SCMRepo{Provider: "gitlab", Host: "host", Owner: "group/sub", Name: "repo", Repo: "group/sub/repo"},
		},
		{
			name:    "ssh host not allowlisted",
			allowed: []string{"host"},
			remote:  "ssh://git@other:30022/a/b/c.git",
		},
		{
			name:    "ssh host name differs from allowlisted web port host",
			allowed: []string{"host:8443"},
			remote:  "ssh://git@evil-host:30022/a/b.git",
		},
		{
			name:    "scp host not allowlisted",
			allowed: []string{"host"},
			remote:  "git@other:group/repo.git",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newSSHTestProvider(t, tc.allowed...)
			got, ok := p.ParseRepository(tc.remote)
			if ok != tc.ok {
				t.Fatalf("ParseRepository(%q) ok = %v, want %v (repo %+v)", tc.remote, ok, tc.ok, got)
			}
			if !tc.ok {
				return
			}
			if got != tc.want {
				t.Fatalf("ParseRepository(%q) = %+v, want %+v", tc.remote, got, tc.want)
			}
		})
	}
}
