package ladoverify

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// headCheckRows builds the CI checks of a head from name and status pairs.
func headCheckRows(pairs ...string) []domain.PullRequestCheck {
	var checks []domain.PullRequestCheck
	for i := 0; i+1 < len(pairs); i += 2 {
		checks = append(checks, domain.PullRequestCheck{Name: pairs[i], CommitHash: strings.Repeat("a", 40), Status: domain.PRCheckStatus(pairs[i+1])})
	}
	return checks
}

func requiredChecksDetailsOf(t *testing.T, item domain.LadoVerifyItem) domain.LadoRequiredChecksDetails {
	t.Helper()
	var details domain.LadoRequiredChecksDetails
	if err := json.Unmarshal(item.Details, &details); err != nil {
		t.Fatalf("required_checks details %s: %v", item.Details, err)
	}
	return details
}

func expectItemOutcome(t *testing.T, item domain.LadoVerifyItem, kind domain.LadoVerifyKind, outcome domain.LadoVerifyOutcome, code string) {
	t.Helper()
	if item.Kind != kind || item.Outcome != outcome || item.Code != code {
		t.Fatalf("item = %s %s %q, want %s %s %q (details %s)", item.Kind, item.Outcome, item.Code, kind, outcome, code, item.Details)
	}
}

// Gate 6: the worker's report is a claim. LADO reads the head's checks, and a
// failing required check keeps the task Validating.
func TestFailingRequiredCheckKeepsValidatingDespiteReport(t *testing.T) {
	const prURL = "https://github.com/acme/demo/pull/7"
	head := strings.Repeat("a", 40)
	// The worker reported done (ao report --done) and said its tests pass.
	// Nothing LADO verifies takes that report: only the head's records count.
	report := domain.ReportRecord{SessionID: "lado-1", State: domain.ReportDone, Message: "Done: all tests pass."}

	required, named := RequiredContexts(protectionRules(protectionAllTypes, "build", domain.LadoIntegrationStatusContext))
	checks, err := EvaluateRequiredChecks(required, named, true, headCheckRows("build", "failed", "lint", "passed"))
	if err != nil {
		t.Fatal(err)
	}
	expectItemOutcome(t, checks, domain.LadoVerifyRequiredChecks, domain.LadoVerifyFail, domain.LadoCodeChecksFailed)
	commands, err := EvaluateCommands(CommandsInput{RequiredNamed: named})
	if err != nil {
		t.Fatal(err)
	}
	expectItemOutcome(t, commands, domain.LadoVerifyCommands, domain.LadoVerifyPass, domain.LadoCodeNoneConfigured)
	review, _, err := EvaluateReview(ReviewInput{})
	if err != nil {
		t.Fatal(err)
	}

	record := domain.LadoVerification{ExecutionSessionID: report.SessionID, HeadSHA: head, PRURL: prURL, Items: []domain.LadoVerifyItem{checks, commands, review}, ComputedAt: t0}
	outcome, reason := record.Overall()
	if outcome != domain.LadoVerifyFail || reason != domain.LadoCodeChecksFailed {
		t.Fatalf("Overall() = %s %q, want fail checks_failed", outcome, reason)
	}
	if ReviewReady(record.Items) {
		t.Fatal("ReviewReady = true with a failing required check")
	}

	task := domain.LadoTask{Execution: domain.LadoExecution{
		SessionID:    report.SessionID,
		StartedAt:    t0,
		PR:           domain.LadoExecutionPR{URL: prURL, Number: 7, HeadSHA: head},
		Verification: domain.LadoExecutionVerification{HeadSHA: head, Outcome: outcome, Reason: reason},
	}}
	if state := task.StateAt(t0.Add(time.Hour)); state != domain.LadoTaskValidating {
		t.Fatalf("task state = %s, want validating", state)
	}
	if got := task.StateReason(); got != domain.LadoCodeChecksFailed {
		t.Fatalf("task reason = %q, want checks_failed", got)
	}
}

