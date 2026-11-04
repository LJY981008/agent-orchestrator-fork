package cli

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeLadoGit answers the guard's git reads from fixed values, the same in
// every folder.
type fakeLadoGit struct {
	branch string
	config map[string][]string
	heads  map[string]string
}

func (f fakeLadoGit) CurrentBranch(string) string               { return f.branch }
func (f fakeLadoGit) Config(string) map[string][]string         { return f.config }
func (f fakeLadoGit) RemoteHead(_ string, remote string) string { return f.heads[remote] }

func TestLadoPushGuardDecidesCommands(t *testing.T) {
	work := fakeLadoGit{
		branch: "ao/x/root",
		config: map[string][]string{
			"branch.ao/x/root.remote": {"origin"},
			"branch.ao/x/root.merge":  {"refs/heads/main"},
		},
		heads: map[string]string{"origin": "develop", "upstream": "trunk"},
	}
	onMain := fakeLadoGit{branch: "main"}
	upstreamMode := work
	upstreamMode.config = map[string][]string{
		"push.default":            {"upstream"},
		"branch.ao/x/root.remote": {"origin"},
		"branch.ao/x/root.merge":  {"refs/heads/main"},
	}
	matching := fakeLadoGit{branch: "ao/x/root", config: map[string][]string{"push.default": {"matching"}}}
	pushRefspec := fakeLadoGit{branch: "ao/x/root", config: map[string][]string{"remote.origin.push": {"refs/heads/ao/x/root:refs/heads/main"}}}
	cases := []struct {
		command string
		git     fakeLadoGit
		base    []string
		deny    string // a word the reason holds; "" allows
	}{
		// Refused.
		{"git push origin HEAD:main", work, nil, "main"},
		{"git push --no-verify origin HEAD:main", work, nil, "main"},
		{"git push origin HEAD:refs/heads/main", work, nil, "main"},
		{"git push -f origin +HEAD:master", work, nil, "master"},
		{"git push origin main", work, nil, "main"},
		{"git push origin feature:release", work, []string{"release"}, "release"},
		{"git push origin HEAD:develop", work, nil, "develop"},
		{"git push upstream HEAD:trunk", work, nil, "trunk"},
		{"git push origin :main", work, nil, "main"},
		{"git push origin --delete main", work, nil, "main"},
		{"git push -d origin refs/heads/main", work, nil, "main"},
		{"git push --mirror origin", work, nil, "--mirror"},
		{"git push --all origin", work, nil, "--all"},
		{"git push origin :", work, nil, "every branch"},
		{"git push origin 'refs/heads/*:refs/heads/*'", work, nil, "main"},
		{"git push -o merge_request.merge_when_pipeline_succeeds origin HEAD:refs/heads/x", work, nil, "merge_when_pipeline_succeeds"},
		{"git push --push-option=merge_request.auto_merge origin HEAD:refs/heads/x", work, nil, "auto_merge"},
		{"git push -omerge_request.merge_when_pipeline_succeeds origin HEAD:refs/heads/x", work, nil, "merge_when_pipeline_succeeds"},
		{"gh pr merge 1", work, nil, "gh pr merge"},
		{"gh pr merge --squash --admin 12", work, nil, "gh pr merge"},
		{"gh api -X PUT repos/o/r/pulls/3/merge", work, nil, "gh api"},
		{"glab mr merge 1", work, nil, "glab mr merge"},
		{"glab api -X PUT projects/1/merge_requests/4/merge", work, nil, "glab api"},
		{`bash -c "git push origin HEAD:main"`, work, nil, "main"},
		{`sh -lc 'git push origin HEAD:main'`, work, nil, "main"},
		{`eval "git push origin HEAD:main"`, work, nil, "main"},
		{"echo $(git push origin HEAD:main)", work, nil, "main"},
		{"echo `git push origin HEAD:main`", work, nil, "main"},
		{"go test ./... && git push origin HEAD:main", work, nil, "main"},
		{"cd /repo; git push origin HEAD:main 2>&1 | tail -5", work, nil, "main"},
		{"GIT_TRACE=1 git -C /repo -c core.x=y push origin HEAD:main", work, nil, "main"},
		{"env FOO=1 timeout 60 /usr/bin/git push origin HEAD:main", work, nil, "main"},
		{"git push origin HEAD:main # done", work, nil, "main"},
		{"git push", onMain, nil, "main"},
		{"git push origin", onMain, nil, "main"},
		{"git push origin HEAD", onMain, nil, "main"},
		{"git push", upstreamMode, nil, "upstream"},
		{"git push origin ao/x/root", upstreamMode, nil, "main"},
		{"git -c push.default=upstream push", work, nil, "upstream"},
		{"git push", matching, nil, "matching"},
		{"git push", pushRefspec, nil, "remote.origin.push"},
		{`git push origin "HEAD:main`, work, nil, "could not be read"},
		{"git push origin HEAD:$(git branch --show-current)", onMain, nil, "main"},
		// Allowed.
		{"git push origin HEAD:refs/heads/ao/x/root", work, nil, ""},
		{"git push -u origin HEAD:refs/heads/ao/x/root", work, nil, ""},
		{"git push --force-with-lease origin HEAD:ao/x/root", work, nil, ""},
		{"git push -o merge_request.create origin HEAD:refs/heads/x", work, nil, ""},
		{"git push -o merge_request.create -o merge_request.target=main origin HEAD:refs/heads/x", work, nil, ""},
		{"git push origin HEAD:refs/heads/$(git branch --show-current)", work, nil, ""},
		{"git push origin HEAD", work, nil, ""},
		{"git push", work, nil, ""},
		{"git push --tags", work, nil, ""},
		{"git push origin v1.0", work, nil, ""},
		{"git push origin refs/tags/main", work, nil, ""},
		{"git push origin --delete ao/x/old", work, nil, ""},
		{"git log --oneline main..HEAD | grep push", work, nil, ""},
		{`echo "git push origin HEAD:main"`, work, nil, ""},
		{"git commit -m 'never git push origin HEAD:main'", work, nil, ""},
		{"git commit -F - <<'EOF'\nfix: x\n\ngit push origin HEAD:main\nEOF\ngit status", work, nil, ""},
		{"gh pr create --title t --body \"$(cat <<'EOF'\nDon't run git push origin HEAD:main (it's refused)\nEOF\n)\"", work, nil, ""},
		{"gh pr view 1 --json mergeable", work, nil, ""},
		{"gh pr create --title t --body b", work, nil, ""},
		{"glab mr create --fill", work, nil, ""},
		{"command -v git", work, nil, ""},
		{"git merge main", work, nil, ""},
		{"git push origin HEAD:$BRANCH", work, nil, ""},
		{"", work, nil, ""},
	}
	for _, tc := range cases {
		got := newLadoPushGuard(tc.git, tc.base).decide(tc.command, "/repo")
		if tc.deny == "" {
			if got != "" {
				t.Errorf("%q refused: %s", tc.command, got)
			}
			continue
		}
		if got == "" {
			t.Errorf("%q allowed, want refused", tc.command)
			continue
		}
		if !strings.Contains(got, tc.deny) {
			t.Errorf("%q: reason %q does not name %q", tc.command, got, tc.deny)
		}
		if !strings.Contains(got, "git push origin HEAD:refs/heads/") {
			t.Errorf("%q: reason %q does not say how to push the work branch", tc.command, got)
		}
	}
}

