package ladoverify

// Fix requests (decision r of the step 8 plan; docs/LJY-PRD/HARNESS.md §5,
// block_and_request_fix). When one of LADO's own checks fails on a head, the
// worker gets one message saying what failed, while its session lives: the
// project's check commands, the commit message rule, a protected path the
// plan does not name, and a review that asked for changes. CI failures and
// review comments reach the worker through AO's own nudges.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// fixMessageMax caps a fix request; fixLogTailMax caps the end of a check
	// command's output it quotes, fixReviewBodyMax the review it quotes.
	fixMessageMax    = 4 << 10
	fixLogTailMax    = 1 << 10
	fixReviewBodyMax = 2 << 10
	fixFooter        = "Push a fix; LADO checks the new head."
	// cutMark marks where a fix request cut text.
	cutMark = "…"
)

// requestFixes sends a fix request for each item of the head that fails and
// that the worker was not told about yet, and returns the notified list with
// the items it told about. A failure of the check commands waits until none
// of them runs or can start, so that one message names every command that
// failed. A message that could not be sent goes out on a later pass.
func (s *Service) requestFixes(ctx context.Context, in headInputs, ev evaluation, notified []string) []string {
	if s.deps.Sender == nil || !in.live {
		return notified
	}
	notified = slices.Clone(notified)
	for _, item := range ev.items {
		if !fixable(item) || slices.Contains(notified, string(item.Kind)) {
			continue
		}
		if item.Kind == domain.LadoVerifyCommands && !ev.commandsSettled {
			continue
		}
		message, err := s.fixMessage(in, ev, item)
		if err != nil {
			s.deps.Log.Warn("LADO could not write a fix request", "session", in.exec.SessionID, "head", in.head, "item", item.Kind, "err", err)
			continue
		}
		if err := s.deps.Sender.Send(ctx, in.exec.SessionID, message); err != nil {
			s.deps.Log.Warn("LADO could not tell a worker what failed; it tries again", "session", in.exec.SessionID, "head", in.head, "item", item.Kind, "err", err)
			continue
		}
		notified = append(notified, string(item.Kind))
	}
	return notified
}

// fixable reports whether item is a failure LADO asks the worker to fix.
func fixable(item domain.LadoVerifyItem) bool {
	if item.Outcome != domain.LadoVerifyFail {
		return false
	}
	switch item.Kind {
	case domain.LadoVerifyCommands, domain.LadoVerifyCommitMessages:
		return true
	case domain.LadoVerifyPaths:
		return item.Code == domain.LadoCodeProtectedPath
	case domain.LadoVerifyReview:
		return item.Code == domain.LadoCodeChangesRequested
	}
	return false
}

// fixMessage writes the fix request of a failing item: the head, the item,
// what failed and what to do, within fixMessageMax bytes.
func (s *Service) fixMessage(in headInputs, ev evaluation, item domain.LadoVerifyItem) (string, error) {
	var what, body string
	switch item.Kind {
	case domain.LadoVerifyCommands:
		var details domain.LadoCommandsDetails
		if err := json.Unmarshal(item.Details, &details); err != nil {
			return "", err
		}
		what, body = "these check commands of the project did not pass in your worktree", s.commandsFailure(ev, item.Code, details)
	case domain.LadoVerifyCommitMessages:
		var details domain.LadoCommitMessagesDetails
		if err := json.Unmarshal(item.Details, &details); err != nil {
			return "", err
		}
		what, body = "the commit message rule asks for Korean, and these are not in Korean", commitsFailure(details)
	case domain.LadoVerifyPaths:
		var details domain.LadoPathsDetails
		if err := json.Unmarshal(item.Details, &details); err != nil {
			return "", err
		}
		what, body = "you changed these protected paths, which the approved plan does not name", strings.Join(details.Protected, "\n")
	case domain.LadoVerifyReview:
		var details domain.LadoReviewDetails
		if err := json.Unmarshal(item.Details, &details); err != nil {
			return "", err
		}
		what, body = "the code review asked for changes", reviewFailure(ev.reviewRuns, details)
	default:
		return "", fmt.Errorf("no fix request for the %s item", item.Kind)
	}
	header := fmt.Sprintf("LADO checked head %s of your pull request, and the %s item failed: %s.", shortCommit(in.head), item.Kind, what)
	return fitFixMessage(header, body), nil
}