func TestRequiredChecksFromTheRuleset(t *testing.T) {
	// The ruleset requires build and Test, and lado/integration, which LADO
	// posts itself after the approval.
	required, named := RequiredContexts(protectionRules(protectionAllTypes, "build", domain.LadoIntegrationStatusContext, "Test"))
	if !named || !reflect.DeepEqual(required, []string{"build", "Test"}) {
		t.Fatalf("RequiredContexts = %q, %v; want [build Test], true", required, named)
	}
	cases := []struct {
		name    string
		checks  []domain.PullRequestCheck
		outcome domain.LadoVerifyOutcome
		code    string
	}{
		{"required checks pass or skip, names without case", headCheckRows("build", "passed", "test", "skipped"), domain.LadoVerifyPass, ""},
		{"a check that is not required fails", headCheckRows("lint", "failed", "build", "passed", "Test", "passed"), domain.LadoVerifyPass, ""},
		{"a required check is missing", headCheckRows("build", "passed", "lint", "passed"), domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"a required check runs", headCheckRows("build", "in_progress", "Test", "passed"), domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"a required check is queued", headCheckRows("build", "queued", "Test", "passed"), domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"a required check is unknown", headCheckRows("build", "passed", "Test", "unknown"), domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"no check recorded yet", nil, domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"a required check failed", headCheckRows("build", "failed", "Test", "passed"), domain.LadoVerifyFail, domain.LadoCodeChecksFailed},
		{"a required check was cancelled", headCheckRows("build", "passed", "Test", "cancelled"), domain.LadoVerifyFail, domain.LadoCodeChecksFailed},
		{"a failure wins over a missing check", headCheckRows("build", "failed"), domain.LadoVerifyFail, domain.LadoCodeChecksFailed},
		{"one of two rows of a name failed", headCheckRows("build", "passed", "Build", "failed", "Test", "passed"), domain.LadoVerifyFail, domain.LadoCodeChecksFailed},
		{"a pending lado/integration does not count", headCheckRows("build", "passed", "Test", "passed", "LADO/integration", "queued"), domain.LadoVerifyPass, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item, err := EvaluateRequiredChecks(required, named, true, tc.checks)
			if err != nil {
				t.Fatal(err)
			}
			expectItemOutcome(t, item, domain.LadoVerifyRequiredChecks, tc.outcome, tc.code)
			details := requiredChecksDetailsOf(t, item)
			if !reflect.DeepEqual(details.Required, []string{"build", "Test"}) || !details.RulesetRead {
				t.Fatalf("details = %+v, want required [build Test] read", details)
			}
			for _, check := range details.Checks {
				if strings.EqualFold(check.Name, domain.LadoIntegrationStatusContext) {
					t.Fatalf("details list %s: %+v", check.Name, details.Checks)
				}
				if want := !strings.EqualFold(check.Name, "lint"); check.Required != want {
					t.Fatalf("check %s required = %v, want %v", check.Name, check.Required, want)
				}
			}
		})
	}

	// The details list every other check of the head by name, the failure
	// that is not required among them.
	item, err := EvaluateRequiredChecks(required, named, true, headCheckRows("lint", "failed", "build", "passed", domain.LadoIntegrationStatusContext, "passed", "Test", "skipped"))
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.LadoVerifyCheck{
		{Name: "Test", Status: "skipped", Required: true},
		{Name: "build", Status: "passed", Required: true},
		{Name: "lint", Status: "failed", Required: false},
	}
	if got := requiredChecksDetailsOf(t, item).Checks; !reflect.DeepEqual(got, want) {
		t.Fatalf("details checks = %+v, want %+v", got, want)
	}
}