// The reason names the worker's own branch to push to, unless it is on a
// base branch.
func TestLadoPushGuardReasonNamesTheWorkBranch(t *testing.T) {
	got := newLadoPushGuard(fakeLadoGit{branch: "ao/x/root"}, nil).decide("git push origin HEAD:main", "/repo")
	if !strings.Contains(got, "git push origin HEAD:refs/heads/ao/x/root") {
		t.Fatalf("reason %q", got)
	}
	got = newLadoPushGuard(fakeLadoGit{branch: "main"}, nil).decide("git push", "/repo")
	if !strings.Contains(got, "HEAD:refs/heads/<your-work-branch>") {
		t.Fatalf("reason %q", got)
	}
}

func TestLadoBaseBranchesEnvRoundTrip(t *testing.T) {
	joined := domain.JoinLadoBaseBranches([]string{"", "develop", "auto", "refs/heads/main", "develop", " release "})
	if joined != "develop,main,release" {
		t.Fatalf("joined %q", joined)
	}
	got := domain.SplitLadoBaseBranches(joined + " trunk")
	if strings.Join(got, ",") != "develop,main,release,trunk" {
		t.Fatalf("split %q", got)
	}
}

// hookDecision runs AO's Claude Code hook for a Bash command and returns its
// output.
func hookDecision(t *testing.T, event, command, cwd string, worker bool) string {
	t.Helper()
	t.Setenv("AO_SESSION_ID", "demo-1")
	t.Setenv("AO_REVIEW_SESSION_ID", "")
	t.Setenv(domain.LadoPlanWorkerEnv, "")
	value := ""
	if worker {
		value = "1"
	}
	t.Setenv(domain.LadoWorkerEnv, value)
	t.Setenv(domain.LadoBaseBranchesEnv, "develop")
	cfg := setConfigEnv(t)
	srv, _ := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	payload, _ := json.Marshal(map[string]any{"tool_name": "Bash", "cwd": cwd, "tool_input": map[string]any{"command": command}})
	out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(string(payload)), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", event)
	if err != nil {
		t.Fatalf("hooks: %v\nstderr=%s", err, errOut)
	}
	return out
}

