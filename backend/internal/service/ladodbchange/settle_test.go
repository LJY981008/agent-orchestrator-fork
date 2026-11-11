package ladodbchange

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// merged marks the fixture's PR merged, as AO observed it.
func (f *jobFixture) merged() domain.LadoExecution {
	f.exec.PR.Merged = true
	f.store.executions[testSession] = f.exec
	f.store.setMerged(testPRURL)
	return f.exec
}

func (f *jobFixture) record() *domain.LadoVerification {
	record := f.store.records[string(testSession)+"\x00"+f.head]
	return &record
}

func TestSettledMergedOutsideLADO(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	exec := f.merged()
	ctx := context.Background()
	for range 2 {
		if err := f.s.Settled(ctx, exec, f.record(), nil); err != nil {
			t.Fatal(err)
		}
	}
	pre, after := f.run(domain.LadoDBChangeRunPreMerge), f.run(domain.LadoDBChangeRunAfterDeploy)
	if pre.State != domain.LadoDBChangeRunAwaitingHuman || pre.Trigger != domain.LadoDBChangeTriggerMergedUnapplied {
		t.Fatalf("pre-merge run = %+v", pre)
	}
	if after.State != domain.LadoDBChangeRunAwaitingHuman || after.Trigger != domain.LadoDBChangeTriggerAfterMerge {
		t.Fatalf("after-deploy run = %+v", after)
	}
	if got := f.noticeKeys(); !slices.Equal(got, []string{"db_merged_unapplied", "db_after_deploy"}) {
		t.Fatalf("notices = %v", got)
	}
	if !strings.Contains(f.store.notices[0].Text, "2개") || !strings.Contains(f.store.notices[1].Text, "1개") {
		t.Fatalf("notices = %+v", f.store.notices)
	}
	summaries, err := f.s.ProjectDBChanges(ctx, testProject)
	if err != nil || len(summaries) != 1 || summaries[0].State != domain.LadoDBChangeBoardMergedUnapplied || summaries[0].Pending != 2 {
		t.Fatalf("summaries = %+v, %v", summaries, err)
	}

	// The developer applies the pre-merge files: no preflight and no merge.
	f.live.err = errors.New("the host is not asked")
	if _, err := f.apply(domain.LadoDBChangePreMerge, "db/002_add.sql", "db/004_boom.sql"); err != nil {
		t.Fatal(err)
	}
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone || f.merger.merges != 0 || f.merger.reads != 0 {
		t.Fatalf("run = %+v, merges %d, reads %d", run, f.merger.merges, f.merger.reads)
	}
	summaries, _ = f.s.ProjectDBChanges(ctx, testProject)
	if len(summaries) != 1 || summaries[0].State != domain.LadoDBChangeBoardAfterDeployPending || summaries[0].Pending != 1 {
		t.Fatalf("summaries = %+v", summaries)
	}

	// Then the after-deploy file, by the button.
	if _, err := f.apply(domain.LadoDBChangeAfterDeploy, "db/003_drop.sql"); err != nil {
		t.Fatal(err)
	}
	f.tick()
	if run := f.run(domain.LadoDBChangeRunAfterDeploy); run.State != domain.LadoDBChangeRunDone {
		t.Fatalf("run = %+v", run)
	}
	if got := f.applied(); !strings.HasSuffix(got, "-- lado:after-deploy\nDROP TABLE old;\n") || !strings.HasPrefix(got, "ALTER TABLE a ADD b int;\n") {
		t.Fatalf("applied = %q", got)
	}
	if summaries, _ := f.s.ProjectDBChanges(ctx, testProject); len(summaries) != 0 {
		t.Fatalf("summaries = %+v", summaries)
	}
	// Settling again finds nothing left.
	if err := f.s.Settled(ctx, exec, f.record(), nil); err != nil || len(f.store.notices) != 2 {
		t.Fatalf("Settled = %v, notices %+v", err, f.store.notices)
	}
}

func TestSettledApprovalRunOfAPRMergedOutside(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	if err := f.s.Settled(context.Background(), f.merged(), f.record(), nil); err != nil {
		t.Fatal(err)
	}
	run := f.run(domain.LadoDBChangeRunPreMerge)
	if run.State != domain.LadoDBChangeRunAwaitingHuman || run.Trigger != domain.LadoDBChangeTriggerApproval || !strings.Contains(run.Error, "outside LADO") {
		t.Fatalf("run = %+v", run)
	}
	f.tick()
	if f.applied() != "" {
		t.Fatalf("applied %q", f.applied())
	}
}

func TestApprovalRunOfAMergedPRWaitsForTheDeveloper(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.merged() // AO saw the merge before LADO settled it
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunAwaitingHuman || f.applied() != "" {
		t.Fatalf("run = %+v, applied %q", run, f.applied())
	}
	if _, err := f.apply(domain.LadoDBChangePreMerge, "db/002_add.sql", "db/004_boom.sql"); err != nil {
		t.Fatal(err)
	}
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone || f.merger.merges != 0 || !strings.Contains(f.applied(), "ADD c int") {
		t.Fatalf("run = %+v, merges %d, applied %q", run, f.merger.merges, f.applied())
	}
}

