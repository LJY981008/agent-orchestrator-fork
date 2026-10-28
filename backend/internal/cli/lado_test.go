package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A plan worker's tool calls are decided without a person: reading passes,
// and every write, command and anything a check cannot read is refused.
func TestLadoPlanWorkerCommandDecision(t *testing.T) {
	heredoc := "ao lado plan submit --request lado-plan-request-1 --draft <<'LADO_PLAN_EOF'\n---\ntask: x\n---\n\n$(rm -rf /) `id` > out.txt\nLADO_PLAN_EOF"
	allowed := []string{
		"git log --oneline -20",
		"git --no-pager log --format='%h %s' -5",
		"git -C backend diff HEAD~1 -- internal/domain",
		"git show HEAD:README.md",
		"git status --short",
		"git ls-files 'backend/*.go'",
		"git ls-tree --name-only origin/main backend/",
		"git log --oneline | head -20",
		"git diff --stat | grep lado | wc -l",
		"gh issue view 27 --repo acme/demo --comments",
		"gh pr view 12 --json files",
		"gh pr diff 12",
		heredoc,
		"ao lado plan submit --request lado-plan-request-1 <<'EOF'\nplan\nEOF",
		"ao lado plan critique submit --request lado-plan-request-1 <<'LADO_CRITIQUE_EOF'\n{\"findings\":[]}\nLADO_CRITIQUE_EOF",
		"printf '%s' '{\"findings\":[{\"title\":\"it'\\''s wrong\"}]}' | ao lado plan critique submit --request lado-plan-request-1",
	}
	denied := []string{
		"git push origin HEAD",
		"git commit -m wip",
		"git -c core.pager=sh log",
		"git diff --output=/tmp/x",
		"git log --ext-diff",
		"git log; rm -rf /",
		"git log && echo hi",
		"git log || true",
		"git log > out.txt",
		"git log 2>&1",
		"git log $(rm x)",
		"git log `id`",
		"git log \"$HOME\"",
		"git log ~/x",
		"git ls-files *.go",
		"git log | sort -o out.txt",
		"git log | tee out.txt",
		"gh pr create --fill",
		"gh pr merge 12",
		"gh api repos/acme/demo/pulls",
		"npm install",
		"cat README.md",
		"echo hi > notes.md",
		"rm -rf backend",
		"ao spawn --prompt x",
		"ao send demo-1 hi",
		"ao lado plan submit --request lado-plan-request-1 <<EOF\nplan\nEOF",
		"ao lado plan submit --request lado-plan-request-1 <<-'EOF'\nplan\nEOF",
		"ao lado plan submit --request lado-plan-request-1 <<'EOF'\nplan\nEOF\nrm -rf /",
		"ao lado plan submit --request lado-plan-request-1 <<'EOF'\nplan\nEOF\nmore\nEOF",
		"ao lado plan submit --request lado-plan-request-1 <<'EOF' && rm x\nplan\nEOF",
		"cat > /tmp/p.md <<'EOF'\nplan\nEOF",
		"printf '%s' 'x'$(id) | ao lado plan submit --request lado-plan-request-1",
		"",
	}
	for _, command := range allowed {
		if !ladoPlanWorkerCommand(command) {
			t.Errorf("refused %q", command)
		}
	}
	for _, command := range denied {
		if ladoPlanWorkerCommand(command) {
			t.Errorf("allowed %q", command)
		}
	}
	for tool, want := range map[string]bool{"Read": true, "Grep": true, "Glob": true, "TodoWrite": true, "Edit": false, "Write": false, "NotebookEdit": false, "MultiEdit": false, "WebFetch": false, "Task": false, "mcp__ao__preview": false} {
		payload, _ := json.Marshal(map[string]any{"tool_name": tool, "tool_input": map[string]any{"file_path": "/repo/x.go"}})
		if got := ladoPlanWorkerToolDecision(payload); got != want {
			t.Errorf("%s = %v, want %v", tool, got, want)
		}
	}
}

