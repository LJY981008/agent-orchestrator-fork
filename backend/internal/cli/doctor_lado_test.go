package cli

// LADO fork: ao doctor reads LADO's stored GitLab tokens after the env and
// glab tokens.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ladoDoctorSecret = "glpat-lado-stored-secret"

// writeLadoGitLabTokens writes LADO's GitLab token file under the state dir
// (AO_DATA_DIR in tests, since an explicit data dir is also the state dir).
func writeLadoGitLabTokens(t *testing.T, dataDir string, hosts map[string]string) {
	t.Helper()
	type stored struct {
		Token string `json:"token"`
	}
	file := struct {
		Version int               `json:"version"`
		Hosts   map[string]stored `json:"hosts"`
	}{Version: 1, Hosts: map[string]stored{}}
	for host, token := range hosts {
		file.Hosts[host] = stored{Token: token}
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(dataDir, "lado", "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gitlab-tokens.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func ladoGitLabServer(t *testing.T, wantToken string, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v4/user" {
			t.Errorf("unexpected gitlab probe: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != wantToken {
			t.Errorf("PRIVATE-TOKEN = %q, want the stored token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func gitlabChecks(checks []doctorCheck) []doctorCheck {
	var out []doctorCheck
	for _, check := range checks {
		if check.Name == "gitlab-token" {
			out = append(out, check)
		}
	}
	return out
}

func TestDoctorChecksLadoStoredGitLabToken(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := ladoGitLabServer(t, ladoDoctorSecret, http.StatusOK, `{"username":"lado-user"}`)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})
	writeLadoGitLabTokens(t, cfg.dataDir, map[string]string{"gitlab.example.test": ladoDoctorSecret})
	var probedHost string
	c.deps.HTTPClient = srv.Client()
	c.deps.DoctorGitLabHostRESTBase = func(host string) string {
		probedHost = host
		return srv.URL + "/api/v4"
	}

	checks := gitlabChecks(c.runDoctor(context.Background()))
	if len(checks) != 1 {
		t.Fatalf("gitlab-token checks = %+v, want one stored-host check", checks)
	}
	check := checks[0]
	if check.Level != doctorPass || check.Section != doctorSectionGitLab {
		t.Fatalf("gitlab-token check = %+v, want PASS in GitLab", check)
	}
	if want := "LADO stored token valid for lado-user on gitlab.example.test"; check.Message != want {
		t.Fatalf("message = %q, want %q", check.Message, want)
	}
	if strings.Contains(check.Message, ladoDoctorSecret) {
		t.Fatalf("message leaks the token: %q", check.Message)
	}
	if probedHost != "gitlab.example.test" {
		t.Fatalf("probed host = %q, want gitlab.example.test", probedHost)
	}
}

func TestDoctorLadoStoredGitLabTokenOutputHidesToken(t *testing.T) {
	cfg := setConfigEnv(t)
	clearDoctorGitHubEnv(t)
	clearDoctorGitLabEnv(t)
	srv := ladoGitLabServer(t, ladoDoctorSecret, http.StatusOK, `{"username":"lado-user"}`)
	writeLadoGitLabTokens(t, cfg.dataDir, map[string]string{"gitlab.example.test": ladoDoctorSecret})
	out, errOut, _ := executeCLI(t, Deps{
		LookPath: func(name string) (string, error) {
			if name == "git" {
				return "/bin/git", nil
			}
			return "", os.ErrNotExist
		},
		CommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("git version 2.43.0\n"), nil
		},
		ProcessAlive:             func(int) bool { return false },
		HTTPClient:               srv.Client(),
		DoctorGitLabHostRESTBase: func(string) string { return srv.URL + "/api/v4" },
	}, "doctor")
	if !strings.Contains(out, "PASS gitlab-token: LADO stored token valid for lado-user on gitlab.example.test") {
		t.Fatalf("doctor output missing the stored token check:\n%s", out)
	}
	if strings.Contains(out, ladoDoctorSecret) || strings.Contains(errOut, ladoDoctorSecret) {
		t.Fatalf("doctor output leaks the token:\n%s\n%s", out, errOut)
	}
}

func TestDoctorFailsRejectedLadoStoredGitLabToken(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := ladoGitLabServer(t, ladoDoctorSecret, http.StatusUnauthorized, `{"message":"401 Unauthorized"}`)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})
	writeLadoGitLabTokens(t, cfg.dataDir, map[string]string{"gitlab.example.test": ladoDoctorSecret})
	c.deps.HTTPClient = srv.Client()
	c.deps.DoctorGitLabHostRESTBase = func(string) string { return srv.URL + "/api/v4" }

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "gitlab-token")
	if check.Level != doctorFail || !strings.Contains(check.Message, "gitlab.example.test") || !strings.Contains(check.Message, "HTTP 401") {
		t.Fatalf("gitlab-token check = %+v, want FAIL naming the host", check)
	}
	if strings.Contains(check.Message, ladoDoctorSecret) {
		t.Fatalf("message leaks the token: %q", check.Message)
	}
}

