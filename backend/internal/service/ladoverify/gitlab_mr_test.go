package ladoverify

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestTickBuildsARecordForAGitLabMergeRequest is the last link from a
// self-managed GitLab merge request to a LADO verification record: once the
// observer stores the MR as the execution session's PR, Tick verifies its
// head like a GitHub pull request.
func TestTickBuildsARecordForAGitLabMergeRequest(t *testing.T) {
	const mrURL = "https://host/a/b/c/-/merge_requests/1"
	f := newVerifyFixture(t)
	exec := f.addExecution("1", verifyHead1)
	f.reads.updateTask(exec.TaskID, func(task *domain.LadoTask) {
		task.Repo = domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "a/b/c", Host: "host"}
	})
	f.store.update(exec.SessionID, func(e *domain.LadoExecution) { e.PR.URL = mrURL })
	f.host.setGitLab(gitlabGuarded())

	f.tick()
	record := f.record(exec.SessionID, verifyHead1)
	if record.PRURL != mrURL || record.HeadSHA != verifyHead1 {
		t.Fatalf("record = %s at %s, want %s at %s", record.PRURL, record.HeadSHA, mrURL, verifyHead1)
	}
	if len(record.Items) == 0 {
		t.Fatal("record has no items")
	}
	protection := verifyItemOf(t, record, domain.LadoVerifyProtection)
	if protection.Outcome != domain.LadoVerifyPass || protection.Code != "" || !strings.Contains(string(protection.Details), `"provider":"gitlab"`) {
		t.Errorf("protection item = %s %q %s, want a pass for a guarded GitLab branch", protection.Outcome, protection.Code, protection.Details)
	}
	if paths := verifyItemOf(t, record, domain.LadoVerifyPaths); paths.Outcome != domain.LadoVerifyPass {
		t.Errorf("paths item = %s %s %s, want pass", paths.Outcome, paths.Code, paths.Details)
	}
}
