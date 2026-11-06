package ports

import (
	"errors"
	"strings"
)

// LADO #22: a short, user-facing reason for a failed session workspace setup.

// ErrGitLFSMissing reports that a repository needs Git LFS to check out but
// git-lfs is not installed on this computer.
var ErrGitLFSMissing = errors.New("workspace: git-lfs is not installed")

// WorkspaceSetupError wraps a workspace setup failure with a short summary
// and a suggested fix. Error() is the wrapped error's text unchanged, so logs
// and existing callers see the same message; callers that show the failure to
// a person use WorkspaceSetupSummary instead.
type WorkspaceSetupError struct {
	// Repo is the repository folder name the failure belongs to, if any.
	Repo string
	// Summary says what failed, in one sentence without a trailing period.
	Summary string
	// Fix says what the user can do about it. It may be empty.
	Fix string
	// Err is the underlying error.
	Err error
}

func (e *WorkspaceSetupError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Summary
	}
	return e.Err.Error()
}

func (e *WorkspaceSetupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// WorkspaceSetupSummary returns "<Summary>. <Fix>" for the first
// WorkspaceSetupError in err's chain. It reports false when there is none or
// its summary is empty.
func WorkspaceSetupSummary(err error) (string, bool) {
	var setup *WorkspaceSetupError
	if !errors.As(err, &setup) || setup == nil {
		return "", false
	}
	summary := strings.TrimRight(strings.TrimSpace(setup.Summary), ".")
	if summary == "" {
		return "", false
	}
	summary += "."
	if fix := strings.TrimSpace(setup.Fix); fix != "" {
		summary += " " + fix
	}
	return summary, true
}
