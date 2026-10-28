package ladoverify

// LADO fork: the required_checks and commands items (decisions d and e of
// the step 8 plan; docs/LJY-PRD/TDD.md §8.2, §8.4). A worker's report that
// it is done is a claim: these items read only what the CI checks and the
// project's check commands recorded for the PR's head, so a failing required
// check keeps the task Validating whatever the worker said (PRD gate 6).

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// EvaluateRequiredChecks computes the required_checks item from the CI
// checks AO recorded for the head. When the base branch's rules name
// required checks (required and named, as RequiredContexts returns them),
// each name needs checks, compared without case, that passed or were
// skipped: a missing one, or one unknown, queued or in progress, waits as
// checks_pending, and a failed or cancelled one fails as checks_failed. When
// they name none, every recorded check counts the same way, and a head
// without any passes as none_reported. A failure wins over a wait.
// rulesetRead says whether the rules were read this time; without a read,
// required comes from the last good one.
//
// lado/integration never counts and stays out of the details: LADO posts it
// after the approval. The details list the other checks by name, marking
// those the item counted as required.
func EvaluateRequiredChecks(required []string, named, rulesetRead bool, checks []domain.PullRequestCheck) (domain.LadoVerifyItem, error) {
	details := domain.LadoRequiredChecksDetails{Required: required, RulesetRead: rulesetRead}
	isRequired := func(name string) bool {
		return !named || slices.ContainsFunc(required, func(r string) bool { return strings.EqualFold(r, name) })
	}
	failed, waiting := false, false
	for _, check := range checks {
		if strings.EqualFold(check.Name, domain.LadoIntegrationStatusContext) {
			continue
		}
		counted := isRequired(check.Name)
		details.Checks = append(details.Checks, domain.LadoVerifyCheck{Name: check.Name, Status: string(check.Status), Required: counted})
		if !counted {
			continue
		}
		switch requiredCheckOutcome(check.Status) {
		case domain.LadoVerifyFail:
			failed = true
		case domain.LadoVerifyWait:
			waiting = true
		}
	}
	if named {
		for _, name := range required {
			if !slices.ContainsFunc(details.Checks, func(c domain.LadoVerifyCheck) bool { return strings.EqualFold(c.Name, name) }) {
				waiting = true
			}
		}
	}
	// The record's hash covers the details: list the checks in one order
	// whatever order they were read in.
	slices.SortStableFunc(details.Checks, func(a, b domain.LadoVerifyCheck) int { return strings.Compare(a.Name, b.Name) })

	outcome, code := domain.LadoVerifyPass, ""
	switch {
	case failed:
		outcome, code = domain.LadoVerifyFail, domain.LadoCodeChecksFailed
	case waiting:
		outcome, code = domain.LadoVerifyWait, domain.LadoCodeChecksPending
	case !named && len(details.Checks) == 0:
		code = domain.LadoCodeNoneReported
	}
	return domain.NewLadoVerifyItem(domain.LadoVerifyRequiredChecks, outcome, code, details)
}

// requiredCheckOutcome is what one CI check's status says: passed or
// skipped passes, failed or cancelled fails, and unknown, queued or in
// progress waits.
func requiredCheckOutcome(status domain.PRCheckStatus) domain.LadoVerifyOutcome {
	switch status {
	case domain.PRCheckPassed, domain.PRCheckSkipped:
		return domain.LadoVerifyPass
	case domain.PRCheckFailed, domain.PRCheckCancelled:
		return domain.LadoVerifyFail
	default:
		return domain.LadoVerifyWait
	}
}

// CommandsInput is what the commands item reads.
type CommandsInput struct {
	// Commands are the project's check commands, in order.
	Commands []domain.LadoCheckCommand
	// Runs are the check runs of this head, in start order.
	Runs []domain.LadoCheckRun
	// RequiredNamed says whether the base branch's rules name required
	// checks (RequiredContexts).
	RequiredNamed bool
	// WorktreeCode is WorktreeReady's answer: empty when the next command
	// can run in the execution's worktree, else worktree_missing,
	// worktree_not_at_head or worktree_changed.
	WorktreeCode string
}