func TestRequiredChecksWithoutARuleset(t *testing.T) {
	// Rules that name no check, as for a branch without a ruleset.
	required, named := RequiredContexts(protectionRules([]string{ports.LadoBranchRulePullRequest}))
	if named || len(required) != 0 {
		t.Fatalf("RequiredContexts = %q, %v; want none", required, named)
	}
	cases := []struct {
		name    string
		checks  []domain.PullRequestCheck
		outcome domain.LadoVerifyOutcome
		code    string
	}{
		{"no check recorded", nil, domain.LadoVerifyPass, domain.LadoCodeNoneReported},
		{"only LADO's own status", headCheckRows(domain.LadoIntegrationStatusContext, "passed"), domain.LadoVerifyPass, domain.LadoCodeNoneReported},
		{"every check passed or skipped", headCheckRows("build", "passed", "docs", "skipped"), domain.LadoVerifyPass, ""},
		{"a check runs", headCheckRows("build", "passed", "e2e", "in_progress"), domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"a check is queued", headCheckRows("e2e", "queued"), domain.LadoVerifyWait, domain.LadoCodeChecksPending},
		{"a check failed while another runs", headCheckRows("build", "failed", "e2e", "in_progress"), domain.LadoVerifyFail, domain.LadoCodeChecksFailed},
		{"a check was cancelled", headCheckRows("build", "cancelled"), domain.LadoVerifyFail, domain.LadoCodeChecksFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, read := range []bool{true, false} {
				item, err := EvaluateRequiredChecks(required, named, read, tc.checks)
				if err != nil {
					t.Fatal(err)
				}
				expectItemOutcome(t, item, domain.LadoVerifyRequiredChecks, tc.outcome, tc.code)
				details := requiredChecksDetailsOf(t, item)
				if len(details.Required) != 0 || details.RulesetRead != read {
					t.Fatalf("details = %+v, want no required checks and rulesetRead %v", details, read)
				}
				for _, check := range details.Checks {
					if !check.Required {
						t.Fatalf("check %s is not required; without named checks every check is", check.Name)
					}
				}
			}
		})
	}
}

// checkRunAt is a run of a check command on the head, finished a minute
// after it started unless it still runs.
// withCommand is run as a run of another text of its command.
func withCommand(run domain.LadoCheckRun, command string) domain.LadoCheckRun {
	run.Command = command
	return run
}

func checkRunAt(id, name string, status domain.LadoCheckRunStatus, started time.Time, exit int) domain.LadoCheckRun {
	run := domain.LadoCheckRun{ID: id, ExecutionSessionID: "lado-1", HeadSHA: strings.Repeat("a", 40), Name: name, Command: "make " + name, Status: status, StartedAt: started}
	if status != domain.LadoCheckRunRunning {
		run.FinishedAt = started.Add(time.Minute)
	}
	if status == domain.LadoCheckRunPassed || status == domain.LadoCheckRunFailed {
		run.ExitCode = &exit
	}
	return run
}

