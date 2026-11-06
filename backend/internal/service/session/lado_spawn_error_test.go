package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// LADO #22: a workspace setup failure reads as its short summary first.

// ladoQueueProblemBytes mirrors ladoqueue's maxProblemBytes, the part of the
// message the LADO queue keeps.
const ladoQueueProblemBytes = 512

func ladoSpawnWorkspaceErr(setup error) error {
	inner := fmt.Errorf("gitworktree: workspace repo \"front\" worktree add branch ao/mer-1: %w", setup)
	return fmt.Errorf("spawn mer-1: %w: %w", sessionmanager.ErrWorkspaceCreate, inner)
}

func TestLadoSpawnErrorPutsTheGitLFSSummaryFirst(t *testing.T) {
	longPath := "/home/user/.ao/data/worktrees/project/" + strings.Repeat("deep/", 120)
	gitOut := fmt.Errorf("/usr/bin/git -C %s worktree add -b ao/mer-1 %s: exit status 1: %w", longPath, longPath,
		errors.New("Preparing worktree\ngit-lfs filter-process: git-lfs: command not found\nfatal: the remote end hung up unexpectedly"))
	setup := &ports.WorkspaceSetupError{
		Repo:    "front",
		Summary: "repository front uses Git LFS (.gitattributes has filter=lfs), but git-lfs is not installed on this computer",
		Fix:     "install Git LFS (https://git-lfs.com), run git lfs install, then start the session again",
		Err:     errors.Join(ports.ErrGitLFSMissing, gitOut),
	}
	err := ladoSpawnWorkspaceErr(setup)

	var apiErr *apierr.Error
	if !errors.As(toSpawnAPIError(err), &apiErr) {
		t.Fatalf("toSpawnAPIError(%v) is not an apierr", err)
	}
	if apiErr.Code != "WORKSPACE_CREATE_FAILED" || apiErr.Kind != apierr.KindConflict {
		t.Fatalf("code/kind = %s/%v, want WORKSPACE_CREATE_FAILED conflict", apiErr.Code, apiErr.Kind)
	}
	want, _ := ports.WorkspaceSetupSummary(setup)
	if !strings.HasPrefix(apiErr.Message, want) {
		t.Fatalf("message = %q, want prefix %q", apiErr.Message, want)
	}
	// The LADO queue shows "<code>: <message>" cut to 512 bytes.
	shown := apiErr.Code + ": " + apiErr.Message
	if len(shown) > ladoQueueProblemBytes {
		shown = shown[:ladoQueueProblemBytes]
	}
	for _, part := range []string{"front", "git-lfs is not installed", "install Git LFS"} {
		if !strings.Contains(shown, part) {
			t.Fatalf("first %d bytes %q lack %q", ladoQueueProblemBytes, shown, part)
		}
	}
	if apiErr.Details["repo"] != "front" {
		t.Fatalf("details.repo = %v, want front", apiErr.Details["repo"])
	}
	if apiErr.Details["cause"] != err.Error() {
		t.Fatalf("details.cause = %v, want the full error", apiErr.Details["cause"])
	}
	if apiErr.Details["suggestedFix"] != setup.Fix {
		t.Fatalf("details.suggestedFix = %v, want %q", apiErr.Details["suggestedFix"], setup.Fix)
	}
}

func TestLadoSpawnErrorWorktreeAddSummaryHasNoSuggestedFix(t *testing.T) {
	setup := &ports.WorkspaceSetupError{
		Repo:    "back",
		Summary: "could not create the worktree of repository back: fatal: invalid reference: origin/dev",
		Err:     errors.New(strings.Repeat("x", 2000) + "\nfatal: invalid reference: origin/dev"),
	}
	var apiErr *apierr.Error
	if !errors.As(toSpawnAPIError(ladoSpawnWorkspaceErr(setup)), &apiErr) {
		t.Fatal("not an apierr")
	}
	if !strings.HasPrefix(apiErr.Message, "could not create the worktree of repository back: fatal: invalid reference: origin/dev.") {
		t.Fatalf("message = %q", apiErr.Message)
	}
	if _, ok := apiErr.Details["suggestedFix"]; ok {
		t.Fatalf("details.suggestedFix set for a failure that is not git-lfs: %v", apiErr.Details)
	}
}

func TestLadoSpawnErrorWithoutSetupErrorUnchanged(t *testing.T) {
	err := fmt.Errorf("spawn mer-1: %w: %w", sessionmanager.ErrWorkspaceCreate, errors.New("worktree add failed: exit status 1"))
	var apiErr *apierr.Error
	if !errors.As(toSpawnAPIError(err), &apiErr) {
		t.Fatal("not an apierr")
	}
	if apiErr.Code != "WORKSPACE_CREATE_FAILED" || apiErr.Message != err.Error() || apiErr.Details != nil {
		t.Fatalf("got %s %q %v, want the unchanged message and no details", apiErr.Code, apiErr.Message, apiErr.Details)
	}
}