// EvaluateCommands computes the commands item. Without commands it passes as
// none_configured when the rules name required checks, and needs confirming
// as no_tests otherwise: nothing would test the head.
//
// Each command is judged by its newest run of the head, matched by name and
// text, so a run of a command whose text changed since does not count: a
// passed run passes, a running one waits as commands_running, a timed-out
// one fails as command_timed_out, and a failed one or one that could not
// start fails as commands_failed. A command without a run, or whose newest
// run was cancelled, runs next: it waits as commands_running while the
// worktree is ready, else as worktree_not_at_head or worktree_changed, and
// fails as worktree_missing without a worktree. A failure wins over a wait;
// the first command in order names the code. The details list each command
// with the hash of its text and its newest run.
func EvaluateCommands(in CommandsInput) (domain.LadoVerifyItem, error) {
	switch in.WorktreeCode {
	case "", domain.LadoCodeWorktreeMissing, domain.LadoCodeWorktreeNotAtHead, domain.LadoCodeWorktreeChanged:
	default:
		return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the check commands: unknown worktree code %q", in.WorktreeCode)
	}
	if len(in.Commands) == 0 {
		if in.RequiredNamed {
			return domain.NewLadoVerifyItem(domain.LadoVerifyCommands, domain.LadoVerifyPass, domain.LadoCodeNoneConfigured, domain.LadoCommandsDetails{})
		}
		return domain.NewLadoVerifyItem(domain.LadoVerifyCommands, domain.LadoVerifyConfirm, domain.LadoCodeNoTests, domain.LadoCommandsDetails{})
	}
	var details domain.LadoCommandsDetails
	failCode, waitCode := "", ""
	for _, command := range in.Commands {
		entry := domain.LadoVerifyCommandRun{Name: command.Name, CommandSHA256: domain.LadoCheckCommandSHA256(command.Command)}
		run, ran := newestCheckRun(in.Runs, command)
		if ran {
			entry.CheckRunID, entry.Status, entry.ExitCode = run.ID, string(run.Status), run.ExitCode
			if !run.FinishedAt.IsZero() {
				entry.DurationMS = max(run.FinishedAt.Sub(run.StartedAt).Milliseconds(), 0)
			}
		}
		details.Runs = append(details.Runs, entry)
		outcome, code, err := commandOutcome(run, ran, in.WorktreeCode)
		if err != nil {
			return domain.LadoVerifyItem{}, fmt.Errorf("evaluate the check commands: %w", err)
		}
		switch {
		case outcome == domain.LadoVerifyFail && failCode == "":
			failCode = code
		case outcome == domain.LadoVerifyWait && waitCode == "":
			waitCode = code
		}
	}
	switch {
	case failCode != "":
		return domain.NewLadoVerifyItem(domain.LadoVerifyCommands, domain.LadoVerifyFail, failCode, details)
	case waitCode != "":
		return domain.NewLadoVerifyItem(domain.LadoVerifyCommands, domain.LadoVerifyWait, waitCode, details)
	default:
		return domain.NewLadoVerifyItem(domain.LadoVerifyCommands, domain.LadoVerifyPass, "", details)
	}
}

// CommandsToRun returns the commands the head still needs a run of, in
// order: those without a run of their name and text and those whose newest
// such run was cancelled, as EvaluateCommands counts them.
func CommandsToRun(commands []domain.LadoCheckCommand, runs []domain.LadoCheckRun) []domain.LadoCheckCommand {
	var due []domain.LadoCheckCommand
	for _, command := range commands {
		if run, ran := newestCheckRun(runs, command); !ran || run.Status == domain.LadoCheckRunCancelled {
			due = append(due, command)
		}
	}
	return due
}

// newestCheckRun returns the run of command, by its name and text, that
// started last; of runs that started at the same time, the later one in the
// list. A run of the same name with other text is another command's.
func newestCheckRun(runs []domain.LadoCheckRun, command domain.LadoCheckCommand) (domain.LadoCheckRun, bool) {
	var newest domain.LadoCheckRun
	found := false
	for _, run := range runs {
		if run.Name == command.Name && run.Command == command.Command && (!found || !run.StartedAt.Before(newest.StartedAt)) {
			newest, found = run, true
		}
	}
	return newest, found
}

// commandOutcome is what one command's newest run says, ran false when it
// has none.
func commandOutcome(run domain.LadoCheckRun, ran bool, worktreeCode string) (domain.LadoVerifyOutcome, string, error) {
	if !ran || run.Status == domain.LadoCheckRunCancelled {
		// The command runs next, once the worktree is ready.
		switch worktreeCode {
		case domain.LadoCodeWorktreeMissing:
			return domain.LadoVerifyFail, worktreeCode, nil
		case domain.LadoCodeWorktreeNotAtHead, domain.LadoCodeWorktreeChanged:
			return domain.LadoVerifyWait, worktreeCode, nil
		}
		return domain.LadoVerifyWait, domain.LadoCodeCommandsRunning, nil
	}
	switch run.Status {
	case domain.LadoCheckRunPassed:
		return domain.LadoVerifyPass, "", nil
	case domain.LadoCheckRunRunning:
		return domain.LadoVerifyWait, domain.LadoCodeCommandsRunning, nil
	case domain.LadoCheckRunTimedOut:
		return domain.LadoVerifyFail, domain.LadoCodeCommandTimedOut, nil
	case domain.LadoCheckRunFailed, domain.LadoCheckRunError:
		return domain.LadoVerifyFail, domain.LadoCodeCommandsFailed, nil
	}
	return "", "", fmt.Errorf("check run %s has status %q", run.ID, run.Status)
}