func TestCommandsItem(t *testing.T) {
	commands := []domain.LadoCheckCommand{{Name: "unit", Command: "make unit"}, {Name: "lint", Command: "make lint"}}
	at := func(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }
	passed := func(id, name string, minutes int) domain.LadoCheckRun {
		return checkRunAt(id, name, domain.LadoCheckRunPassed, at(minutes), 0)
	}
	cases := []struct {
		name     string
		commands []domain.LadoCheckCommand
		runs     []domain.LadoCheckRun
		named    bool
		worktree string
		outcome  domain.LadoVerifyOutcome
		code     string
		toRun    []string
	}{
		{name: "no commands, required checks named", named: true, outcome: domain.LadoVerifyPass, code: domain.LadoCodeNoneConfigured},
		{name: "no commands and no required checks", outcome: domain.LadoVerifyConfirm, code: domain.LadoCodeNoTests},
		{name: "not run, worktree ready", commands: commands, outcome: domain.LadoVerifyWait, code: domain.LadoCodeCommandsRunning, toRun: []string{"unit", "lint"}},
		{name: "not run, worktree not at the head", commands: commands, worktree: domain.LadoCodeWorktreeNotAtHead, outcome: domain.LadoVerifyWait, code: domain.LadoCodeWorktreeNotAtHead, toRun: []string{"unit", "lint"}},
		{name: "not run, no worktree", commands: commands, worktree: domain.LadoCodeWorktreeMissing, outcome: domain.LadoVerifyFail, code: domain.LadoCodeWorktreeMissing, toRun: []string{"unit", "lint"}},
		{name: "first runs, second waits", commands: commands, runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", domain.LadoCheckRunRunning, at(0), 0)}, outcome: domain.LadoVerifyWait, code: domain.LadoCodeCommandsRunning, toRun: []string{"lint"}},
		{name: "first passed, second waits for the worktree", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0)}, worktree: domain.LadoCodeWorktreeNotAtHead, outcome: domain.LadoVerifyWait, code: domain.LadoCodeWorktreeNotAtHead, toRun: []string{"lint"}},
		{name: "all passed", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0), passed("r2", "lint", 2)}, outcome: domain.LadoVerifyPass},
		{name: "all passed, worktree gone since", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0), passed("r2", "lint", 2)}, worktree: domain.LadoCodeWorktreeMissing, outcome: domain.LadoVerifyPass},
		{name: "a command failed", commands: commands, runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", domain.LadoCheckRunFailed, at(0), 2), passed("r2", "lint", 2)}, outcome: domain.LadoVerifyFail, code: domain.LadoCodeCommandsFailed},
		{name: "a command did not start", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0), checkRunAt("r2", "lint", domain.LadoCheckRunError, at(2), 0)}, outcome: domain.LadoVerifyFail, code: domain.LadoCodeCommandsFailed},
		{name: "a command timed out", commands: commands, runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", domain.LadoCheckRunTimedOut, at(0), 0), passed("r2", "lint", 2)}, outcome: domain.LadoVerifyFail, code: domain.LadoCodeCommandTimedOut},
		{name: "a cancelled run runs again", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0), checkRunAt("r2", "lint", domain.LadoCheckRunCancelled, at(2), 0)}, outcome: domain.LadoVerifyWait, code: domain.LadoCodeCommandsRunning, toRun: []string{"lint"}},
		{name: "a cancelled run waits for the worktree", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0), checkRunAt("r2", "lint", domain.LadoCheckRunCancelled, at(2), 0)}, worktree: domain.LadoCodeWorktreeNotAtHead, outcome: domain.LadoVerifyWait, code: domain.LadoCodeWorktreeNotAtHead, toRun: []string{"lint"}},
		{name: "the newest run counts: passed after a failure", commands: commands, runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", domain.LadoCheckRunFailed, at(0), 1), passed("r2", "lint", 2), passed("r3", "unit", 4)}, outcome: domain.LadoVerifyPass},
		{name: "the newest run counts: cancelled after a pass", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0), passed("r2", "lint", 2), checkRunAt("r3", "unit", domain.LadoCheckRunCancelled, at(4), 0)}, outcome: domain.LadoVerifyWait, code: domain.LadoCodeCommandsRunning, toRun: []string{"unit"}},
		{name: "a failure wins over a running command", commands: commands, runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", domain.LadoCheckRunRunning, at(0), 0), checkRunAt("r2", "lint", domain.LadoCheckRunFailed, at(0), 1)}, outcome: domain.LadoVerifyFail, code: domain.LadoCodeCommandsFailed},
		{name: "a failure wins over the worktree wait", commands: commands, runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", domain.LadoCheckRunFailed, at(0), 1)}, worktree: domain.LadoCodeWorktreeNotAtHead, outcome: domain.LadoVerifyFail, code: domain.LadoCodeCommandsFailed, toRun: []string{"lint"}},
		{name: "the first failing command names the code", commands: commands, runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", domain.LadoCheckRunTimedOut, at(0), 0), checkRunAt("r2", "lint", domain.LadoCheckRunFailed, at(2), 1)}, outcome: domain.LadoVerifyFail, code: domain.LadoCodeCommandTimedOut},
		{name: "runs of a command no longer configured do not count", commands: commands[:1], runs: []domain.LadoCheckRun{passed("r1", "unit", 0), checkRunAt("r2", "lint", domain.LadoCheckRunFailed, at(2), 1)}, outcome: domain.LadoVerifyPass},
		// V1 of #15: a run counts for a command by its name and its text, so
		// a pass of the command before its text changed is not this command's.
		{name: "a pass of the command's old text does not count", commands: commands, runs: []domain.LadoCheckRun{withCommand(passed("r1", "unit", 0), "make unit-fast"), passed("r2", "lint", 2)}, outcome: domain.LadoVerifyWait, code: domain.LadoCodeCommandsRunning, toRun: []string{"unit"}},
		{name: "a failure of the command's old text does not count", commands: commands, runs: []domain.LadoCheckRun{withCommand(checkRunAt("r1", "unit", domain.LadoCheckRunFailed, at(0), 1), "make unit-old"), passed("r2", "unit", 1), passed("r3", "lint", 2)}, outcome: domain.LadoVerifyPass},
		{name: "the worktree changed since the last run", commands: commands, runs: []domain.LadoCheckRun{passed("r1", "unit", 0)}, worktree: domain.LadoCodeWorktreeChanged, outcome: domain.LadoVerifyWait, code: domain.LadoCodeWorktreeChanged, toRun: []string{"lint"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item, err := EvaluateCommands(CommandsInput{Commands: tc.commands, Runs: tc.runs, RequiredNamed: tc.named, WorktreeCode: tc.worktree})
			if err != nil {
				t.Fatal(err)
			}
			expectItemOutcome(t, item, domain.LadoVerifyCommands, tc.outcome, tc.code)
			var toRun []string
			for _, command := range CommandsToRun(tc.commands, tc.runs) {
				toRun = append(toRun, command.Name)
			}
			if !reflect.DeepEqual(toRun, tc.toRun) {
				t.Fatalf("CommandsToRun = %q, want %q", toRun, tc.toRun)
			}
		})
	}

	// The details list each command with its newest run.
	item, err := EvaluateCommands(CommandsInput{Commands: commands, Runs: []domain.LadoCheckRun{
		checkRunAt("r1", "unit", domain.LadoCheckRunFailed, at(0), 2),
		checkRunAt("r2", "unit", domain.LadoCheckRunRunning, at(3), 0),
	}})
	if err != nil {
		t.Fatal(err)
	}
	unitSHA, lintSHA := domain.LadoCheckCommandSHA256("make unit"), domain.LadoCheckCommandSHA256("make lint")
	if want := `{"runs":[{"name":"unit","commandSha256":"` + unitSHA + `","checkRunId":"r2","status":"running","exitCode":null,"durationMs":0},{"name":"lint","commandSha256":"` + lintSHA + `","checkRunId":"","status":"","exitCode":null,"durationMs":0}]}`; string(item.Details) != want {
		t.Fatalf("details = %s\nwant %s", item.Details, want)
	}
	// The hash of a command's text is in the details, so changing the text
	// changes the record's hash even before the command runs again.
	changed, err := EvaluateCommands(CommandsInput{Commands: []domain.LadoCheckCommand{{Name: "unit", Command: "make unit-all"}, commands[1]}, Runs: []domain.LadoCheckRun{
		checkRunAt("r1", "unit", domain.LadoCheckRunFailed, at(0), 2),
		checkRunAt("r2", "unit", domain.LadoCheckRunRunning, at(3), 0),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(changed.Details) == string(item.Details) {
		t.Fatalf("details of a changed command = %s, want them to differ", changed.Details)
	}
	item, err = EvaluateCommands(CommandsInput{Commands: commands, Runs: []domain.LadoCheckRun{
		checkRunAt("r1", "unit", domain.LadoCheckRunFailed, at(0), 2), passed("r2", "lint", 1),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"runs":[{"name":"unit","commandSha256":"` + unitSHA + `","checkRunId":"r1","status":"failed","exitCode":2,"durationMs":60000},{"name":"lint","commandSha256":"` + lintSHA + `","checkRunId":"r2","status":"passed","exitCode":0,"durationMs":60000}]}`; string(item.Details) != want {
		t.Fatalf("details = %s\nwant %s", item.Details, want)
	}

	// A worktree code or a run state LADO does not know is an error.
	if _, err := EvaluateCommands(CommandsInput{Commands: commands, WorktreeCode: "dirty"}); err == nil {
		t.Fatal("EvaluateCommands took an unknown worktree code")
	}
	if _, err := EvaluateCommands(CommandsInput{Commands: commands, Runs: []domain.LadoCheckRun{checkRunAt("r1", "unit", "paused", at(0), 0)}}); err == nil {
		t.Fatal("EvaluateCommands took an unknown run state")
	}
}
