package ports

import (
	"errors"
	"fmt"
	"testing"
)

func TestWorkspaceSetupErrorKeepsWrappedText(t *testing.T) {
	inner := fmt.Errorf("gitworktree: worktree add: exit status 1: %w", ErrGitLFSMissing)
	setup := &WorkspaceSetupError{Repo: "front", Summary: "repository front uses Git LFS", Fix: "install Git LFS", Err: inner}
	wrapped := fmt.Errorf("spawn s1: workspace: %w", setup)

	if setup.Error() != inner.Error() {
		t.Fatalf("Error() = %q, want %q", setup.Error(), inner.Error())
	}
	if !errors.Is(wrapped, ErrGitLFSMissing) {
		t.Fatal("errors.Is(ErrGitLFSMissing) = false through WorkspaceSetupError")
	}
	got, ok := WorkspaceSetupSummary(wrapped)
	if !ok || got != "repository front uses Git LFS. install Git LFS" {
		t.Fatalf("summary = %q, %v", got, ok)
	}
}

func TestWorkspaceSetupSummary(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		want   string
		wantOK bool
	}{
		{name: "nil", err: nil},
		{name: "plain error", err: errors.New("boom")},
		{name: "empty summary", err: &WorkspaceSetupError{Err: errors.New("x")}},
		{name: "no fix", err: &WorkspaceSetupError{Summary: "could not copy workspace folder data."}, want: "could not copy workspace folder data.", wantOK: true},
		{name: "fix", err: &WorkspaceSetupError{Summary: "a", Fix: " do b "}, want: "a. do b", wantOK: true},
	}
	for _, tc := range cases {
		got, ok := WorkspaceSetupSummary(tc.err)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: WorkspaceSetupSummary = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
	if (&WorkspaceSetupError{Summary: "only"}).Error() != "only" {
		t.Error("Error() without Err should fall back to Summary")
	}
}