// An env token still checks gitlab.com, and each stored self-managed host is
// checked on its own.
func TestDoctorChecksEnvAndLadoStoredGitLabTokens(t *testing.T) {
	cfg := setConfigEnv(t)
	dotCom := gitlabDoctorServer(t, http.StatusOK, `{"username":"env-user"}`)
	t.Cleanup(dotCom.Close)
	selfManaged := ladoGitLabServer(t, ladoDoctorSecret, http.StatusOK, `{"username":"lado-user"}`)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})
	t.Setenv("AO_GITLAB_TOKEN", "env-token")
	writeLadoGitLabTokens(t, cfg.dataDir, map[string]string{"gitlab.example.test": ladoDoctorSecret})
	c.deps.HTTPClient = http.DefaultClient
	c.deps.DoctorGitLabRESTBase = dotCom.URL
	c.deps.DoctorGitLabHostRESTBase = func(string) string { return selfManaged.URL + "/api/v4" }

	checks := gitlabChecks(c.runDoctor(context.Background()))
	if len(checks) != 2 {
		t.Fatalf("gitlab-token checks = %+v, want env and stored-host checks", checks)
	}
	if checks[0].Level != doctorPass || !strings.Contains(checks[0].Message, "AO_GITLAB_TOKEN token valid for env-user") {
		t.Fatalf("env check = %+v", checks[0])
	}
	if checks[1].Level != doctorPass || checks[1].Message != "LADO stored token valid for lado-user on gitlab.example.test" {
		t.Fatalf("stored check = %+v", checks[1])
	}
}

// A stored gitlab.com token stands in for env and glab, as in the daemon's
// gitlab.com chain.
func TestDoctorUsesLadoStoredGitLabDotComToken(t *testing.T) {
	cfg := setConfigEnv(t)
	dotCom := gitlabDoctorServer(t, http.StatusOK, `{"username":"dotcom-user"}`)
	t.Cleanup(dotCom.Close)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})
	writeLadoGitLabTokens(t, cfg.dataDir, map[string]string{"gitlab.com": ladoDoctorSecret})
	c.deps.HTTPClient = dotCom.Client()
	c.deps.DoctorGitLabRESTBase = dotCom.URL
	c.deps.DoctorGitLabHostRESTBase = func(host string) string {
		t.Errorf("gitlab.com must use the gitlab.com REST base, got a host probe for %s", host)
		return ""
	}

	checks := gitlabChecks(c.runDoctor(context.Background()))
	if len(checks) != 1 || checks[0].Level != doctorPass || checks[0].Message != "LADO stored token valid for dotcom-user on gitlab.com" {
		t.Fatalf("gitlab-token checks = %+v, want one PASS for gitlab.com", checks)
	}
}

func TestDoctorWarnsOnUnreadableLadoGitLabTokens(t *testing.T) {
	cfg := setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})
	dir := filepath.Join(cfg.dataDir, "lado", "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gitlab-tokens.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	checks := gitlabChecks(c.runDoctor(context.Background()))
	if len(checks) != 2 {
		t.Fatalf("gitlab-token checks = %+v, want the missing-token and store warnings", checks)
	}
	for _, check := range checks {
		if check.Level != doctorWarn {
			t.Fatalf("check = %+v, want WARN", check)
		}
	}
	if !strings.Contains(checks[1].Message, "could not read LADO's stored GitLab tokens") {
		t.Fatalf("store warning = %+v", checks[1])
	}
}
