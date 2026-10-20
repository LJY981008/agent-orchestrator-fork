package ladoconnect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeGitHubTokens stands in for AO's githubpat store.
type fakeGitHubTokens struct {
	token string
}

func (f *fakeGitHubTokens) StorePAT(_ context.Context, token string) error {
	f.token = token
	return nil
}

func (f *fakeGitHubTokens) HasPAT(context.Context) bool { return f.token != "" }

func (f *fakeGitHubTokens) DeletePAT(context.Context) error {
	f.token = ""
	return nil
}

func (f *fakeGitHubTokens) GetToken(context.Context) (string, error) {
	if f.token == "" {
		return "", errors.New("no GitHub token stored")
	}
	return f.token, nil
}

func newTestCredentials(t *testing.T) (*Credentials, *fakeGitHubTokens, string) {
	t.Helper()
	stateDir := t.TempDir()
	github := &fakeGitHubTokens{}
	creds := NewCredentials(github, stateDir)
	creds.now = func() time.Time { return time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC) }
	return creds, github, stateDir
}

func TestCredentialsStoreGitLabTokensInAPrivateFile(t *testing.T) {
	creds, _, stateDir := newTestCredentials(t)
	ctx := context.Background()

	stored, err := creds.Store(ctx, "GitLab.Example.com", "glpat-secret-1")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if stored.Host != "gitlab.example.com" || stored.Provider != domain.TrackerProviderGitLab {
		t.Fatalf("stored = %+v, want gitlab.example.com on GitLab", stored)
	}
	token, ok, err := creds.Token(ctx, "gitlab.example.com")
	if err != nil || !ok || token != "glpat-secret-1" {
		t.Fatalf("Token = %q, %v, %v; want the stored token", token, ok, err)
	}

	path := filepath.Join(stateDir, "lado", "credentials", "gitlab-tokens.json")
	if runtime.GOOS != "windows" {
		fileInfo, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat token file: %v", err)
		}
		if mode := fileInfo.Mode().Perm(); mode != 0o600 {
			t.Fatalf("token file mode = %o, want 600", mode)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat credentials dir: %v", err)
		}
		if mode := dirInfo.Mode().Perm(); mode != 0o700 {
			t.Fatalf("credentials dir mode = %o, want 700", mode)
		}
	}

	if _, err := creds.Store(ctx, "gitlab.example.com", "glpat-secret-2"); err != nil {
		t.Fatalf("Store again: %v", err)
	}
	if token, _, _ := creds.Token(ctx, "gitlab.example.com"); token != "glpat-secret-2" {
		t.Fatalf("Token after replacing = %q, want the new token", token)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read credentials dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("credentials dir has %d entries, want only the token file", len(entries))
	}

	if err := creds.Delete(ctx, "gitlab.example.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := creds.Token(ctx, "gitlab.example.com"); err != nil || ok {
		t.Fatalf("Token after delete = ok %v, err %v; want none", ok, err)
	}
	if err := creds.Delete(ctx, "gitlab.example.com"); err != nil {
		t.Fatalf("Delete of a missing token: %v", err)
	}
}

func TestCredentialsKeepGitHubTokensInAOsStore(t *testing.T) {
	creds, github, stateDir := newTestCredentials(t)
	ctx := context.Background()

	if _, ok, err := creds.Token(ctx, "github.com"); err != nil || ok {
		t.Fatalf("Token before storing = ok %v, err %v; want none", ok, err)
	}
	stored, err := creds.Store(ctx, "www.github.com", "ghp_secret")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if stored.Host != "github.com" || stored.Provider != domain.TrackerProviderGitHub || github.token != "ghp_secret" {
		t.Fatalf("stored = %+v, AO store has %q; want github.com in AO's store", stored, github.token)
	}
	if token, ok, err := creds.Token(ctx, "github.com"); err != nil || !ok || token != "ghp_secret" {
		t.Fatalf("Token = %q, %v, %v", token, ok, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "lado", "credentials")); !os.IsNotExist(err) {
		t.Fatalf("a github.com token created LADO's credentials dir (err %v)", err)
	}
	if err := creds.Delete(ctx, "github.com"); err != nil || github.token != "" {
		t.Fatalf("Delete = %v, AO store has %q; want it removed", err, github.token)
	}
}

func TestCredentialsListHostsWithoutTokens(t *testing.T) {
	creds, _, _ := newTestCredentials(t)
	ctx := context.Background()
	for host, token := range map[string]string{"github.com": "ghp_a", "gitlab.com": "glpat-b", "gitlab.example.com": "glpat-c"} {
		if _, err := creds.Store(ctx, host, token); err != nil {
			t.Fatalf("Store %s: %v", host, err)
		}
	}
	listed, err := creds.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var hosts []string
	for _, c := range listed {
		hosts = append(hosts, c.Host)
	}
	if strings.Join(hosts, ",") != "github.com,gitlab.com,gitlab.example.com" {
		t.Fatalf("listed hosts = %v", hosts)
	}
	selfManaged, err := creds.GitLabHosts(ctx)
	if err != nil {
		t.Fatalf("GitLabHosts: %v", err)
	}
	if strings.Join(selfManaged, ",") != "gitlab.example.com" {
		t.Fatalf("self-managed hosts = %v, want only gitlab.example.com", selfManaged)
	}
}

func TestCredentialsRefuseBadHostsAndTokens(t *testing.T) {
	creds, _, _ := newTestCredentials(t)
	ctx := context.Background()
	for _, host := range []string{"", "https://gitlab.com", "gitlab.com/group", "gitlab example.com", "gitlab.example.com:8443", "acme.ghe.io", "api.github.com"} {
		if _, err := creds.Store(ctx, host, "glpat-x"); err == nil {
			t.Errorf("Store(%q) succeeded, want an error", host)
		}
	}
	for _, token := range []string{"", "   ", "glpat-a b", "glpat-a\nb"} {
		_, err := creds.Store(ctx, "gitlab.com", token)
		if err == nil {
			t.Errorf("Store with token %q succeeded, want an error", token)
			continue
		}
		if strings.TrimSpace(token) != "" && strings.Contains(err.Error(), strings.TrimSpace(token)) {
			t.Errorf("error %q repeats the token", err)
		}
	}
}

func TestCredentialsReportACorruptTokenFile(t *testing.T) {
	creds, _, stateDir := newTestCredentials(t)
	dir := filepath.Join(stateDir, "lado", "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gitlab-tokens.json"), []byte(`{"hosts":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := creds.Token(context.Background(), "gitlab.com"); err == nil {
		t.Fatal("Token read a corrupt file without an error")
	}
}