// commandsFailure names each check command whose newest run on the head did
// not pass, with the end of its output.
func (s *Service) commandsFailure(ev evaluation, code string, details domain.LadoCommandsDetails) string {
	var parts []string
	for _, entry := range details.Runs {
		run, ok := checkRunByID(ev.runs, entry.CheckRunID)
		if !ok {
			if code == domain.LadoCodeWorktreeMissing {
				parts = append(parts, entry.Name+" did not run: LADO found no worktree of this session.")
			}
			continue
		}
		var line string
		switch {
		case run.Status == domain.LadoCheckRunFailed && run.ExitCode != nil:
			line = fmt.Sprintf("%s exited with %d.", run.Name, *run.ExitCode)
		case run.Status == domain.LadoCheckRunFailed:
			line = run.Name + " was stopped before it exited."
		case run.Status == domain.LadoCheckRunTimedOut:
			line = run.Name + " ran longer than its time limit."
		case run.Status == domain.LadoCheckRunError:
			line = fmt.Sprintf("%s could not run: %s", run.Name, run.ErrorMessage)
		default:
			continue
		}
		if tail := s.logTail(run); tail != "" {
			line += " The end of its output:\n" + tail
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, "\n\n")
}

// logTail is the end of a check run's log, at most fixLogTailMax bytes.
func (s *Service) logTail(run domain.LadoCheckRun) string {
	if s.deps.Blobs == nil || run.LogSHA256 == "" {
		return ""
	}
	data, err := s.deps.Blobs.Get(run.LogSHA256, MaxCommandOutput)
	if err != nil {
		return ""
	}
	return tailBytes(strings.TrimRight(string(data), "\n"), fixLogTailMax)
}

func checkRunByID(runs []domain.LadoCheckRun, id string) (domain.LadoCheckRun, bool) {
	if id == "" {
		return domain.LadoCheckRun{}, false
	}
	i := slices.IndexFunc(runs, func(run domain.LadoCheckRun) bool { return run.ID == id })
	if i < 0 {
		return domain.LadoCheckRun{}, false
	}
	return runs[i], true
}

// commitsFailure lists the commits, and the PR title, that are not in
// Korean.
func commitsFailure(details domain.LadoCommitMessagesDetails) string {
	var lines []string
	for _, commit := range details.Commits {
		if commit.Result == MessageNotKorean {
			lines = append(lines, shortCommit(commit.SHA)+" "+commit.Subject)
		}
	}
	if details.PRTitle.Result == MessageNotKorean {
		lines = append(lines, "The pull request title: "+details.PRTitle.Text)
	}
	return strings.Join(lines, "\n")
}

// reviewFailure is the deciding review's text, at most fixReviewBodyMax
// bytes.
func reviewFailure(runs []domain.ReviewRun, details domain.LadoReviewDetails) string {
	i := slices.IndexFunc(runs, func(run domain.ReviewRun) bool { return run.ID == details.RunID })
	if i < 0 || strings.TrimSpace(runs[i].Body) == "" {
		return "The review has no text here; read it on the pull request."
	}
	return headBytes(strings.TrimSpace(runs[i].Body), fixReviewBodyMax)
}

// fitFixMessage joins a fix request's header, body and footer, cutting the
// body so that the message stays within fixMessageMax bytes.
func fitFixMessage(header, body string) string {
	if body == "" {
		return header + "\n\n" + fixFooter
	}
	room := fixMessageMax - len(header) - len(fixFooter) - len("\n\n\n\n")
	return header + "\n\n" + headBytes(body, room) + "\n\n" + fixFooter
}

// headBytes is text cut to at most limit bytes at a character boundary,
// with cutMark where it was cut.
func headBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := max(limit-len(cutMark), 0)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + cutMark
}

// tailBytes is the end of text, at most limit bytes from a character
// boundary, with cutMark where it was cut.
func tailBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - max(limit-len(cutMark), 0)
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return cutMark + text[start:]
}

// shortCommit is the first seven characters of a commit id.
func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
