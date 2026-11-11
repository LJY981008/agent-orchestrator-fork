package ladodbchange

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestExecutionDBChangesShowsEachFile(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	ctx := context.Background()
	view, err := f.s.ExecutionDBChanges(ctx, testSession)
	if err != nil || view.TaskID != testTask || len(view.PRs) != 1 {
		t.Fatalf("view = %+v, %v", view, err)
	}
	for _, file := range view.PRs[0].Files {
		if file.Status != domain.LadoDBChangeFilePending || file.Attempts != 0 {
			t.Fatalf("file before any run = %+v", file)
		}
	}

	if err := os.WriteFile(f.flagPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.s.IntegrationApproved(ctx, f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick()
	view, err = f.s.ExecutionDBChanges(ctx, testSession)
	if err != nil || len(view.PRs) != 1 {
		t.Fatalf("view = %+v, %v", view, err)
	}
	pr := view.PRs[0]
	if pr.PRURL != testPRURL || pr.HeadSHA != f.head || len(pr.Runs) != 1 || pr.Runs[0].State != domain.LadoDBChangeRunFailed {
		t.Fatalf("pr = %+v", pr)
	}
	status := map[string]domain.LadoDBChangeFileView{}
	for _, file := range pr.Files {
		status[file.Path] = file
	}
	if got := status["db/002_add.sql"]; got.Status != domain.LadoDBChangeFileApplied || got.AppliedAt == nil || got.Attempts != 1 {
		t.Fatalf("002 = %+v", got)
	}
	if got := status["db/003_drop.sql"]; got.Status != domain.LadoDBChangeFilePending || got.Phase != domain.LadoDBChangeAfterDeploy {
		t.Fatalf("003 = %+v", got)
	}
	failed := status["db/004_boom.sql"]
	if failed.Status != domain.LadoDBChangeFileFailed || failed.OutputSHA256 == "" || !strings.Contains(failed.LastMessage, "exit code 3") ||
		strings.Contains(failed.LastMessage, "s3cr3t-pa55") {
		t.Fatalf("004 = %+v", failed)
	}

	summaries, err := f.s.ProjectDBChanges(ctx, testProject)
	if err != nil || len(summaries) != 1 || summaries[0].State != domain.LadoDBChangeBoardApplyFailed || summaries[0].Pending != 1 ||
		summaries[0].Failed != 1 || summaries[0].TaskID != testTask || !strings.Contains(summaries[0].Message, "004") {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}
	if _, err := f.s.ProjectDBChanges(ctx, "missing"); err == nil {
		t.Fatal("a missing project has a board")
	}
	if _, err := f.s.ExecutionDBChanges(ctx, "missing"); err == nil {
		t.Fatal("a missing execution has a view")
	} else if code, _ := apiCode(t, err); code != "LADO_EXECUTION_NOT_FOUND" {
		t.Fatalf("code = %s", code)
	}
}

func TestDBChangeFileReadsOnlyTheHeadsFiles(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	ctx := context.Background()
	content, err := f.s.DBChangeFile(ctx, testSession, testPRURL, f.repo.blob(f.head, "db/003_drop.sql"))
	if err != nil || content.Path != "db/003_drop.sql" || content.Content != "-- lado:after-deploy\nDROP TABLE old;\n" || content.Truncated {
		t.Fatalf("content = %+v, %v", content, err)
	}
	for _, blob := range []string{f.repo.blob(f.head, "db/001_init.sql"), "nope", strings.Repeat("0", 40)} {
		if _, err := f.s.DBChangeFile(ctx, testSession, testPRURL, blob); err == nil {
			t.Fatalf("blob %s was read", blob)
		} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_NOT_FOUND" {
			t.Fatalf("code = %s", code)
		}
	}
	if _, err := f.s.DBChangeFile(ctx, testSession, "https://github.com/o/other/pull/1", f.repo.blob(f.head, "db/003_drop.sql")); err == nil {
		t.Fatal("another PR's file was read")
	}
}

func TestBoardState(t *testing.T) {
	pre, after := domain.LadoDBChangeRunPreMerge, domain.LadoDBChangeRunAfterDeploy
	cases := []struct {
		kind   domain.LadoDBChangeRunKind
		state  domain.LadoDBChangeRunState
		merged bool
		want   domain.LadoDBChangeBoardState
	}{
		{pre, domain.LadoDBChangeRunQueued, false, domain.LadoDBChangeBoardApplyWaiting},
		{pre, domain.LadoDBChangeRunApplying, false, domain.LadoDBChangeBoardApplying},
		{pre, domain.LadoDBChangeRunApplied, false, domain.LadoDBChangeBoardApplying},
		{pre, domain.LadoDBChangeRunMerging, false, domain.LadoDBChangeBoardApplying},
		{pre, domain.LadoDBChangeRunFailed, false, domain.LadoDBChangeBoardApplyFailed},
		{pre, domain.LadoDBChangeRunMergeFailed, false, domain.LadoDBChangeBoardMergeFailed},
		{pre, domain.LadoDBChangeRunAwaitingHuman, false, domain.LadoDBChangeBoardPreflightBlocked},
		{pre, domain.LadoDBChangeRunAwaitingHuman, true, domain.LadoDBChangeBoardMergedUnapplied},
		{pre, domain.LadoDBChangeRunFailed, true, domain.LadoDBChangeBoardMergedUnapplied},
		{pre, domain.LadoDBChangeRunApplying, true, domain.LadoDBChangeBoardApplying},
		{after, domain.LadoDBChangeRunAwaitingHuman, true, domain.LadoDBChangeBoardAfterDeployPending},
		{after, domain.LadoDBChangeRunQueued, true, domain.LadoDBChangeBoardApplyWaiting},
		{after, domain.LadoDBChangeRunFailed, true, domain.LadoDBChangeBoardApplyFailed},
	}
	for _, c := range cases {
		if got := boardState(domain.LadoDBChangeRun{Kind: c.kind, State: c.state}, c.merged); got != c.want {
			t.Errorf("boardState(%s, %s, merged %v) = %s, want %s", c.kind, c.state, c.merged, got, c.want)
		}
	}
}