type hookDecisionOutput struct {
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

func decodeHookDecision(t *testing.T, out string) hookDecisionOutput {
	t.Helper()
	var res hookDecisionOutput
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return res
}

// A LADO execution worker's PreToolUse hook refuses a push to a base branch
// and a merge with the reason; it writes nothing for anything else, and
// another session's hook writes nothing at all.
func TestHooksRefuseALadoWorkersBasePush(t *testing.T) {
	cwd := t.TempDir()
	denied := []string{
		"git push origin HEAD:main",
		"git push --no-verify origin HEAD:main",
		"git push origin :main",
		"git push --mirror origin",
		"git push -o merge_request.merge_when_pipeline_succeeds origin HEAD:refs/heads/x",
		"gh pr merge 1",
		"glab mr merge 1",
		`bash -c "git push origin HEAD:main"`,
		"git push origin HEAD:develop",
	}
	for _, command := range denied {
		t.Run(command, func(t *testing.T) {
			res := decodeHookDecision(t, hookDecision(t, "pre-tool-use", command, cwd, true))
			if res.HookSpecificOutput.HookEventName != "PreToolUse" || res.HookSpecificOutput.PermissionDecision != "deny" {
				t.Fatalf("decision %+v", res.HookSpecificOutput)
			}
			if res.HookSpecificOutput.Reason == "" {
				t.Fatal("no reason")
			}
		})
	}
	res := decodeHookDecision(t, hookDecision(t, "pre-tool-use", "git push origin HEAD:main", cwd, true))
	if !strings.Contains(res.HookSpecificOutput.Reason, "main") {
		t.Fatalf("reason %q does not name main", res.HookSpecificOutput.Reason)
	}
	allowed := []string{
		"git push origin HEAD:refs/heads/ao/x/root",
		"git push -o merge_request.create origin HEAD:refs/heads/x",
		"go test ./...",
	}
	for _, command := range allowed {
		if out := hookDecision(t, "pre-tool-use", command, cwd, true); strings.TrimSpace(out) != "" {
			t.Errorf("%q: output %q, want none", command, out)
		}
	}
	if out := hookDecision(t, "pre-tool-use", "git push origin HEAD:main", cwd, false); strings.TrimSpace(out) != "" {
		t.Fatalf("a session that is no LADO worker got %q", out)
	}
}

// A permission prompt for a refused command is denied with the reason.
func TestHooksDenyALadoWorkersBasePushPrompt(t *testing.T) {
	cwd := t.TempDir()
	res := decodeHookDecision(t, hookDecision(t, "permission-request", "git push origin HEAD:main", cwd, true))
	if res.HookSpecificOutput.HookEventName != "PermissionRequest" || res.HookSpecificOutput.Decision.Behavior != "deny" ||
		!strings.Contains(res.HookSpecificOutput.Decision.Message, "main") {
		t.Fatalf("decision %+v", res.HookSpecificOutput)
	}
	if out := hookDecision(t, "permission-request", "git push origin HEAD:refs/heads/x", cwd, true); strings.TrimSpace(out) != "" {
		t.Fatalf("an allowed prompt got %q", out)
	}
}

// In a real repository the guard reads the checked-out branch and the push
// configuration: a bare git push on main is refused, on a work branch it is
// not, unless push.default sends it to a base branch upstream.
func TestHooksReadTheRepositoryForABarePush(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	res := decodeHookDecision(t, hookDecision(t, "pre-tool-use", "git push", repo, true))
	if res.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(res.HookSpecificOutput.Reason, "main") {
		t.Fatalf("git push on main: %+v", res.HookSpecificOutput)
	}
	git("checkout", "-q", "-b", "ao/x/root")
	git("config", "branch.ao/x/root.remote", "origin")
	git("config", "branch.ao/x/root.merge", "refs/heads/main")
	if out := hookDecision(t, "pre-tool-use", "git push", repo, true); strings.TrimSpace(out) != "" {
		t.Fatalf("git push on a work branch got %q", out)
	}
	git("config", "push.default", "upstream")
	res = decodeHookDecision(t, hookDecision(t, "pre-tool-use", "git push", repo, true))
	if res.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(res.HookSpecificOutput.Reason, "upstream") {
		t.Fatalf("git push to an upstream main: %+v", res.HookSpecificOutput)
	}
	if !strings.Contains(res.HookSpecificOutput.Reason, "HEAD:refs/heads/ao/x/root") {
		t.Fatalf("reason %q does not name the work branch", res.HookSpecificOutput.Reason)
	}
	// cd into the repository from elsewhere is followed too.
	git("config", "push.default", "simple")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	res = decodeHookDecision(t, hookDecision(t, "pre-tool-use", "cd "+repo+" && git push", t.TempDir(), true))
	if res.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("cd then git push on main: %+v", res.HookSpecificOutput)
	}
}