func TestSettledAfterLADOMergedFindsNothing(t *testing.T) {
	f := newJobFixture(t, map[string]string{"db/002_add.sql": "ALTER TABLE a ADD b int;\n"})
	f.s.IntegrationApproved(context.Background(), f.exec, []domain.LadoIntegrationApproval{f.approval})
	f.tick()
	if err := f.s.Settled(context.Background(), f.merged(), f.record(), nil); err != nil {
		t.Fatal(err)
	}
	if len(f.store.runs) != 1 || len(f.store.notices) != 0 {
		t.Fatalf("runs %+v, notices %+v", f.store.runs, f.store.notices)
	}
}

func TestSettledWithSettingsOff(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	settings := f.store.settings[settingsKey(testProject, "")]
	settings.Enabled = false
	f.store.settings[settingsKey(testProject, "")] = settings
	f.store.readErr = errors.New("not read")
	if err := f.s.Settled(context.Background(), f.merged(), f.record(), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Settled(context.Background(), f.exec, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.store.runs) != 0 || len(f.store.notices) != 0 {
		t.Fatalf("runs %+v, notices %+v", f.store.runs, f.store.notices)
	}
	// An open PR is not settled.
	open := f.exec
	open.PR.Merged = false
	if err := f.s.Settled(context.Background(), open, f.record(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSettledWithoutARecordUsesTheInputs(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	delete(f.store.records, string(testSession)+"\x00"+f.head)
	in := f.input()
	if err := f.s.Settled(context.Background(), f.merged(), nil, &in); err != nil {
		t.Fatal(err)
	}
	f.run(domain.LadoDBChangeRunPreMerge)
	f.run(domain.LadoDBChangeRunAfterDeploy)
	view, err := f.s.ExecutionDBChanges(context.Background(), testSession)
	if err != nil || len(view.PRs) != 1 || len(view.PRs[0].Files) != 3 || len(view.PRs[0].Runs) != 2 {
		t.Fatalf("view = %+v, %v", view, err)
	}
	if _, err := f.apply(domain.LadoDBChangePreMerge, "db/002_add.sql", "db/004_boom.sql"); err != nil {
		t.Fatal(err)
	}
	f.tick()
	if run := f.run(domain.LadoDBChangeRunPreMerge); run.State != domain.LadoDBChangeRunDone {
		t.Fatalf("run = %+v", run)
	}
}

func TestApplyCheckedFilesOfAHeadLADODoesNotKnow(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	delete(f.store.records, string(testSession)+"\x00"+f.head)
	f.merged()
	// A file that is not the head's blob is refused.
	bad := domain.LadoDBChangeApplyRequest{SessionID: testSession, PRURL: testPRURL, HeadSHA: f.head, Phase: domain.LadoDBChangePreMerge,
		Files: []domain.LadoDBChangeFileRef{{Path: "db/002_add.sql", BlobSHA: f.repo.blob(f.head, "db/004_boom.sql"), Phase: domain.LadoDBChangePreMerge}}}
	if _, err := f.s.ApplyDBChanges(context.Background(), bad, f.presence.fn); err == nil {
		t.Fatal("a wrong blob was queued")
	} else if code, _ := apiCode(t, err); code != "LADO_DB_CHANGE_FILES_CHANGED" {
		t.Fatalf("code = %s", code)
	}
	if _, err := f.apply(domain.LadoDBChangeAfterDeploy, "db/002_add.sql"); err == nil {
		t.Fatal("a pre-merge file was queued as after-deploy")
	}
	run, err := f.apply(domain.LadoDBChangePreMerge, "db/002_add.sql")
	if err != nil || run.Trigger != domain.LadoDBChangeTriggerMergedUnapplied {
		t.Fatalf("run = %+v, %v", run, err)
	}
	f.tick()
	if got := f.applied(); got != "ALTER TABLE a ADD b int;\n" {
		t.Fatalf("applied = %q", got)
	}
}

func TestSettledErrorsUntilItGivesUpAfterAnHour(t *testing.T) {
	f := newJobFixture(t, defaultHeadFiles())
	exec := f.merged()
	f.store.readErr = errors.New("database locked")
	if err := f.s.Settled(context.Background(), exec, f.record(), nil); err == nil {
		t.Fatal("Settled hid a failed read")
	}
	f.advance(30 * time.Minute)
	if err := f.s.Settled(context.Background(), exec, f.record(), nil); err == nil {
		t.Fatal("Settled gave up early")
	}
	f.advance(31 * time.Minute)
	if err := f.s.Settled(context.Background(), exec, f.record(), nil); err != nil {
		t.Fatalf("Settled = %v after an hour", err)
	}
	if got := f.noticeKeys(); !slices.Equal(got, []string{"db_unknown"}) {
		t.Fatalf("notices = %v", got)
	}
	// Without a record or inputs LADO cannot tell either.
	f.store.readErr = nil
	if err := f.s.Settled(context.Background(), exec, nil, nil); err == nil {
		t.Fatal("Settled without a record or inputs passed")
	}
}