// A plan worker's Claude Code hooks answer PreToolUse with allow or deny and
// keep reporting its activity; a permission prompt is denied at once and
// reports nothing. Another session's hooks decide nothing.
func TestHooksAnswerAPlanWorkersToolCalls(t *testing.T) {
	cases := []struct {
		name, event, payload string
		planWorker           bool
		want                 string
		reports              bool
	}{
		{"edit refused", "pre-tool-use", `{"tool_name":"Edit","tool_input":{"file_path":"a.go"}}`, true, "deny", true},
		{"read allowed", "pre-tool-use", `{"tool_name":"Read","tool_input":{"file_path":"a.go"}}`, true, "allow", true},
		{"push refused", "pre-tool-use", `{"tool_name":"Bash","tool_input":{"command":"git push"}}`, true, "deny", true},
		{"prompt refused", "permission-request", `{"tool_name":"Write","tool_input":{"file_path":"a.go"}}`, true, "deny", false},
		{"another session", "pre-tool-use", `{"tool_name":"Edit","tool_input":{"file_path":"a.go"}}`, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "demo-plan-1")
			t.Setenv("AO_REVIEW_SESSION_ID", "")
			value := ""
			if tc.planWorker {
				value = "1"
			}
			t.Setenv(domain.LadoPlanWorkerEnv, value)
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)
			out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(tc.payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", tc.event)
			if err != nil {
				t.Fatalf("hooks: %v\nstderr=%s", err, errOut)
			}
			if (capture.hits > 0) != tc.reports {
				t.Fatalf("activity reported = %v, want %v", capture.hits > 0, tc.reports)
			}
			if tc.want == "" {
				if strings.Contains(out, "permissionDecision") || strings.Contains(out, "behavior") {
					t.Fatalf("another session got a decision: %s", out)
				}
				return
			}
			var res struct {
				HookSpecificOutput struct {
					HookEventName      string `json:"hookEventName"`
					PermissionDecision string `json:"permissionDecision"`
					Reason             string `json:"permissionDecisionReason"`
					Decision           struct {
						Behavior string `json:"behavior"`
						Message  string `json:"message"`
					} `json:"decision"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatalf("decode %q: %v", out, err)
			}
			got := res.HookSpecificOutput.PermissionDecision
			if tc.event == "permission-request" {
				got = res.HookSpecificOutput.Decision.Behavior
				if res.HookSpecificOutput.HookEventName != "PermissionRequest" || res.HookSpecificOutput.Decision.Message == "" {
					t.Fatalf("output = %s", out)
				}
			} else if res.HookSpecificOutput.HookEventName != "PreToolUse" || res.HookSpecificOutput.Reason == "" {
				t.Fatalf("output = %s", out)
			}
			if got != tc.want {
				t.Fatalf("decision = %q, want %q (%s)", got, tc.want, out)
			}
		})
	}
}

// ladoServer answers the submission routes and records the last request.
func ladoServer(t *testing.T, status int, respBody string) (*httptest.Server, *activityCapture) {
	t.Helper()
	capture := &activityCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capture.body, capture.path = string(body), r.URL.Path
		capture.hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func TestLadoPlanSubmitSendsTheFileAsTheSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-plan-1")
	cfg := setConfigEnv(t)
	srv, capture := ladoServer(t, http.StatusOK, `{"id":"lado-plan-request-1","phase":"critiquing"}`)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader("---\ntask: x\n---\nPlan.\n"), ProcessAlive: func(int) bool { return true }},
		"lado", "plan", "submit", "--request", "lado-plan-request-1", "--draft")
	if err != nil {
		t.Fatalf("submit: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/lado/plan-requests/lado-plan-request-1/submit" {
		t.Fatalf("path = %q", capture.path)
	}
	var body submitLadoPlanRequest
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil || body.SessionID != "demo-plan-1" || !body.Draft || body.Content != "---\ntask: x\n---\nPlan.\n" {
		t.Fatalf("body = %+v, %v", body, err)
	}
	if !strings.Contains(out, "took the draft of lado-plan-request-1") {
		t.Fatalf("out = %q", out)
	}
}

func TestLadoCritiqueSubmitSendsTheFindings(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-plan-2")
	cfg := setConfigEnv(t)
	srv, capture := ladoServer(t, http.StatusOK, `{"id":"lado-critique-1"}`)
	writeRunFileFor(t, cfg, srv)
	in := `{"findings":[{"id":"C1","severity":"P1","title":"Order","failure_scenario":"breaks"}]}`
	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(in), ProcessAlive: func(int) bool { return true }},
		"lado", "plan", "critique", "submit", "--request", "lado-plan-request-1")
	if err != nil {
		t.Fatalf("critique submit: %v\nstderr=%s", err, errOut)
	}
	var body submitLadoCritiqueRequest
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil || capture.path != "/api/v1/lado/plan-requests/lado-plan-request-1/critique" ||
		body.SessionID != "demo-plan-2" || len(body.Findings) != 1 || body.Findings[0].FailureScenario != "breaks" {
		t.Fatalf("path %q, body %+v, %v", capture.path, body, err)
	}
	if !strings.Contains(out, "took 1 findings") {
		t.Fatalf("out = %q", out)
	}
}

func TestLadoSubmitUsageErrors(t *testing.T) {
	cases := []struct {
		name    string
		session string
		in      string
		args    []string
	}{
		{"no request", "demo-plan-1", "plan", []string{"lado", "plan", "submit"}},
		{"outside a session", "", "plan", []string{"lado", "plan", "submit", "--request", "r"}},
		{"empty stdin", "demo-plan-1", " \n", []string{"lado", "plan", "submit", "--request", "r"}},
		{"findings not JSON", "demo-plan-1", "C1 is bad", []string{"lado", "plan", "critique", "submit", "--request", "r"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", tc.session)
			setConfigEnv(t)
			_, _, err := executeCLI(t, Deps{In: strings.NewReader(tc.in), ProcessAlive: func(int) bool { return true }}, tc.args...)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("err = %v, want a usage error", err)
			}
		})
	}
}
