package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const ladoAccountLimitsBody = `{"limits":[
 {"accountKey":"claude:device-claude","provider":"claude","accountId":"device-claude","sessionId":"mer-7","identityKnown":true,"active":true,
  "resetAt":"2026-11-10T10:00:00Z","resetGuessed":true,"detectedAt":"2026-11-10T07:00:00Z","updatedAt":"2026-11-10T07:00:00Z","releasedWaiting":0},
 {"accountKey":"codex:work","provider":"codex","accountId":"work","identityKnown":false,"active":false,
  "resetAt":"2026-11-10T09:00:00Z","resetGuessed":false,"detectedAt":"2026-11-10T06:00:00Z","updatedAt":"2026-11-10T06:30:00Z",
  "releasedAt":"2026-11-10T06:30:00Z","releaseReason":"account_changed","releasedWaiting":3}]}`

const ladoAccountLimitReleaseBody = `{"limit":{"accountKey":"claude:device-claude","provider":"claude","accountId":"device-claude","identityKnown":true,"active":false,
 "resetAt":"2026-11-10T10:00:00Z","resetGuessed":false,"detectedAt":"2026-11-10T07:00:00Z","updatedAt":"2026-11-10T08:00:00Z",
 "releasedAt":"2026-11-10T08:00:00Z","releaseReason":"manual","releasedWaiting":4},"released":true,"waiting":4}`

// ladoLimitsServer answers every request with status and body, and records
// the last request and its body.
func ladoLimitsServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	got := &http.Request{}
	gotBody := new([]byte)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotBody, _ = io.ReadAll(r.Body)
		*got = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got, gotBody
}

func TestLadoLimitsTable(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, req, _ := ladoLimitsServer(t, http.StatusOK, ladoAccountLimitsBody)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, aliveDeps(), "lado", "limits")
	if err != nil {
		t.Fatalf("limits: %v\nstderr=%s", err, errOut)
	}
	if req.Method != http.MethodGet || req.URL.Path != "/api/v1/lado/account-limits" {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	reset := ladoLimitTime(time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	released := ladoLimitTime(time.Date(2026, 11, 10, 6, 30, 0, 0, time.UTC))
	for _, want := range []string{
		"ACCOUNT", "claude:device-claude", "active", reset + " (guessed)",
		"codex:work", "released", released, "account_changed",
		"ao lado limits release <account> --reason <text>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("out has no %q:\n%s", want, out)
		}
	}
}

func TestLadoLimitsEmptyAndNoHint(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _, _ := ladoLimitsServer(t, http.StatusOK, `{"limits":[]}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "lado", "limits")
	if err != nil || !strings.Contains(out, "No account usage limits recorded.") {
		t.Fatalf("empty: err %v out %s", err, out)
	}
}

func TestLadoLimitsJSON(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		args   []string
	}{
		"list":    {http.StatusOK, ladoAccountLimitsBody, []string{"lado", "limits", "--json"}},
		"release": {http.StatusOK, ladoAccountLimitReleaseBody, []string{"lado", "limits", "release", "claude:device-claude", "--reason", "switched", "--json"}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, _, _ := ladoLimitsServer(t, tc.status, tc.body)
			writeRunFileFor(t, cfg, srv)
			out, errOut, err := executeCLI(t, aliveDeps(), tc.args...)
			if err != nil {
				t.Fatalf("--json: %v\nstderr=%s", err, errOut)
			}
			var got, want any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("out is not JSON: %v\n%s", err, out)
			}
			_ = json.Unmarshal([]byte(tc.body), &want)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("out = %s, want the daemon's answer %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestLadoLimitsRelease(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, req, body := ladoLimitsServer(t, http.StatusOK, ladoAccountLimitReleaseBody)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, aliveDeps(), "lado", "limits", "release", "claude:device-claude", "--reason", "  서브 계정으로 다시 로그인함 ")
	if err != nil {
		t.Fatalf("release: %v\nstderr=%s", err, errOut)
	}
	if req.Method != http.MethodPost || req.URL.Path != "/api/v1/lado/account-limits/claude:device-claude/release" {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	var sent releaseLadoAccountLimitRequest
	if err := json.Unmarshal(*body, &sent); err != nil || sent.Reason != "서브 계정으로 다시 로그인함" {
		t.Fatalf("body = %s (%v)", *body, err)
	}
	if !strings.Contains(out, "released the usage limit of claude:device-claude") || !strings.Contains(out, "4 account_limited waits") {
		t.Fatalf("out = %s", out)
	}
}

func TestLadoLimitsUsageErrors(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, req, _ := ladoLimitsServer(t, http.StatusOK, ladoAccountLimitReleaseBody)
	writeRunFileFor(t, cfg, srv)
	for _, args := range [][]string{
		{"lado", "limits", "extra"},
		{"lado", "limits", "release"},
		{"lado", "limits", "release", "--reason", "switched"},
		{"lado", "limits", "release", "claude:device-claude"},
		{"lado", "limits", "release", "claude:device-claude", "--reason", "  "},
		{"lado", "limits", "release", "claude:device-claude", "codex:work", "--reason", "switched"},
		{"lado", "limits", "release", "device-claude", "--reason", "switched"},
		{"lado", "limits", "release", "claude:device-claude", "--reason", strings.Repeat("가", 501)},
	} {
		_, _, err := executeCLI(t, aliveDeps(), args...)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args = %v, exit code = %d, want 2; err=%v", args, got, err)
		}
	}
	if req.Method != "" {
		t.Fatalf("a usage error reached the daemon: %s %s", req.Method, req.URL)
	}
}

func TestLadoLimitsReleaseSurfacesDaemonErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   []string
	}{
		"not found": {http.StatusNotFound, `{"error":"not_found","code":"LADO_ACCOUNT_LIMIT_NOT_FOUND","message":"LADO has no usage limit of this account","requestId":"req-4"}`,
			[]string{"LADO_ACCOUNT_LIMIT_NOT_FOUND", "no usage limit", "req-4"}},
		"not active": {http.StatusConflict, `{"error":"conflict","code":"LADO_ACCOUNT_LIMIT_NOT_ACTIVE","message":"The account's usage limit was already released or its reset passed","requestId":"req-9"}`,
			[]string{"LADO_ACCOUNT_LIMIT_NOT_ACTIVE", "already released", "req-9"}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, _, _ := ladoLimitsServer(t, tc.status, tc.body)
			writeRunFileFor(t, cfg, srv)
			_, _, err := executeCLI(t, aliveDeps(), "lado", "limits", "release", "claude:device-claude", "--reason", "switched")
			if got := ExitCode(err); got != 1 {
				t.Fatalf("exit code = %d, want 1; err=%v", got, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %q, want %q", err, want)
				}
			}
		})
	}
}
