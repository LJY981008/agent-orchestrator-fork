package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const ladoTaskTimingsBody = `{"taskId":"lado-task-1","issueRef":"github:acme/demo#12","title":"Add login",
"timeline":{"points":[
 {"stage":"received","at":"2026-10-19T09:00:00Z","sincePrevMs":0,"estimated":false},
 {"stage":"plan_requested","at":"2026-10-19T09:01:30Z","sincePrevMs":90000,"estimated":false},
 {"stage":"pr_opened","at":"2026-10-19T10:00:00Z","sincePrevMs":3510000,"estimated":true},
 {"stage":"merged","at":"2026-10-19T11:00:00Z","sincePrevMs":3600000,"estimated":false}],
 "totalMs":7200000,"planRequests":2,"revisions":3,"approvals":1,"runs":1},
"sessions":[
 {"sessionId":"demo-plan-1","role":"designer","toolCalls":12,"readCalls":7,"searchCalls":4,"commandCalls":1,"editCalls":0,"investigationCalls":11,"investigateMs":600000,"stored":true},
 {"sessionId":"demo-7","role":"worker","toolCalls":20,"readCalls":5,"searchCalls":3,"commandCalls":8,"editCalls":4,"investigationCalls":8,"investigateMs":9500,"stored":false}]}`

const ladoProjectTimingsBody = `{"projectId":"mer","tasks":[` + ladoTaskTimingsBody + `],
"gaps":[{"from":"received","to":"plan_requested","count":3,"medianMs":120000,"maxMs":3600000}],
"investigation":[{"role":"critic","sessions":4,"medianCalls":9,"medianInvestigationCalls":8,"medianInvestigateMs":300000}]}`

// ladoTimingsServer answers one GET and records its path and query.
func ladoTimingsServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	got := &http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		*got = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestLadoTimingsTaskTable(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, req := ladoTimingsServer(t, http.StatusOK, ladoTaskTimingsBody)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, aliveDeps(), "lado", "timings", "--task", "lado-task-1")
	if err != nil {
		t.Fatalf("timings: %v\nstderr=%s", err, errOut)
	}
	if req.Method != http.MethodGet || req.URL.Path != "/api/v1/lado/tasks/lado-task-1/timings" {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	for _, want := range []string{
		"lado-task-1  github:acme/demo#12  Add login",
		"Plan requests 2, revisions 3, approvals 1, runs 1. Total: 2h0m0s",
		"plan_requested", "1m30s",
		"2026-10-19T10:00:00Z (estimated)",
		"demo-plan-1", "designer", "10m0s", "stored",
		"demo-7", "computed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("out has no %q:\n%s", want, out)
		}
	}
}

func TestLadoTimingsProjectTable(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, req := ladoTimingsServer(t, http.StatusOK, ladoProjectTimingsBody)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, aliveDeps(), "lado", "timings", "--project", "mer", "--limit", "50")
	if err != nil {
		t.Fatalf("timings: %v\nstderr=%s", err, errOut)
	}
	if req.URL.Path != "/api/v1/lado/timings" || req.URL.Query().Get("projectId") != "mer" || req.URL.Query().Get("limit") != "50" {
		t.Fatalf("request = %s", req.URL)
	}
	for _, want := range []string{"Project mer: 1 tasks", "received", "plan_requested", "2m0s", "1h0m0s", "critic", "5m0s", "lado-task-1", "2h0m0s", "Add login"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out has no %q:\n%s", want, out)
		}
	}

	// Without --limit the daemon's default applies.
	if _, _, err := executeCLI(t, aliveDeps(), "lado", "timings", "--project", "mer"); err != nil || req.URL.Query().Has("limit") {
		t.Fatalf("no --limit: err %v, query %s", err, req.URL.RawQuery)
	}
}

func TestLadoTimingsJSON(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		args []string
	}{
		"task":    {ladoTaskTimingsBody, []string{"lado", "timings", "--task", "lado-task-1", "--json"}},
		"project": {ladoProjectTimingsBody, []string{"lado", "timings", "--project", "mer", "--json"}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, _ := ladoTimingsServer(t, http.StatusOK, tc.body)
			writeRunFileFor(t, cfg, srv)
			out, errOut, err := executeCLI(t, aliveDeps(), tc.args...)
			if err != nil {
				t.Fatalf("timings --json: %v\nstderr=%s", err, errOut)
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

func TestLadoTimingsUsageErrors(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{
		{"lado", "timings"},
		{"lado", "timings", "--task", "lado-task-1", "--project", "mer"},
		{"lado", "timings", "--task", " "},
		{"lado", "timings", "--task", "lado-task-1", "--limit", "5"},
		{"lado", "timings", "--project", "mer", "--limit", "0"},
		{"lado", "timings", "--project", "mer", "--limit", "101"},
	} {
		_, _, err := executeCLI(t, aliveDeps(), args...)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args = %v, exit code = %d, want 2; err=%v", args, got, err)
		}
	}
}

func TestLadoTimingsSurfacesDaemonError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := ladoTimingsServer(t, http.StatusNotFound, `{"error":"not_found","code":"LADO_TASK_NOT_FOUND","message":"Unknown task","requestId":"req-9"}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, aliveDeps(), "lado", "timings", "--task", "lado-task-9")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1; err=%v", got, err)
	}
	for _, want := range []string{"Unknown task", "LADO_TASK_NOT_FOUND", "req-9"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want %q", err, want)
		}
	}
}

// `ao lado` is no longer only for plan workers: its help says so.
func TestLadoHelpCoversEveryLadoCommand(t *testing.T) {
	setConfigEnv(t)
	out, _, err := executeCLI(t, aliveDeps(), "lado", "--help")
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if strings.Contains(out, "plan workers run") || !strings.Contains(out, "timings") {
		t.Fatalf("help = %s", out)
	}
}
